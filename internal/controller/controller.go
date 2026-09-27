// Package controller is the MVP control loop: it polls GitHub for an explicit
// request, borrows the GPU safely, runs the job, and puts everything back.
//
// The controller is deliberately thin (docs/mvp.md §6): one in-flight job, no
// leader election, no run store. What it does own is the order of external
// effects and the write-ahead record of them, because that is the part that
// cannot be recovered from anywhere else. It never decides whether a candidate
// is good; it publishes what the measurement found and opens a PR for a human.
package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/lease"
	"github.com/nomanoma121/llm-bench/internal/operator"
)

// Issue is one request as GitHub presents it.
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
}

// PullRequest is one result PR.
type PullRequest struct {
	Head  string
	Base  string
	Title string
	Body  string
}

// Gateway is the GitHub side of the loop. The controller only reads issues,
// writes labels and comments, and opens pull requests.
type Gateway interface {
	// Pending returns open issues carrying a kind label and no state label.
	Pending(ctx context.Context, labels operator.Labels) ([]Issue, error)
	Label(ctx context.Context, issue int, label string) error
	Unlabel(ctx context.Context, issue int, label string) error
	Comment(ctx context.Context, issue int, body string) error
	OpenPR(ctx context.Context, pr PullRequest) (int, error)
	// Claimed reports whether the issue already carries a state label, which
	// is how the controller proves its own claim landed.
	Claimed(ctx context.Context, issue int, labels operator.Labels) (bool, error)
	// EnsureLabels creates the state labels the controller writes.
	EnsureLabels(ctx context.Context, labels operator.Labels) error
}

// Pauser stops and restarts the shared inference workload. It is bound per
// job, because the pause and restore branches are deterministic per job, so a
// retry after a restart acts on the same PRs instead of opening new ones. A
// not-converged pause is retried by the loop, never treated as a failure.
type Pauser interface {
	Pause(ctx context.Context, jobID string) error
	Restore(ctx context.Context, jobID string) error
}

// Sandbox is the GPU execution environment of one job.
type Sandbox interface {
	// Ensure makes a ready sandbox available for the job.
	Ensure(ctx context.Context, jobID string) error
	// Put writes a file into the sandbox. The job spec travels this way: the
	// CLI reads a file, and a file can be quoted, re-read and re-validated,
	// which a stdin stream cannot.
	Put(ctx context.Context, jobID, path string, content []byte) error
	// Exec runs the benchmark CLI inside it and returns its stdout.
	Exec(ctx context.Context, jobID string, argv []string, env map[string]string) ([]byte, error)
	// Delete removes the sandbox. done=false means "still terminating", which
	// the caller retries without releasing the lease. It must be safe to call
	// when nothing exists.
	Delete(ctx context.Context, jobID string) (bool, error)
}

// ErrNotConverged is returned by a Pauser whose external state is not where it
// must be yet (an open pause PR, a workload that has not stopped). The loop
// retries; it is not a failure.
var ErrNotConverged = errors.New("controller: not converged")

// Request is one accepted job.
type Request struct {
	Issue Issue
	JobID string
	Kind  job.Kind
	Spec  job.Spec
	// SpecYAML is the validated spec as it is handed to the benchmark CLI, so
	// the executed spec is the one the controller validated.
	SpecYAML []byte
	Digest   string
	Model    operator.MVPModel
	// Env is extra environment for the sandbox command (for example the
	// short-lived git token).
	Env map[string]string
	// Branch and Commit are the pushed result, filled in by the measurement.
	Branch string
	Commit string
	// PullRequest is the number of the opened PR, 0 before it exists.
	PullRequest int
}

// Config is the controller's operator-owned configuration.
type Config struct {
	Repo          string
	DefaultBranch string
	Labels        operator.Labels
	Models        []operator.MVPModel
	Constraints   job.Constraints
	// ReservedArgs maps an engine to the flags its adapter owns. The reserved
	// list is per engine: using the first engine's list for every job would let
	// a FreeToken job set llama.cpp's flags and vice versa.
	ReservedArgs map[string][]string
	Sandbox      operator.MVPSandbox
	// LLMBench is the argv prefix that runs the CLI inside the sandbox.
	LLMBench []string
	// HolderIdentity prefixes the lease holder. A single-replica Deployment
	// uses one stable value, which is what makes an interrupted job
	// recognisable as its own after a restart.
	HolderIdentity string
	// GitToken mints the short-lived installation token the sandbox uses to
	// push its branch. Empty means the sandbox gets no token and --push fails,
	// which is the honest outcome for a controller without App credentials.
	GitToken func(ctx context.Context) (string, error)
	// PauseTimeout bounds how long the loop waits for a pause to converge.
	PauseTimeout time.Duration
	// PollInterval is how often a not-converged pause is retried.
	PollInterval time.Duration
	// LeaseRenewInterval is how often the lease is renewed while a job runs.
	// When it is zero it is derived from LeaseDuration, so it can never be
	// longer than the lease itself.
	LeaseRenewInterval time.Duration
	// LeaseDuration is the lifetime of the lease, used to derive a safe
	// renewal interval.
	LeaseDuration time.Duration
}

// Controller runs jobs one at a time.
type Controller struct {
	Config  Config
	Gateway Gateway
	Lease   lease.Lease
	Pauser  Pauser
	Sandbox Sandbox
	Now     func() time.Time
	Logf    func(format string, args ...any)

	// keeper is the lease renewal loop of the job we currently hold. It lives
	// across recovery passes: a cleanup waiting for a human merge can last
	// hours, and a lease that expires while we still hold the work would let
	// another instance take the GPU away mid-restore.
	mu     sync.Mutex
	keeper context.CancelFunc
}

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Controller) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// RunOnce polls for a request and runs one job when it finds one. It returns
// true when it handled something, so the caller can poll again immediately
// instead of sleeping.
func (c *Controller) RunOnce(ctx context.Context) (bool, error) {
	issues, err := c.Gateway.Pending(ctx, c.Config.Labels)
	if err != nil {
		return false, err
	}
	if len(issues) == 0 {
		return false, nil
	}
	// One job at a time: the lease rejects the rest anyway, and taking them in
	// order keeps the queue visible in the Issues themselves. handled is true
	// only when this poll actually moved a job forward — a pending Issue whose
	// lease is busy must not turn the loop into a spin against the GitHub API.
	progressed, err := c.run(ctx, issues[0])
	if err != nil {
		c.logf("issue #%d: %v", issues[0].Number, err)
	}
	return progressed, nil
}

// Run is the control loop: it recovers unfinished work periodically and polls
// for new requests. It returns when the context is cancelled, which is also
// how a Deployment shutdown asks it to stop: the in-flight job is cancelled,
// and its cleanup runs on a context that survives cancellation.
func (c *Controller) Run(ctx context.Context, poll, recovery time.Duration) error {
	if poll <= 0 {
		poll = 15 * time.Second
	}
	if recovery <= 0 {
		recovery = time.Minute
	}
	if c.Config.LeaseDuration > 0 && c.Config.LeaseRenewInterval >= c.Config.LeaseDuration {
		return fmt.Errorf("controller: the lease renewal interval (%s) must be shorter than the lease duration (%s)", c.Config.LeaseRenewInterval, c.Config.LeaseDuration)
	}
	if err := c.Gateway.EnsureLabels(ctx, c.Config.Labels); err != nil {
		return err
	}
	if err := c.Recover(ctx); err != nil {
		c.logf("initial recovery: %v", err)
	}
	nextRecovery := time.Now().Add(recovery)
	for {
		if ctx.Err() != nil {
			return nil
		}
		if !time.Now().Before(nextRecovery) {
			if err := c.Recover(ctx); err != nil {
				c.logf("recovery: %v", err)
			}
			nextRecovery = time.Now().Add(recovery)
		}
		// Serving a request is not a reason to poll in a tight loop afterwards:
		// the delay is recomputed every round, so a handled job only skips the
		// wait for the next poll rather than replacing the interval.
		delay := poll
		handled, err := c.RunOnce(ctx)
		if err != nil {
			c.logf("poll: %v", err)
		}
		if handled {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// Recover reconciles the durable state after a restart or a long gap.
//
// It never re-runs a job: a phase that may already have executed the
// measurement is finished as a failure, because a second run would publish a
// different result for the same request. What it always completes is
// restoration: the sandbox is deleted, the workload is restored and the lease
// is released.
func (c *Controller) Recover(ctx context.Context) error {
	record, err := c.Lease.Get(ctx)
	if err != nil {
		return err
	}
	if record.JobID == "" {
		return nil
	}
	if record.Holder == "" {
		// The lease was released: the job is finished and must not be
		// resurrected. What may still be missing is the label mirror, if the
		// controller died between writing the labels and releasing the lease.
		c.stopLeaseKeeper()
		if record.Outcome != "" && record.Issue > 0 {
			if err := c.syncLabels(ctx, record.Issue, record.Outcome); err != nil {
				return err
			}
		}
		return nil
	}
	holder := c.holderFor(record.JobID)
	if record.Holder == holder {
		// Ours: an interrupted job of this very process. Ownership is extended
		// first, because a cleanup can wait a long time for a human merge and
		// the lease must not expire while we still own the work.
		if err := c.Lease.Renew(ctx, holder); err != nil {
			return fmt.Errorf("renew our own lease before recovering: %w", err)
		}
		// A restart leaves the keeper dead but the lease alive. It is started
		// again here, because a cleanup may wait for a human merge for far
		// longer than the lease duration, and the renewal has to continue until
		// the lease is released.
		c.keepLease(context.WithoutCancel(ctx), holder, func() {})
		return c.reconcileLogged(ctx, record)
	}
	// Someone else's live lease is fenced: taking it over while its holder is
	// still running could delete a sandbox that is being measured in. Only an
	// expired lease is recoverable, and then only by taking it first.
	if !record.ExpiresAt.IsZero() && c.now().Before(record.ExpiresAt) {
		return nil
	}
	previous := record.Holder
	acquired, err := c.Lease.Acquire(ctx, holder, record, true)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	record.Holder = holder
	c.logf("taking over job %s left in phase %q by %q", record.JobID, record.Phase, previous)
	c.keepLease(context.WithoutCancel(ctx), holder, func() {})
	return c.reconcileLogged(ctx, record)
}

// reconcileLogged runs reconcile and treats a not-converged cleanup as the
// normal waiting state it is: the loop should not report an error for a
// restore that is waiting for a human to merge the PR.
func (c *Controller) reconcileLogged(ctx context.Context, record lease.Record) error {
	err := c.reconcile(ctx, record)
	if err != nil && errors.Is(err, ErrNotConverged) {
		c.logf("job %s is waiting for restoration to converge: %v", record.JobID, err)
		return nil
	}
	return err
}

// reconcile decides what an interrupted job needs, from its durable phase.
//
// The rule that matters: a job whose measurement may already have run is never
// re-run, because a second run would publish a different result for the same
// request. A job that never started measuring is discarded; a job whose
// measurement finished keeps its branch and commit, so only the pull request
// and the cleanup are repeated (both idempotent).
func (c *Controller) reconcile(ctx context.Context, record lease.Record) error {
	switch record.Phase {
	case lease.PhaseDeletingClaim, lease.PhaseClaimDeleted, lease.PhaseRestoring, lease.PhaseRestored, lease.PhaseReleasing:
		// Cleanup was already decided; finish it with the outcome the record
		// carries instead of deciding again.
		return c.finish(ctx, record)
	case lease.PhaseExecuted, lease.PhaseOpeningPR, lease.PhasePROpen:
		// The measurement is done and identified by branch/commit, so only the
		// pull request is repeated. That is safe: the gateway returns the
		// existing PR for that head instead of opening a second one.
		c.logf("resuming job %s from phase %q", record.JobID, record.Phase)
		if record.PullRequest == 0 {
			if record.Branch == "" {
				c.logf("job %s has no branch in its record; finishing it as failed", record.JobID)
				record.Outcome = lease.OutcomeFailed
				return c.finish(ctx, record)
			}
			pr, err := c.Gateway.OpenPR(ctx, PullRequest{
				Head:  record.Branch,
				Base:  c.Config.DefaultBranch,
				Title: fmt.Sprintf("benchmark: %s", record.JobID),
				Body:  c.resumedPullRequestBody(record),
			})
			if err != nil {
				return fmt.Errorf("resume the pull request: %w", err)
			}
			record.PullRequest = pr
			record.Phase = lease.PhasePROpen
			if err := c.annotate(ctx, &record); err != nil {
				return err
			}
			if record.Issue > 0 {
				_ = c.Gateway.Comment(ctx, record.Issue, fmt.Sprintf("llmbench recovered job %s and opened #%d for review.", record.JobID, pr))
			}
		}
		return c.finish(ctx, record)
	case lease.PhaseExecuting:
		// Unknown whether the measurement completed. Never repeat it; the
		// request is finished as a failure and a human can re-open it.
		c.logf("job %s was interrupted while measuring; not repeating it", record.JobID)
		record.Outcome = lease.OutcomeFailed
		return c.finish(ctx, record)
	default:
		c.logf("discarding job %s stopped in phase %q before measuring", record.JobID, record.Phase)
		record.Outcome = lease.OutcomeFailed
		return c.finish(ctx, record)
	}
}

// run executes one request end to end. Every step is idempotent and the phase
// is written before the effect, so a crash anywhere is recoverable.
func (c *Controller) run(ctx context.Context, issue Issue) (bool, error) {
	request, err := c.parse(issue)
	if err != nil {
		// An invalid request never takes the GPU: it is answered on the Issue.
		c.logf("issue #%d: %v", issue.Number, err)
		_ = c.Gateway.Comment(ctx, issue.Number, "llmbench could not accept this request:\n\n```\n"+err.Error()+"\n```")
		_ = c.Gateway.Label(ctx, issue.Number, c.Config.Labels.Failed)
		return true, nil
	}
	holder := c.holderFor(request.JobID)
	record := lease.Record{
		JobID: request.JobID, Issue: issue.Number, JobSpecDigest: request.Digest, Phase: lease.PhaseAcquired,
	}

	// The lease comes first: it is the job mutex, so only the controller that
	// holds it may claim the Issue. A live lease held by the same job id means
	// another writer (or an earlier attempt) is already on it.
	acquired, err := c.Lease.Acquire(ctx, holder, record, false)
	if err != nil {
		return false, fmt.Errorf("acquire the gpu lease: %w", err)
	}
	if !acquired {
		c.logf("issue #%d: the gpu lease is held elsewhere; leaving it for the next poll", issue.Number)
		return false, nil
	}
	record.Holder = holder
	if err := c.claim(ctx, issue); err != nil {
		return true, c.abort(ctx, record, "could not claim the request", err)
	}

	jobCtx, cancelJob := context.WithCancel(ctx)
	defer cancelJob()
	// The renewal loop outlives this call: a cleanup that waits for a merge
	// keeps holding the GPU, so it must keep owning the lease. It is therefore
	// not tied to the job context (which ends with this call) but to the
	// lease: it stops when the lease is released, or cancels the job when the
	// lease is lost.
	c.keepLease(context.WithoutCancel(ctx), holder, cancelJob)

	if err := c.execute(jobCtx, request, &record); err != nil {
		return true, c.abort(ctx, record, "the job did not complete", err)
	}
	record.Outcome = lease.OutcomeSucceeded
	return true, c.finish(ctx, record)
}

// parse turns an Issue into a validated request. A rejected request is a fact
// about the request, not a failure of the controller.
func (c *Controller) parse(issue Issue) (Request, error) {
	spec, err := job.FromIssueBody(issue.Body)
	if err != nil {
		return Request{}, err
	}
	if spec.Kind == job.KindOptimize {
		// The controller can measure, but it cannot yet hand a sandbox to an
		// Agent. Accepting the request and running a single benchmark would
		// answer a different question than the one that was asked.
		return Request{}, errors.New("optimization requests are not supported by this controller build yet; open a benchmark request instead")
	}
	constraints := c.Config.Constraints
	reserved, ok := c.Config.ReservedArgs[spec.Runtime.Engine]
	if !ok {
		return Request{}, fmt.Errorf("engine %q has no reserved-argument list in the operator configuration", spec.Runtime.Engine)
	}
	constraints.ReservedArgs = reserved
	if err := spec.ValidateConstraints(constraints); err != nil {
		return Request{}, err
	}
	digest, err := spec.Digest()
	if err != nil {
		return Request{}, err
	}
	model, ok := c.model(spec.Model.ID)
	if !ok {
		return Request{}, fmt.Errorf("model %q is not configured by the operator", spec.Model.ID)
	}
	specYAML, err := yaml.Marshal(spec)
	if err != nil {
		return Request{}, fmt.Errorf("encode the accepted spec: %w", err)
	}
	return Request{
		Issue:    issue,
		JobID:    jobIDFor(issue.Number, c.now()),
		Kind:     spec.Kind,
		Spec:     spec,
		SpecYAML: specYAML,
		Digest:   digest,
		Model:    model,
	}, nil
}

// jobIDFor names a job after the day and the Issue, which is stable across
// retries of the same request (docs/mvp.md §3.3).
func jobIDFor(issue int, now time.Time) string {
	return fmt.Sprintf("%s-issue%d", now.UTC().Format("2006-01-02"), issue)
}

func (c *Controller) model(id string) (operator.MVPModel, bool) {
	for _, m := range c.Config.Models {
		if m.ID == id {
			return m, true
		}
	}
	return operator.MVPModel{}, false
}

// claim marks the Issue as taken and proves the write landed, so the GPU is
// never borrowed for a request that still looks free to everyone else.
func (c *Controller) claim(ctx context.Context, issue Issue) error {
	if err := c.Gateway.Label(ctx, issue.Number, c.Config.Labels.Claimed); err != nil {
		return err
	}
	claimed, err := c.Gateway.Claimed(ctx, issue.Number, c.Config.Labels)
	if err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("the claim label on issue #%d did not stick", issue.Number)
	}
	return nil
}

// execute pauses the workload, runs the measurement in a sandbox, and opens
// the pull request.
func (c *Controller) execute(ctx context.Context, request Request, record *lease.Record) error {
	if err := c.phase(ctx, record, lease.PhasePausing); err != nil {
		return err
	}
	if err := c.pause(ctx, request.JobID); err != nil {
		return err
	}
	if err := c.phase(ctx, record, lease.PhasePaused); err != nil {
		return err
	}
	if err := c.phase(ctx, record, lease.PhaseClaiming); err != nil {
		return err
	}
	if err := c.Sandbox.Ensure(ctx, request.JobID); err != nil {
		return fmt.Errorf("create the sandbox: %w", err)
	}
	if err := c.phase(ctx, record, lease.PhaseClaimReady); err != nil {
		return err
	}
	// Write-ahead: from here on a crash means the measurement may have run,
	// and recovery must not repeat it.
	if err := c.phase(ctx, record, lease.PhaseExecuting); err != nil {
		return err
	}
	env, err := c.sandboxEnv(ctx, request.Env)
	if err != nil {
		return err
	}
	specPath := "/tmp/llmbench-job-" + request.JobID + ".yaml"
	if err := c.Sandbox.Put(ctx, request.JobID, specPath, request.SpecYAML); err != nil {
		return fmt.Errorf("write the job spec into the sandbox: %w", err)
	}
	out, err := c.Sandbox.Exec(ctx, request.JobID, c.benchmarkArgv(request, specPath), env)
	if err != nil {
		return fmt.Errorf("measure in the sandbox: %w", err)
	}
	result, err := parseBenchmarkOutput(out)
	if err != nil {
		return err
	}
	if result.Branch == "" {
		return errors.New("the benchmark did not push a branch, so there is nothing to review")
	}
	request.Branch, request.Commit = result.Branch, result.Commit
	record.Branch, record.Commit = result.Branch, result.Commit
	// The outcome is durable with the phase that follows the measurement: a
	// crash after this point must not let recovery reinterpret a successful
	// run as a failed one.
	record.Phase = lease.PhaseExecuted
	record.Outcome = lease.OutcomeSucceeded
	if err := c.annotate(ctx, record); err != nil {
		return err
	}
	if err := c.phase(ctx, record, lease.PhaseOpeningPR); err != nil {
		return err
	}
	pr, err := c.Gateway.OpenPR(ctx, PullRequest{
		Head:  request.Branch,
		Base:  c.Config.DefaultBranch,
		Title: fmt.Sprintf("%s: %s (%s)", request.Spec.Kind, request.Spec.Model.ID, request.JobID),
		Body:  c.pullRequestBody(request, result),
	})
	if err != nil {
		return fmt.Errorf("open the pull request: %w", err)
	}
	record.PullRequest = pr
	record.Phase = lease.PhasePROpen
	if err := c.annotate(ctx, record); err != nil {
		return err
	}
	request.PullRequest = pr
	record.Issue = request.Issue.Number
	if err := c.Gateway.Comment(ctx, request.Issue.Number, fmt.Sprintf("llmbench measured %s and opened #%d for review.", request.JobID, pr)); err != nil {
		// The PR is the result; a failed link comment must not undo the run.
		c.logf("issue #%d: could not comment the PR link: %v", request.Issue.Number, err)
	}
	return nil
}

// sandboxEnv adds the short-lived git token to the environment of the sandbox
// command. The token is minted per job, passed in the environment and never
// written anywhere; §7 explains why the default branch protection is still
// required even though the token is short-lived.
func (c *Controller) sandboxEnv(ctx context.Context, extra map[string]string) (map[string]string, error) {
	env := map[string]string{}
	for k, v := range extra {
		env[k] = v
	}
	if c.Config.GitToken == nil {
		return env, nil
	}
	token, err := c.Config.GitToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("mint the sandbox git token: %w", err)
	}
	env[SandboxTokenEnv] = token
	return env, nil
}

// SandboxTokenEnv is the environment variable the benchmark CLI reads the
// short-lived installation token from.
const SandboxTokenEnv = "LLMBENCH_GIT_TOKEN"

// finish completes a job whose measurement is done (or may be): it deletes the
// sandbox, restores the workload, releases the lease and records the outcome.
//
// Restoration is unconditional and runs on a context that survives
// cancellation: the GPU must never be left paused, and the lease is released
// last so an interrupted job is still recognisable after a restart.
func (c *Controller) finish(ctx context.Context, record lease.Record) error {
	cleanup := context.WithoutCancel(ctx)
	holder := record.Holder
	if holder == "" {
		holder = c.holderFor(record.JobID)
	}

	// Take the lease before writing phases: after a restart the record names
	// an older holder, and the phases are ours to write only while we own it.
	// A live lease of another instance cannot be taken (fencing), so the job
	// waits for expiry instead of being cleaned up under a running measurement.
	current, err := c.Lease.Get(cleanup)
	if err != nil {
		return err
	}
	if current.JobID != record.JobID {
		return fmt.Errorf("the gpu lease no longer belongs to job %s", record.JobID)
	}
	if current.Holder != holder {
		if !current.ExpiresAt.IsZero() && c.now().Before(current.ExpiresAt) {
			return fmt.Errorf("%w: job %s is still held by %q", ErrNotConverged, record.JobID, current.Holder)
		}
		acquired, err := c.Lease.Acquire(cleanup, holder, record, true)
		if err != nil {
			return err
		}
		if !acquired {
			return fmt.Errorf("%w: another writer holds the gpu lease for job %s", ErrNotConverged, record.JobID)
		}
	}

	// The renewal keeps running while cleanup waits for a human merge: if the
	// lease expired here, another controller could take the GPU away from a job
	// that is still restoring.
	// A recovery pass may be the first thing that happens after a restart, so
	// ownership is re-checked (and extended) before any external effect.
	if current.Holder == holder {
		if err := c.Lease.Renew(cleanup, holder); err != nil {
			return fmt.Errorf("renew the gpu lease before cleanup: %w", err)
		}
	}

	// The outcome is durable before cleanup starts, so the labels mirror the
	// job's result rather than whichever cleanup step ran last.
	if record.Outcome == "" {
		record.Outcome = lease.OutcomeFailed
	}
	if err := c.annotate(cleanup, &record); err != nil {
		return err
	}

	// Nothing is released until both the sandbox is gone and the workload is
	// back: a not-converged restore keeps the lease, keeps the phase, and is
	// retried by the recovery loop. The lease is what stops the next job from
	// borrowing a GPU that is still paused.
	if err := c.ensureClaimDeleted(cleanup, &record); err != nil {
		return err
	}
	if err := c.ensureRestored(cleanup, &record); err != nil {
		return err
	}
	record.Phase = lease.PhaseReleasing
	if err := c.annotate(cleanup, &record); err != nil {
		return err
	}
	// The labels are part of completion, so they are established before the
	// lease goes away. If the terminal label cannot be written the lease is
	// kept: releasing it would make the Issue pending again (no state label)
	// and the same request would be measured a second time.
	if err := c.syncLabels(cleanup, record.Issue, record.Outcome); err != nil {
		return fmt.Errorf("%w: %w", ErrNotConverged, err)
	}
	if err := c.Lease.Release(cleanup, holder); err != nil {
		return fmt.Errorf("release the gpu lease: %w", err)
	}
	c.stopLeaseKeeper()
	return nil
}

// keepLease starts the renewal loop for a job we hold, replacing any previous
// one. The loop stops when the lease is released or when it is lost (which
// also cancels the job, so a fenced process stops measuring).
func (c *Controller) keepLease(ctx context.Context, holder string, onLost func()) {
	renewCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.keeper != nil {
		c.keeper()
	}
	c.keeper = cancel
	c.mu.Unlock()
	go c.renewLoop(renewCtx, holder, onLost)
}

// stopLeaseKeeper stops renewal after the lease has been released.
func (c *Controller) stopLeaseKeeper() {
	c.mu.Lock()
	if c.keeper != nil {
		c.keeper()
		c.keeper = nil
	}
	c.mu.Unlock()
}

// renewLoop keeps the lease alive until the context is cancelled.
func (c *Controller) renewLoop(ctx context.Context, holder string, onLost func()) {
	ticker := time.NewTicker(c.renewInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Lease.Renew(ctx, holder); err != nil {
				c.logf("renewing the gpu lease failed: %v", err)
				onLost()
				return
			}
		}
	}
}

// ensureClaimDeleted deletes the sandbox and records the phase only once the
// claim is really gone.
func (c *Controller) ensureClaimDeleted(ctx context.Context, record *lease.Record) error {
	record.Phase = lease.PhaseDeletingClaim
	if err := c.annotate(ctx, record); err != nil {
		return err
	}
	done, err := c.Sandbox.Delete(ctx, record.JobID)
	if err != nil {
		return fmt.Errorf("delete the sandbox: %w", err)
	}
	if !done {
		return fmt.Errorf("%w: the sandbox of job %s is still terminating", ErrNotConverged, record.JobID)
	}
	record.Phase = lease.PhaseClaimDeleted
	return c.annotate(ctx, record)
}

// ensureRestored restores the workload and records the phase only once the
// restore has converged (the PR merged, Argo synced, the workload back).
func (c *Controller) ensureRestored(ctx context.Context, record *lease.Record) error {
	record.Phase = lease.PhaseRestoring
	if err := c.annotate(ctx, record); err != nil {
		return err
	}
	if err := c.pauser().Restore(ctx, record.JobID); err != nil {
		return fmt.Errorf("restore the workload: %w", err)
	}
	record.Phase = lease.PhaseRestored
	return c.annotate(ctx, record)
}

// syncLabels mirrors the job outcome on the Issue: the claim goes away and
// exactly one terminal label remains, so the labels stay a truthful mirror of
// the durable phase instead of a second, drifting state.
func (c *Controller) syncLabels(ctx context.Context, issue int, outcome lease.Outcome) error {
	if issue == 0 || outcome == "" {
		return nil
	}
	terminal := c.Config.Labels.Done
	other := c.Config.Labels.Failed
	if outcome == lease.OutcomeFailed {
		terminal, other = c.Config.Labels.Failed, c.Config.Labels.Done
	}
	// The terminal label goes on first: while it exists the Issue is not
	// pending, so a crash in the middle of syncing cannot make an already
	// handled request look like a new one. Only then is the claim removed.
	if err := c.Gateway.Label(ctx, issue, terminal); err != nil {
		return fmt.Errorf("label the issue as %s: %w", terminal, err)
	}
	for _, label := range []string{other, c.Config.Labels.Claimed} {
		if label == "" {
			continue
		}
		if err := c.Gateway.Unlabel(ctx, issue, label); err != nil {
			return fmt.Errorf("remove the %s label: %w", label, err)
		}
	}
	return nil
}

// abort records a failure on the Issue and leaves the job restored.
func (c *Controller) abort(ctx context.Context, record lease.Record, what string, cause error) error {
	c.logf("issue #%d: %s: %v", record.Issue, what, cause)
	if record.Issue > 0 {
		if err := c.Gateway.Comment(ctx, record.Issue, "llmbench failed this request: "+what+".\n\n```\n"+cause.Error()+"\n```"); err != nil {
			c.logf("issue #%d: could not comment: %v", record.Issue, err)
		}
	}
	record.Outcome = lease.OutcomeFailed
	return c.finish(ctx, record)
}

// holderFor is the lease holder of a job. The identity is instance-unique
// (the Pod name in production), which is what makes another instance's live
// lease distinguishable from our own interrupted one.
func (c *Controller) holderFor(jobID string) string {
	identity := c.Config.HolderIdentity
	if identity == "" {
		identity = "llmbench-controller"
	}
	return identity + "/" + jobID
}

// pauser returns the pause/restore implementation; a nil Pauser means no
// shared workload to pause.
func (c *Controller) pauser() Pauser {
	if c.Pauser == nil {
		return noopPauser{}
	}
	return c.Pauser
}

type noopPauser struct{}

func (noopPauser) Pause(context.Context, string) error   { return nil }
func (noopPauser) Restore(context.Context, string) error { return nil }

// pause waits for the workload to reach the paused state. A not-converged
// pause is a human action (merge the PR), so it is retried until the timeout.
func (c *Controller) pause(ctx context.Context, jobID string) error {
	timeout := c.Config.PauseTimeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	deadline := c.now().Add(timeout)
	var last error
	for {
		err := c.pauser().Pause(ctx, jobID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotConverged) {
			return err
		}
		last = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !c.now().Before(deadline) {
			return fmt.Errorf("the pause did not converge within %s: %w", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.pollInterval()):
		}
	}
}

// renewInterval is a third of the lease duration unless it was set
// explicitly: a renewal interval longer than the lease would let the lease
// expire mid-measurement, and the first renewal would already be too late.
func (c *Controller) renewInterval() time.Duration {
	if c.Config.LeaseRenewInterval > 0 && (c.Config.LeaseDuration <= 0 || c.Config.LeaseRenewInterval < c.Config.LeaseDuration) {
		return c.Config.LeaseRenewInterval
	}
	if c.Config.LeaseDuration > 0 {
		return c.Config.LeaseDuration / 3
	}
	return 5 * time.Minute
}

func (c *Controller) pollInterval() time.Duration {
	if c.Config.PollInterval > 0 {
		return c.Config.PollInterval
	}
	return 15 * time.Second
}

// phase writes only the phase, keeping the rest of the record.
func (c *Controller) phase(ctx context.Context, record *lease.Record, phase lease.Phase) error {
	record.Phase = phase
	return c.annotate(ctx, record)
}

// annotate persists the record. The whole record is written because the
// annotations are the job's only durable state: a partial write would leave
// recovery guessing what the job had already done.
func (c *Controller) annotate(ctx context.Context, record *lease.Record) error {
	current := *record
	return c.Lease.Annotate(ctx, c.holderFor(record.JobID), func(r *lease.Record) {
		*r = current
	})
}

// benchmarkArgv builds the command that runs the measurement inside the
// sandbox. The CLI reads the job spec from stdin, so the controller does not
// have to write the request into the sandbox as a file.
func (c *Controller) benchmarkArgv(request Request, specPath string) []string {
	argv := append([]string(nil), c.Config.LLMBench...)
	argv = append(argv,
		"benchmark",
		"--job", specPath,
		"--model-path", request.Model.Path,
		"--job-id", request.JobID,
		// The sandbox commits and pushes its own result; the controller only
		// opens the pull request (docs/mvp.md §7).
		"--push",
		"--json",
	)
	if request.Model.Digest != "" {
		argv = append(argv, "--model-digest", request.Model.Digest)
	}
	return argv
}

// resumedPullRequestBody describes a job recovered after a restart, where the
// request details are no longer in memory but the durable record holds what
// matters: the identity of the measurement and where to find it.
func (c *Controller) resumedPullRequestBody(record lease.Record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Job: `%s` (resumed after a controller restart)\n\n", record.JobID)
	fmt.Fprintf(&b, "- job spec digest: `%s`\n", record.JobSpecDigest)
	fmt.Fprintf(&b, "- commit: `%s`\n", record.Commit)
	b.WriteString("\nThe measurement itself is in `experiments/`; the harness did not re-run it, so this PR carries the result of the original run.\n")
	return b.String()
}

// pullRequestBody summarizes the request and the measurement for the reviewer.
func (c *Controller) pullRequestBody(request Request, result benchmarkOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Job: `%s`\n\n", request.JobID)
	fmt.Fprintf(&b, "- kind: %s\n", request.Spec.Kind)
	fmt.Fprintf(&b, "- model: %s\n", request.Spec.Model.ID)
	fmt.Fprintf(&b, "- engine: %s\n", request.Spec.Runtime.Engine)
	fmt.Fprintf(&b, "- image: %s\n", request.Spec.Runtime.Image)
	fmt.Fprintf(&b, "- job spec digest: `%s`\n", request.Digest)
	if result.ResultDigest != "" {
		fmt.Fprintf(&b, "- result digest: `%s`\n", result.ResultDigest)
	}
	fmt.Fprintf(&b, "- measurement valid: %t\n", result.MeasurementValid)
	for _, reason := range result.InvalidReasons {
		fmt.Fprintf(&b, "  - invalid: %s\n", reason)
	}
	b.WriteString("\nThe measurement and the facts about it are in `experiments/`; a comparison against a baseline is `llmbench compare`. Whether this is an improvement is a review decision, not a harness verdict.\n")
	return b.String()
}
