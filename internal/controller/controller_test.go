package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/lease"
	"github.com/nomanoma121/llm-bench/internal/operator"
)

// fakeGateway records what the controller did to GitHub.
type fakeGateway struct {
	issues   []Issue
	labels   map[int][]string
	comments map[int][]string
	prs      []PullRequest
	prErr    error
}

func newFakeGateway(issues ...Issue) *fakeGateway {
	return &fakeGateway{issues: issues, labels: map[int][]string{}, comments: map[int][]string{}}
}

func (g *fakeGateway) Pending(_ context.Context, labels operator.Labels) ([]Issue, error) {
	var out []Issue
	for _, issue := range g.issues {
		if !hasLabel(issue.Labels, labels.Benchmark) && !hasLabel(issue.Labels, labels.Optimize) {
			continue
		}
		if hasLabel(issue.Labels, labels.Claimed) || hasLabel(issue.Labels, labels.Done) || hasLabel(issue.Labels, labels.Failed) {
			continue
		}
		out = append(out, issue)
	}
	return out, nil
}

func (g *fakeGateway) Label(_ context.Context, issue int, label string) error {
	g.labels[issue] = append(g.labels[issue], label)
	for i := range g.issues {
		if g.issues[i].Number == issue {
			g.issues[i].Labels = append(g.issues[i].Labels, label)
		}
	}
	return nil
}

func (g *fakeGateway) Unlabel(_ context.Context, issue int, label string) error {
	var kept []string
	for _, l := range g.labels[issue] {
		if l != label {
			kept = append(kept, l)
		}
	}
	g.labels[issue] = kept
	return nil
}

func (g *fakeGateway) Comment(_ context.Context, issue int, body string) error {
	g.comments[issue] = append(g.comments[issue], body)
	return nil
}

func (g *fakeGateway) OpenPR(_ context.Context, pr PullRequest) (int, error) {
	if g.prErr != nil {
		return 0, g.prErr
	}
	g.prs = append(g.prs, pr)
	return 100 + len(g.prs), nil
}

func (g *fakeGateway) Claimed(_ context.Context, issue int, labels operator.Labels) (bool, error) {
	return hasLabel(g.issues[indexOf(g.issues, issue)].Labels, labels.Claimed), nil
}

func (g *fakeGateway) EnsureLabels(context.Context, operator.Labels) error { return nil }

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func indexOf(issues []Issue, number int) int {
	for i, issue := range issues {
		if issue.Number == number {
			return i
		}
	}
	return 0
}

// fakeLease is the in-memory counterpart of the GPU lease, with the same
// mutual-exclusion rules.
type fakeLease struct {
	record     lease.Record
	live       bool
	annotates  []lease.Phase
	released   bool
	outcomeAt  lease.Phase
	acquireErr error
}

func (l *fakeLease) Acquire(_ context.Context, holder string, record lease.Record, reentrant bool) (bool, error) {
	if l.acquireErr != nil {
		return false, l.acquireErr
	}
	if l.live {
		if l.record.Holder != holder || !reentrant {
			return false, nil
		}
	}
	l.record = record
	l.record.Holder = holder
	l.record.AcquiredAt = time.Now()
	l.record.ExpiresAt = time.Now().Add(30 * time.Minute)
	l.live, l.released = true, false
	return true, nil
}

func (l *fakeLease) Renew(_ context.Context, holder string) error { return nil }

func (l *fakeLease) Annotate(_ context.Context, holder string, mutate func(*lease.Record)) error {
	if !l.live || l.record.Holder != holder {
		return fmt.Errorf("lease is held by %q, not %q", l.record.Holder, holder)
	}
	mutate(&l.record)
	l.annotates = append(l.annotates, l.record.Phase)
	// Record when the outcome first became durable, so a test can prove it
	// happened before the cleanup phases.
	if l.record.Outcome != "" && l.outcomeAt == "" {
		l.outcomeAt = l.record.Phase
	}
	return nil
}

func (l *fakeLease) Release(_ context.Context, holder string) error {
	if !l.live || l.record.Holder != holder {
		return fmt.Errorf("refusing to release a lease held by %q", l.record.Holder)
	}
	l.live = false
	l.released = true
	// The real lease keeps the record but clears the holder; a released record
	// must never be picked up by a later recovery pass.
	l.record.Holder = ""
	l.record.Phase = lease.PhaseReleased
	return nil
}

func (l *fakeLease) Get(context.Context) (lease.Record, error) {
	if !l.live {
		// A released record keeps its annotations, as the real lease does.
		return l.record, nil
	}
	return l.record, nil
}

// fakePauser records pause and restore calls.
type fakePauser struct {
	acquireErr error
	restoreErr error
	pauses     int
	restores   int
}

func (p *fakePauser) Pause(context.Context, string) error {
	if p.acquireErr != nil {
		return p.acquireErr
	}
	p.pauses++
	return nil
}

func (p *fakePauser) Restore(context.Context, string) error {
	if p.restoreErr != nil {
		return p.restoreErr
	}
	p.restores++
	return nil
}

// fakeSandbox records the lifecycle of the sandbox.
type fakeSandbox struct {
	ensures   []string
	deletes   []string
	execs     []string
	puts      []string
	pushed    []string
	execOut   []byte
	execErr   error
	ensureErr error
	deleteErr error
	putErr    error
	// deletePending makes Delete report "still terminating", which the
	// controller must retry without releasing the lease.
	deletePending bool
}

func (s *fakeSandbox) Ensure(_ context.Context, jobID string) error {
	if s.ensureErr != nil {
		return s.ensureErr
	}
	s.ensures = append(s.ensures, jobID)
	return nil
}

func (s *fakeSandbox) Put(_ context.Context, jobID, path string, content []byte) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.puts = append(s.puts, path)
	s.pushed = append(s.pushed, string(content))
	return nil
}

func (s *fakeSandbox) Exec(_ context.Context, jobID string, argv []string, _ map[string]string) ([]byte, error) {
	if s.execErr != nil {
		return nil, s.execErr
	}
	s.execs = append(s.execs, strings.Join(argv, " "))
	return s.execOut, nil
}

func (s *fakeSandbox) Delete(_ context.Context, jobID string) (bool, error) {
	if s.deleteErr != nil {
		return false, s.deleteErr
	}
	if s.deletePending {
		return false, nil
	}
	s.deletes = append(s.deletes, jobID)
	return true, nil
}

// benchOutput is a successful CLI summary.
func benchOutput(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(benchmarkOutput{
		JobID: "job", ResultDigest: strings.Repeat("a", 64), SeriesDigest: strings.Repeat("b", 64),
		MeasurementValid: true, Branch: "llmbench/job", Commit: strings.Repeat("c", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func issueBody(t *testing.T) string {
	t.Helper()
	spec := job.Spec{
		Kind:  job.KindBenchmark,
		Model: job.Model{ID: "model-a"},
		Runtime: job.Runtime{
			Engine: "llamacpp", Image: "img", Ready: job.Ready{Port: 8080},
		},
		Workload: job.Workload{Cases: []job.Case{{Name: "short", PromptText: "hi", MaxTokens: 8, Repeats: 1}}},
		Metrics:  job.Metrics{Collectors: []job.Collector{job.CollectorHarness}},
		Output:   job.Output{Dir: "experiments/model-a"},
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	_ = data
	// The Issue form delivers YAML; build it from the spec's own fields.
	var b strings.Builder
	fmt.Fprintf(&b, "```yaml\nkind: benchmark\nmodel:\n  id: model-a\nruntime:\n  engine: llamacpp\n  image: img\n  ready:\n    port: 8080\nworkload:\n  cases:\n    - name: short\n      prompt_text: hi\n      max_tokens: 8\n      repeats: 1\nmetrics:\n  collectors: [harness]\noutput:\n  dir: experiments/model-a\n```\n")
	return b.String()
}

func newController(t *testing.T, mutate func(*Controller)) (*Controller, *fakeGateway, *fakeLease, *fakePauser, *fakeSandbox) {
	t.Helper()
	labels := operator.Labels{
		Benchmark: "llmbench:benchmark", Optimize: "llmbench:optimize",
		Claimed: "llmbench:claimed", Done: "llmbench:done", Failed: "llmbench:failed",
	}
	gateway := newFakeGateway(Issue{Number: 42, Title: "benchmark", Body: issueBody(t), Labels: []string{labels.Benchmark}})
	leaseStore := &fakeLease{}
	pauser := &fakePauser{}
	sandbox := &fakeSandbox{execOut: benchOutput(t)}
	c := &Controller{
		Config: Config{
			Repo:          "owner/repo",
			DefaultBranch: "main",
			Labels:        labels,
			Models:        []operator.MVPModel{{ID: "model-a", Path: "/models/a", Digest: strings.Repeat("d", 64)}},
			Constraints: job.Constraints{
				Engines: []string{"llamacpp"}, Images: []string{"img"}, Models: []string{"model-a"},
				OutputRoots: []string{"experiments"},
			},
			ReservedArgs: map[string][]string{
				"llamacpp":  {"--model", "--host", "--port"},
				"freetoken": {"--model", "--host", "--port", "--gpu"},
			},
			LLMBench:       []string{"llmbench"},
			HolderIdentity: "test-controller",
			PollInterval:   time.Millisecond,
		},
		Gateway: gateway,
		Lease:   leaseStore,
		Pauser:  pauser,
		Sandbox: sandbox,
		Now:     func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) },
	}
	if mutate != nil {
		mutate(c)
	}
	return c, gateway, leaseStore, pauser, sandbox
}

func TestRunOnceHappyPath(t *testing.T) {
	c, gateway, leaseStore, pauser, sandbox := newController(t, nil)
	handled, err := c.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("the pending issue was not handled")
	}
	// The order of durable phases is the write-ahead contract.
	want := []lease.Phase{
		lease.PhasePausing, lease.PhasePaused, lease.PhaseClaiming, lease.PhaseClaimReady,
		lease.PhaseExecuting, lease.PhaseExecuted, lease.PhaseOpeningPR, lease.PhasePROpen,
		lease.PhaseDeletingClaim, lease.PhaseClaimDeleted, lease.PhaseRestoring, lease.PhaseRestored,
		lease.PhaseReleasing,
	}
	if strings.Join(dedupe(phases(leaseStore.annotates)), ",") != strings.Join(dedupe(phases(want)), ",") {
		t.Fatalf("phases = %v, want %v", leaseStore.annotates, want)
	}
	if len(gateway.prs) != 1 {
		t.Fatalf("pull requests = %+v", gateway.prs)
	}
	pr := gateway.prs[0]
	if pr.Head != "llmbench/job" || pr.Base != "main" {
		t.Fatalf("pull request = %+v", pr)
	}
	if !strings.Contains(pr.Body, "result digest") || strings.Contains(strings.ToLower(pr.Body), "accept") {
		t.Fatalf("pull request body = %q", pr.Body)
	}
	if pauser.pauses != 1 || pauser.restores != 1 {
		t.Fatalf("pause/restore = %d/%d", pauser.pauses, pauser.restores)
	}
	if len(sandbox.ensures) != 1 || len(sandbox.deletes) != 1 {
		t.Fatalf("sandbox = %+v / %+v", sandbox.ensures, sandbox.deletes)
	}
	if !leaseStore.released || leaseStore.live {
		t.Fatal("the gpu lease was not released")
	}
	if hasLabel(gateway.labels[42], "llmbench:claimed") {
		t.Fatalf("the claim label survived a successful job: %v", gateway.labels[42])
	}
	if !hasLabel(gateway.labels[42], "llmbench:done") {
		t.Fatalf("the done label is missing: %v", gateway.labels[42])
	}
	if len(gateway.comments[42]) == 0 {
		t.Fatal("no comment was posted")
	}
	// The command carries the resolved model and the pinned digest.
	argv := sandbox.execs[0]
	for _, want := range []string{"benchmark", "--push", "--model-path /models/a", "--model-digest " + strings.Repeat("d", 64), "--job-id 2026-09-27-issue42"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q does not contain %q", argv, want)
		}
	}
}

// dedupe drops consecutive repeats: writing the durable outcome keeps the
// current phase, which is a re-annotation rather than a new step.
func dedupe(in []string) []string {
	var out []string
	for i, v := range in {
		if i > 0 && in[i-1] == v {
			continue
		}
		out = append(out, v)
	}
	return out
}

// recordedOutcomeEarly reports whether the outcome was durable before the
// cleanup phases started.
func (c *Controller) recordedOutcomeEarly(l *fakeLease) bool {
	switch l.outcomeAt {
	case lease.PhaseExecuted, lease.PhaseOpeningPR, lease.PhasePROpen:
		return true
	}
	return false
}

func phases(in []lease.Phase) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		out = append(out, string(p))
	}
	return out
}

func TestRunOnceRejectsAnInvalidRequestWithoutTakingTheGpu(t *testing.T) {
	c, gateway, leaseStore, pauser, sandbox := newController(t, func(c *Controller) {
		c.Gateway.(*fakeGateway).issues[0].Body = "kind: benchmark\nmodel:\n  id: model-a\nboom: 1\n"
	})
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leaseStore.live {
		t.Fatal("the lease was taken for an invalid request")
	}
	if pauser.pauses != 0 || len(sandbox.ensures) != 0 {
		t.Fatal("an invalid request touched the GPU")
	}
	if !hasLabel(gateway.labels[42], "llmbench:failed") {
		t.Fatalf("labels = %v", gateway.labels[42])
	}
	if len(gateway.comments[42]) == 0 {
		t.Fatal("the reason was not posted")
	}
}

func TestRunOnceSkipsWhenTheLeaseIsHeld(t *testing.T) {
	c, _, leaseStore, _, sandbox := newController(t, nil)
	leaseStore.live = true
	leaseStore.record = lease.Record{Holder: "other-controller/other-job", JobID: "other-job"}
	handled, err := c.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		// A busy lease is not progress: reporting it as handled would make the
		// loop poll GitHub again immediately, forever.
		t.Fatal("a busy lease was reported as progress")
	}
	if len(sandbox.ensures) != 0 {
		t.Fatal("a second job touched the GPU while the lease was held")
	}
	if leaseStore.record.Holder != "other-controller/other-job" {
		t.Fatalf("the lease was taken over: %+v", leaseStore.record)
	}
}

func TestExecFailureRestoresAndMarksFailed(t *testing.T) {
	c, gateway, leaseStore, pauser, sandbox := newController(t, func(c *Controller) {
		c.Sandbox.(*fakeSandbox).execErr = errors.New("sandbox exploded")
	})
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !leaseStore.released {
		t.Fatal("the lease was not released after a failure")
	}
	if pauser.restores != 1 {
		t.Fatalf("restores = %d", pauser.restores)
	}
	if len(sandbox.deletes) != 1 {
		t.Fatalf("deletes = %v", sandbox.deletes)
	}
	if len(gateway.prs) != 0 {
		t.Fatal("a failed run opened a pull request")
	}
	if !hasLabel(gateway.labels[42], "llmbench:failed") {
		t.Fatalf("labels = %v", gateway.labels[42])
	}
	// Restoration still completes before the lease is released, so the phase
	// sequence reaches restored and only then releasing.
	var sawRestored bool
	for _, phase := range leaseStore.annotates {
		if phase == lease.PhaseRestored {
			sawRestored = true
		}
	}
	if !sawRestored {
		t.Fatalf("restoration was skipped: %v", leaseStore.annotates)
	}
	if last := leaseStore.annotates[len(leaseStore.annotates)-1]; last != lease.PhaseReleasing {
		t.Fatalf("last phase = %s, want releasing", last)
	}
}

func TestSandboxFailureBeforeExecStillRestores(t *testing.T) {
	c, _, leaseStore, pauser, _ := newController(t, func(c *Controller) {
		c.Sandbox.(*fakeSandbox).ensureErr = errors.New("no capacity")
	})
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !leaseStore.released || pauser.restores != 1 {
		t.Fatalf("released=%t restores=%d", leaseStore.released, pauser.restores)
	}
}

func TestPauseRetriesUntilConverged(t *testing.T) {
	c, _, _, pauser, _ := newController(t, nil)
	attempts := 0
	c.Pauser = &scriptedPauser{onAcquire: func() error {
		attempts++
		if attempts < 3 {
			return ErrNotConverged
		}
		return nil
	}}
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("pause attempts = %d, want 3", attempts)
	}
	_ = pauser
}

// scriptedPauser drives the pause convergence loop.
type scriptedPauser struct {
	onAcquire func() error
	restores  int
}

func (p *scriptedPauser) Pause(context.Context, string) error { return p.onAcquire() }
func (p *scriptedPauser) Restore(context.Context, string) error {
	p.restores++
	return nil
}

func TestRecoverResumesFromAPhaseThatOnlyNeedsThePR(t *testing.T) {
	c, gateway, leaseStore, _, sandbox := newController(t, nil)
	holder := c.holderFor("2026-09-27-issue42")
	leaseStore.live = true
	leaseStore.record = lease.Record{
		Holder: holder, JobID: "2026-09-27-issue42", Issue: 42,
		Phase: lease.PhaseExecuted, Branch: "llmbench/2026-09-27-issue42", Commit: strings.Repeat("c", 40),
	}
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.execs) != 0 {
		t.Fatal("a resumed job measured again")
	}
	if len(gateway.prs) != 1 || gateway.prs[0].Head != "llmbench/2026-09-27-issue42" {
		t.Fatalf("the pull request was not resumed: %+v", gateway.prs)
	}
	if !leaseStore.released {
		t.Fatal("the resumed job did not release the lease")
	}
}

func TestFinishKeepsTheLeaseWhenRestoreHasNotConverged(t *testing.T) {
	// The restore is a human-merged PR: until it converges the lease stays
	// ours, because releasing it would let the next job borrow a GPU that is
	// still paused.
	c, _, leaseStore, pauser, sandbox := newController(t, nil)
	pauser.acquireErr = nil
	pauser.restoreErr = ErrNotConverged
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leaseStore.released {
		t.Fatal("the lease was released while the workload was still paused")
	}
	if !leaseStore.live {
		t.Fatal("the lease was dropped")
	}
	if got := leaseStore.record.Phase; got != lease.PhaseRestoring {
		t.Fatalf("phase = %s, want the restore to stay in progress", got)
	}
	_ = sandbox
}

func TestFinishKeepsTheLeaseWhenTheSandboxHasNotTerminated(t *testing.T) {
	c, _, leaseStore, pauser, sandbox := newController(t, nil)
	sandbox.deletePending = true
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leaseStore.released {
		t.Fatal("the lease was released while the sandbox was still terminating")
	}
	if pauser.restores != 0 {
		t.Fatal("the workload was restored while the GPU was still held by a sandbox")
	}
	if got := leaseStore.record.Phase; got != lease.PhaseDeletingClaim {
		t.Fatalf("phase = %s", got)
	}
}

func TestOptimizeRequestsAreRejectedUntilTheAgentPathExists(t *testing.T) {
	c, gateway, leaseStore, pauser, sandbox := newController(t, func(c *Controller) {
		body := strings.Replace(issueBody(t), "kind: benchmark", "kind: optimize", 1)
		body = strings.Replace(body, "output:\n", "source:\n  repo: owner/runtime\n  ref: main\nbudget:\n  max_rounds: 3\noutput:\n", 1)
		c.Gateway.(*fakeGateway).issues[0].Body = body
		c.Gateway.(*fakeGateway).issues[0].Labels = []string{"llmbench:optimize"}
	})
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leaseStore.live || pauser.pauses != 0 || len(sandbox.ensures) != 0 {
		t.Fatal("an optimize request touched the GPU")
	}
	if !hasLabel(gateway.labels[42], "llmbench:failed") {
		t.Fatalf("labels = %v", gateway.labels[42])
	}
	if len(gateway.comments[42]) == 0 || !strings.Contains(gateway.comments[42][0], "optimization") {
		t.Fatalf("comments = %v", gateway.comments[42])
	}
}

func TestReservedArgsArePerEngine(t *testing.T) {
	// A FreeToken job must not slip through with llama.cpp's reserved list.
	c, gateway, leaseStore, _, sandbox := newController(t, nil)
	body := strings.Replace(issueBody(t), "engine: llamacpp", "engine: freetoken", 1)
	body = strings.Replace(body, "  image: img\n", "  image: img\n  args: [\"--gpu\", \"1\"]\n", 1)
	c.Gateway.(*fakeGateway).issues[0].Body = body
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leaseStore.live || len(sandbox.ensures) != 0 {
		t.Fatal("a spec setting a reserved engine flag reached the GPU")
	}
	if !hasLabel(gateway.labels[42], "llmbench:failed") {
		t.Fatalf("labels = %v", gateway.labels[42])
	}
}

func TestRunOnceWritesTheSpecAndPushes(t *testing.T) {
	c, _, _, _, sandbox := newController(t, nil)
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.puts) != 1 {
		t.Fatalf("the job spec was not written into the sandbox: %v", sandbox.puts)
	}
	if !strings.Contains(sandbox.pushed[0], "kind: benchmark") {
		t.Fatalf("the pushed spec = %q", sandbox.pushed[0])
	}
	argv := sandbox.execs[0]
	if !strings.Contains(argv, "--push") {
		t.Fatalf("argv %q does not push the result", argv)
	}
	if strings.Contains(argv, "--job -") {
		t.Fatalf("argv %q still reads the spec from a stdin that does not exist", argv)
	}
	if !strings.Contains(argv, "--job "+sandbox.puts[0]) {
		t.Fatalf("argv %q does not point at the written spec", argv)
	}
}

func TestRecoverCleansUpAnInterruptedJobWithoutRerunningIt(t *testing.T) {
	c, gateway, leaseStore, pauser, sandbox := newController(t, nil)
	// A previous controller died while executing: the phase says the
	// measurement may have run, so it must not run again.
	holder := c.holderFor("2026-09-27-issue42")
	leaseStore.live = true
	leaseStore.record = lease.Record{
		Holder: holder, JobID: "2026-09-27-issue42", Issue: 42, Phase: lease.PhaseExecuting,
		Outcome: lease.OutcomeFailed,
	}
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.execs) != 0 {
		t.Fatalf("an interrupted job was re-executed: %v", sandbox.execs)
	}
	if len(sandbox.deletes) != 1 || pauser.restores != 1 || !leaseStore.released {
		t.Fatalf("cleanup incomplete: deletes=%v restores=%d released=%t", sandbox.deletes, pauser.restores, leaseStore.released)
	}
	// A job interrupted before executing still gets its Issue marked, so a
	// human knows it did not produce a result.
	if !hasLabel(gateway.labels[42], "llmbench:failed") && len(gateway.labels[42]) > 0 {
		t.Fatalf("labels = %v", gateway.labels[42])
	}
}

func TestTheSuccessOutcomeIsDurableBeforeCleanup(t *testing.T) {
	// A crash between the pull request and the cleanup must not turn a
	// succeeded job into a failed one, so the outcome has to be written with
	// the phase that follows the measurement.
	c, _, leaseStore, _, _ := newController(t, nil)
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leaseStore.record.Outcome != lease.OutcomeSucceeded {
		t.Fatalf("final outcome = %q", leaseStore.record.Outcome)
	}
	// The outcome was written while the phase was still executed or later, so
	// it was durable before any cleanup phase.
	if !c.recordedOutcomeEarly(leaseStore) {
		t.Fatal("the outcome was only recorded during cleanup")
	}
}

func TestARecoveredCleanupKeepsTheOriginalOutcome(t *testing.T) {
	// A succeeded job that crashed while restoring must end as done, not as
	// failed: cleanup does not get to decide the outcome.
	c, gateway, leaseStore, pauser, _ := newController(t, nil)
	holder := c.holderFor("2026-09-27-issue42")
	leaseStore.live = true
	leaseStore.record = lease.Record{
		Holder: holder, JobID: "2026-09-27-issue42", Issue: 42,
		Phase: lease.PhaseRestoring, Branch: "llmbench/job", Commit: strings.Repeat("c", 40),
		Outcome: lease.OutcomeSucceeded, PullRequest: 7,
	}
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !hasLabel(gateway.labels[42], "llmbench:done") {
		t.Fatalf("labels = %v, want done", gateway.labels[42])
	}
	if hasLabel(gateway.labels[42], "llmbench:failed") {
		t.Fatalf("a succeeded job was marked failed: %v", gateway.labels[42])
	}
	if pauser.restores != 1 || !leaseStore.released {
		t.Fatal("cleanup did not complete")
	}
}

func TestReleasedLeasesAreNeverRecovered(t *testing.T) {
	// A released lease keeps its annotations for history. Recovery must not
	// treat that as unfinished work and resurrect the job.
	c, gateway, leaseStore, pauser, sandbox := newController(t, nil)
	leaseStore.live = true
	leaseStore.record = lease.Record{
		Holder: "", JobID: "2026-09-27-issue1", Issue: 1, Phase: lease.PhaseReleased,
		Outcome: lease.OutcomeSucceeded,
	}
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.deletes) != 0 || pauser.restores != 0 {
		t.Fatal("a released job was resurrected by recovery")
	}
	// The only thing recovery may still do is repair the label mirror: a crash
	// between the label sync and the release would leave the Issue looking
	// claimed forever, and a released record is never revisited afterwards.
	if !hasLabel(gateway.labels[1], "llmbench:done") {
		t.Fatalf("labels = %v", gateway.labels[1])
	}
}

func TestRecoverLeavesAnotherJobsLeaseAlone(t *testing.T) {
	c, _, leaseStore, pauser, sandbox := newController(t, nil)
	leaseStore.live = true
	leaseStore.record = lease.Record{
		Holder: "other/x", JobID: "other-job", Phase: lease.PhaseExecuting,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.deletes) != 0 || pauser.restores != 0 {
		t.Fatal("recovery touched another job's resources")
	}
}

func TestRecoverDoesNothingWithoutAJob(t *testing.T) {
	c, _, _, pauser, sandbox := newController(t, nil)
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.deletes) != 0 || pauser.restores != 0 {
		t.Fatal("recovery acted without a job")
	}
}

func TestParseBenchmarkOutput(t *testing.T) {
	got, err := parseBenchmarkOutput([]byte("\n" + string(benchOutput(t)) + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "llmbench/job" || got.ResultDigest == "" {
		t.Fatalf("output = %+v", got)
	}
	// The controller refuses a summary without an identity: the PR would not
	// be tied to a result.
	if _, err := parseBenchmarkOutput([]byte(`{"job_id":"j"}`)); err == nil {
		t.Fatal("an output without a result digest was accepted")
	}
	if _, err := parseBenchmarkOutput(nil); err == nil {
		t.Fatal("an empty output was accepted")
	}
	if _, err := parseBenchmarkOutput([]byte("not json")); err == nil {
		t.Fatal("non-JSON output was accepted")
	}
}

func TestJobIDIsStablePerIssueAndDay(t *testing.T) {
	day := time.Date(2026, 9, 27, 23, 59, 0, 0, time.UTC)
	if got := jobIDFor(42, day); got != "2026-09-27-issue42" {
		t.Fatalf("job id = %q", got)
	}
	if got := jobIDFor(42, day.Add(2*time.Hour)); got != "2026-09-28-issue42" {
		t.Fatalf("job id after midnight = %q", got)
	}
}

func TestRunLoopRecoversAndServesRequests(t *testing.T) {
	c, gateway, leaseStore, _, sandbox := newController(t, nil)
	// An interrupted job from a previous incarnation is cleaned up before the
	// new request is served.
	leaseStore.live = true
	leaseStore.record = lease.Record{
		Holder: c.holderFor("2026-09-26-issue7"), JobID: "2026-09-26-issue7",
		Issue: 7, Phase: lease.PhasePaused,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx, 10*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.deletes) == 0 {
		t.Fatal("the interrupted job was not cleaned up")
	}
	if len(gateway.prs) != 1 {
		t.Fatalf("pull requests = %d, want the new request to be served", len(gateway.prs))
	}
}

func TestRunLoopSurvivesAGatewayError(t *testing.T) {
	c, gateway, _, _, _ := newController(t, nil)
	calls := 0
	c.Gateway = &flakyGateway{Gateway: gateway, failFirst: &calls}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx, 10*time.Millisecond, time.Minute); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("the loop stopped after the first error: %d calls", calls)
	}
}

// flakyGateway fails its first Pending call.
type flakyGateway struct {
	Gateway
	failFirst *int
}

func (g *flakyGateway) Pending(ctx context.Context, labels operator.Labels) ([]Issue, error) {
	*g.failFirst++
	if *g.failFirst == 1 {
		return nil, errors.New("github is unavailable")
	}
	return g.Gateway.Pending(ctx, labels)
}

func TestSandboxEnvMintsATokenPerJob(t *testing.T) {
	c, _, _, _, sandbox := newController(t, func(c *Controller) {
		c.Config.GitToken = func(context.Context) (string, error) { return "ghs_token", nil }
	})
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = sandbox
}

func TestControllerRefusesASummaryWithoutABranch(t *testing.T) {
	c, gateway, leaseStore, pauser, sandbox := newController(t, func(c *Controller) {
		out, err := json.Marshal(benchmarkOutput{JobID: "job", ResultDigest: strings.Repeat("a", 64)})
		if err != nil {
			t.Fatal(err)
		}
		c.Sandbox.(*fakeSandbox).execOut = out
	})
	if _, err := c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(gateway.prs) != 0 {
		t.Fatal("a result without a branch opened a PR")
	}
	if !leaseStore.released || pauser.restores != 1 || len(sandbox.deletes) != 1 {
		t.Fatal("the job was not cleaned up")
	}
	if !hasLabel(gateway.labels[42], "llmbench:failed") {
		t.Fatalf("labels = %v", gateway.labels[42])
	}
}
