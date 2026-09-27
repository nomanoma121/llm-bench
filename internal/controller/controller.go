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
	"time"

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
	// Exec runs the benchmark CLI inside it and returns its stdout.
	Exec(ctx context.Context, jobID string, argv []string, env map[string]string) ([]byte, error)
	// Delete removes it. It must be safe to call when nothing exists.
	Delete(ctx context.Context, jobID string) error
}

// ErrNotConverged is returned by a Pauser whose external state is not where it
// must be yet (an open pause PR, a workload that has not stopped). The loop
// retries; it is not a failure.
var ErrNotConverged = errors.New("controller: not converged")

// Request is one accepted job.
type Request struct {
	Issue  Issue
	JobID  string
	Kind   job.Kind
	Spec   job.Spec
	Digest string
	Model  operator.MVPModel
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
	Sandbox       operator.MVPSandbox
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
	LeaseRenewInterval time.Duration
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
	// order keeps the queue visible in the Issues themselves.
	if err := c.run(ctx, issues[0]); err != nil {
		c.logf("issue #%d: %v", issues[0].Number, err)
	}
	return true, nil
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
	if err := c.Gateway.EnsureLabels(ctx, c.Config.Labels); err != nil {
		return err
	}
	if err := c.Recover(ctx); err != nil {
		c.logf("initial recovery: %v", err)
	}
	recoveryTicker := time.NewTicker(recovery)
	defer recoveryTicker.Stop()
	pollTicker := time.NewTicker(poll)
	defer pollTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-recoveryTicker.C:
			if err := c.Recover(ctx); err != nil {
				c.logf("recovery: %v", err)
			}
		case <-pollTicker.C:
			handled, err := c.RunOnce(ctx)
			if err != nil {
				c.logf("poll: %v", err)
				continue
			}
			if handled && ctx.Err() == nil {
				// Serve the next request immediately instead of waiting a
				// whole poll interval.
				pollTicker.Reset(time.Millisecond)
			}
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
	if record.Holder == c.holderFor(record.JobID) {
		// Ours: either the loop is running it, or it is an interrupted job of
		// this controller (a single-replica Deployment has no other writer).
		c.logf("recovering job %s left in phase %q", record.JobID, record.Phase)
		return c.finish(ctx, record)
	}
	// Another job holds the lease. Nothing to do: its own controller (or the
	// expiry path) owns it.
	return nil
}

// run executes one request end to end. Every step is idempotent and the phase
// is written before the effect, so a crash anywhere is recoverable.
func (c *Controller) run(ctx context.Context, issue Issue) error {
	request, err := c.parse(issue)
	if err != nil {
		// An invalid request never takes the GPU: it is answered on the Issue.
		c.logf("issue #%d: %v", issue.Number, err)
		_ = c.Gateway.Comment(ctx, issue.Number, "llmbench could not accept this request:\n\n```\n"+err.Error()+"\n```")
		_ = c.Gateway.Label(ctx, issue.Number, c.Config.Labels.Failed)
		return nil
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
		return fmt.Errorf("acquire the gpu lease: %w", err)
	}
	if !acquired {
		c.logf("issue #%d: the gpu lease is held elsewhere; leaving it for the next poll", issue.Number)
		return nil
	}
	record.Holder = holder
	if err := c.claim(ctx, issue); err != nil {
		return c.abort(ctx, record, "could not claim the request", err)
	}

	stopRenew := c.startRenewal(ctx, holder)
	defer stopRenew()

	if err := c.execute(ctx, request, &record); err != nil {
		return c.abort(ctx, record, "the job did not complete", err)
	}
	return c.finish(ctx, record)
}

// parse turns an Issue into a validated request. A rejected request is a fact
// about the request, not a failure of the controller.
func (c *Controller) parse(issue Issue) (Request, error) {
	spec, err := job.FromIssueBody(issue.Body)
	if err != nil {
		return Request{}, err
	}
	if err := spec.ValidateConstraints(c.Config.Constraints); err != nil {
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
	return Request{
		Issue:  issue,
		JobID:  jobIDFor(issue.Number, c.now()),
		Kind:   spec.Kind,
		Spec:   spec,
		Digest: digest,
		Model:  model,
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
	out, err := c.Sandbox.Exec(ctx, request.JobID, c.benchmarkArgv(request), env)
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
	record.Phase = lease.PhaseExecuted
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
	var failures []error

	// Take the lease with the same holder before writing phases: after a
	// restart the record names the old holder, and the phases are ours to
	// write only while we own it.
	if record.Holder != c.holderFor(record.JobID) || !c.owns(ctx, record.JobID) {
		acquired, err := c.Lease.Acquire(cleanup, c.holderFor(record.JobID), record, true)
		if err != nil {
			return fmt.Errorf("take over the gpu lease for recovery: %w", err)
		}
		if !acquired {
			return fmt.Errorf("another writer holds the gpu lease for job %s; not recovering it", record.JobID)
		}
		record.Holder = c.holderFor(record.JobID)
	}

	record.Phase = lease.PhaseDeletingClaim
	if err := c.annotate(cleanup, &record); err != nil {
		failures = append(failures, err)
	}
	if err := c.Sandbox.Delete(cleanup, record.JobID); err != nil {
		failures = append(failures, fmt.Errorf("delete the sandbox: %w", err))
	}
	record.Phase = lease.PhaseClaimDeleted
	if err := c.annotate(cleanup, &record); err != nil {
		failures = append(failures, err)
	}
	record.Phase = lease.PhaseRestoring
	if err := c.annotate(cleanup, &record); err != nil {
		failures = append(failures, err)
	}
	if err := c.pauser().Restore(cleanup, record.JobID); err != nil {
		failures = append(failures, fmt.Errorf("restore the workload: %w", err))
	}
	record.Phase = lease.PhaseRestored
	if err := c.annotate(cleanup, &record); err != nil {
		failures = append(failures, err)
	}
	// The lease is released last: until then the job is unfinished and the
	// next controller must recover it rather than start something new.
	if err := c.Lease.Release(cleanup, c.holderFor(record.JobID)); err != nil {
		failures = append(failures, fmt.Errorf("release the gpu lease: %w", err))
		return errors.Join(failures...)
	}
	if len(failures) > 0 && record.Issue > 0 {
		_ = c.Gateway.Comment(ctx, record.Issue, "llmbench restored the GPU, but with errors:\n\n```\n"+errors.Join(failures...).Error()+"\n```")
	}
	return errors.Join(failures...)
}

// abort records a failure on the Issue and leaves the job restored.
func (c *Controller) abort(ctx context.Context, record lease.Record, what string, cause error) error {
	c.logf("issue #%d: %s: %v", record.Issue, what, cause)
	if record.Issue > 0 {
		if err := c.Gateway.Comment(ctx, record.Issue, "llmbench failed this request: "+what+".\n\n```\n"+cause.Error()+"\n```"); err != nil {
			c.logf("issue #%d: could not comment: %v", record.Issue, err)
		}
		if err := c.Gateway.Label(ctx, record.Issue, c.Config.Labels.Failed); err != nil {
			c.logf("issue #%d: could not label: %v", record.Issue, err)
		}
	}
	return c.finish(ctx, record)
}

// owns reports whether the lease is currently recorded as ours.
func (c *Controller) owns(ctx context.Context, jobID string) bool {
	record, err := c.Lease.Get(ctx)
	if err != nil {
		return false
	}
	return record.JobID == jobID && record.Holder == c.holderFor(jobID)
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

// startRenewal keeps the lease alive while a job runs.
func (c *Controller) startRenewal(ctx context.Context, holder string) func() {
	interval := c.Config.LeaseRenewInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Lease.Renew(ctx, holder); err != nil {
					c.logf("renewing the gpu lease: %v", err)
				}
			}
		}
	}()
	return func() { close(done) }
}

func (c *Controller) holderFor(jobID string) string {
	identity := c.Config.HolderIdentity
	if identity == "" {
		identity = "llmbench-controller"
	}
	return identity + "/" + jobID
}

// benchmarkArgv builds the command that runs the measurement inside the
// sandbox. The CLI reads the job spec from stdin, so the controller does not
// have to write the request into the sandbox as a file.
func (c *Controller) benchmarkArgv(request Request) []string {
	argv := append([]string(nil), c.Config.LLMBench...)
	argv = append(argv,
		"benchmark",
		"--job", "-",
		"--model-path", request.Model.Path,
		"--job-id", request.JobID,
		"--json",
	)
	if request.Model.Digest != "" {
		argv = append(argv, "--model-digest", request.Model.Digest)
	}
	return argv
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
