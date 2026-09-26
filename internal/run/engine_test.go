package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/nomanoma121/llm-bench/internal/operator"
)

// ---------- fakes ----------

type fakeStore struct {
	mu        sync.Mutex
	runs      map[string]Run
	conflicts map[string]int // remaining forced conflicts per run id
	counter   int
}

func newFakeStore() *fakeStore {
	return &fakeStore{runs: map[string]Run{}, conflicts: map[string]int{}}
}

func (s *fakeStore) SaveRun(_ context.Context, r *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.conflicts[r.ID]; n > 0 {
		s.conflicts[r.ID] = n - 1
		return ErrVersionConflict
	}
	if stored, ok := s.runs[r.ID]; ok && stored.StoreVersion != r.StoreVersion {
		return ErrVersionConflict
	}
	s.counter++
	r.StoreVersion = fmt.Sprintf("v%d", s.counter)
	s.runs[r.ID] = *r
	return nil
}

func (s *fakeStore) LoadRun(_ context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return r, nil
}

func (s *fakeStore) ListUnfinished(_ context.Context) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Run
	for _, r := range s.runs {
		if !r.Phase.Terminal() {
			out = append(out, r)
		}
	}
	return out, nil
}

type fakeLeases struct {
	mu          sync.Mutex
	owners      map[string]string // target -> runID
	acquireErrs map[string]error  // per-run injected error (one shot)
	releaseErrs map[string]error  // per-run injected error (one shot)
}

func newFakeLeases() *fakeLeases {
	return &fakeLeases{
		owners:      map[string]string{},
		acquireErrs: map[string]error{},
		releaseErrs: map[string]error{},
	}
}

func (l *fakeLeases) AcquireTargetLease(_ context.Context, target, runID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.acquireErrs[runID]; err != nil {
		delete(l.acquireErrs, runID)
		return err
	}
	owner, ok := l.owners[target]
	if !ok {
		l.owners[target] = runID
		return nil
	}
	if owner == runID {
		return nil
	}
	return ErrLeaseBusy
}

func (l *fakeLeases) ReleaseTargetLease(_ context.Context, target, runID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.releaseErrs[runID]; err != nil {
		delete(l.releaseErrs, runID)
		return err
	}
	owner, ok := l.owners[target]
	if !ok {
		return nil // NotFound = success
	}
	if owner != runID {
		return ErrLeaseBusy
	}
	delete(l.owners, target)
	return nil
}

// releaseAs forcibly frees a lease (test helper simulating the other run).
func (l *fakeLeases) releaseAs(target, runID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owners[target] == runID {
		delete(l.owners, target)
	}
}

type fakeHook struct {
	name        string
	acquireErrs []error
	releaseErrs []error
	log         *eventLog
}

func (h *fakeHook) Name() string { return h.name }

func (h *fakeHook) popErr(errs *[]error) error {
	if len(*errs) == 0 {
		return nil
	}
	err := (*errs)[0]
	*errs = (*errs)[1:]
	return err
}

func (h *fakeHook) Acquire(_ context.Context) error {
	h.log.add("hook:" + h.name + ":acquire")
	return h.popErr(&h.acquireErrs)
}

func (h *fakeHook) Release(_ context.Context) error {
	h.log.add("hook:" + h.name + ":release")
	return h.popErr(&h.releaseErrs)
}

type fakeHooks struct {
	mu     sync.Mutex
	hooks  map[string]Hook // by plan name
	events *eventLog
}

func (f *fakeHooks) HooksFor(r Run) ([]Hook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	digest, err := operator.PlanDigest(r.HookPlan)
	if err != nil {
		return nil, err
	}
	if digest != r.HookPlanDigest {
		return nil, fmt.Errorf("hook plan digest mismatch")
	}
	out := make([]Hook, 0, len(r.HookPlan))
	for _, p := range r.HookPlan {
		h, ok := f.hooks[p.Name]
		if !ok {
			return nil, fmt.Errorf("no implementation for hook %q", p.Name)
		}
		out = append(out, h)
	}
	return out, nil
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type fakeExecutor struct {
	mu        sync.Mutex
	calls     int
	artifacts ExecutionOutputs
	err       error
}

func (f *fakeExecutor) Execute(_ context.Context, _ Run) (ExecutionOutputs, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.artifacts, f.err
}

func (f *fakeExecutor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// ---------- helpers ----------

const testTarget = "gpu"

type fixture struct {
	store  *fakeStore
	leases *fakeLeases
	hooks  *fakeHooks
	events *eventLog
	exec   *fakeExecutor
	engine *Engine
	plan   []operator.PlannedHook
	digest string
}

func newFixture(t *testing.T, hookNames ...string) *fixture {
	t.Helper()
	f := &fixture{
		store:  newFakeStore(),
		leases: newFakeLeases(),
		events: &eventLog{},
		exec: &fakeExecutor{artifacts: ExecutionOutputs{Artifacts: Artifacts{
			Dir: "/tmp/artifacts", IndexSHA256: "abc", ArtifactDigest: "digest",
		}}},
	}
	f.hooks = &fakeHooks{hooks: map[string]Hook{}, events: f.events}
	for _, name := range hookNames {
		f.hooks.hooks[name] = &fakeHook{name: name, log: f.events}
	}
	f.plan = buildPlan(hookNames)
	digest, err := PlanDigestOf(f.plan)
	if err != nil {
		t.Fatal(err)
	}
	f.digest = digest
	f.engine = &Engine{
		Store:    f.store,
		Leases:   f.leases,
		Hooks:    f.hooks,
		Executor: f.exec,
		Log:      discardLogger(),
		Clock:    time.Now,
		Interval: time.Millisecond,
	}
	return f
}

func buildPlan(names []string) []operator.PlannedHook {
	plan := make([]operator.PlannedHook, 0, len(names))
	for _, n := range names {
		plan = append(plan, operator.PlannedHook{
			PlanVersion: operator.PlanVersion,
			Kind:        operator.KindCommand,
			Name:        n,
			Command:     []string{"acquire", n},
			Release:     []string{"release", n},
		})
	}
	return plan
}

// PlanDigestOf re-exports the operator digest computation for tests.
func PlanDigestOf(plan []operator.PlannedHook) (string, error) { return operator.PlanDigest(plan) }

func discardLogger() logr.Logger { return logr.Discard() }

func (f *fixture) newRun(id string) Run {
	now := time.Now()
	r := Run{
		ID:                  id,
		Target:              testTarget,
		Experiment:          "experiments/m/e/config.yaml",
		RecipeJSON:          "{}",
		RecipeSchemaVersion: 1,
		PromptSHA256:        "deadbeef",
		HookPlan:            f.plan,
		HookPlanDigest:      f.digest,
		Phase:               PhasePending,
		ExecutionResult:     ResultNone,
		ExecutionState:      ExecNotStarted,
		LeaseState:          LeaseAcquiring,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	for _, p := range f.plan {
		r.Hooks = append(r.Hooks, HookState{Name: p.Name, Phase: HookNotStarted})
	}
	return r
}

// ---------- tests ----------

func TestHappyPathOrdering(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Drain(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	r, err := f.store.LoadRun(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s, want succeeded (wait=%q hooks=%+v)", r.Phase, r.WaitReason, r.Hooks)
	}
	want := []string{
		"hook:a:acquire", "hook:b:acquire", "hook:c:acquire",
		"hook:c:release", "hook:b:release", "hook:a:release",
	}
	got := f.events.all()
	if len(got) != len(want) {
		t.Fatalf("events = %v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("event %d = %s, want %s (all=%v)", i, got[i], w, got)
		}
	}
	if r.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s", r.Phase)
	}
}

func TestHookPendingThenProceeds(t *testing.T) {
	f := newFixture(t, "a", "b")
	b := f.hooks.hooks["b"].(*fakeHook)
	b.acquireErrs = []error{ErrPending, nil}
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Drain(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	r, _ := f.store.LoadRun(context.Background(), "r1")
	if r.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s", r.Phase)
	}
	if b.acquireErrs != nil && len(b.acquireErrs) != 0 {
		t.Fatal("pending error not consumed")
	}
	if r.WaitReason != "" {
		t.Fatalf("wait reason not cleared: %q", r.WaitReason)
	}
}

func TestAcquireFailureMergesIntoReleasing(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	b := f.hooks.hooks["b"].(*fakeHook)
	b.acquireErrs = []error{errors.New("github api 500")}
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Drain(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	r, _ := f.store.LoadRun(context.Background(), "r1")
	if r.Phase != PhaseFailed {
		t.Fatalf("phase = %s, want failed", r.Phase)
	}
	if r.ExecutionResult != ResultFailure {
		t.Fatalf("result = %s", r.ExecutionResult)
	}
	if f.exec.callCount() != 0 {
		t.Fatal("executor must not run after acquire failure")
	}
	// b was acquiring → released; a was acquired → released; c never started.
	if got := f.events.all(); fmt.Sprint(got) != "[hook:a:acquire hook:b:acquire hook:b:release hook:a:release]" {
		// c never started, so it must never be released.
		t.Fatalf("events = %v", got)
	}
	// The target lease must be released even though the run failed.
	if err := f.leases.AcquireTargetLease(context.Background(), testTarget, "next"); err != nil {
		t.Fatalf("lease not released: %v", err)
	}
}

func TestExecuteFailureSkipsFinalizing(t *testing.T) {
	f := newFixture(t, "a")
	f.exec.err = errors.New("invoke failed")
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Drain(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	r, _ := f.store.LoadRun(context.Background(), "r1")
	if r.Phase != PhaseFailed {
		t.Fatalf("phase = %s, want failed", r.Phase)
	}
	if r.ExecutionState != ExecCompleted {
		t.Fatalf("execution state = %s", r.ExecutionState)
	}
}

func TestInvokingRecoveryDoesNotRerun(t *testing.T) {
	f := newFixture(t, "a")
	r := f.newRun("r1")
	r.Phase = PhaseRunning
	r.ExecutionState = ExecInvoking
	r.LeaseState = LeaseAcquired
	for i := range r.Hooks {
		r.Hooks[i].Phase = HookAcquired
	}
	if err := f.store.SaveRun(context.Background(), &r); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Drain(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.LoadRun(context.Background(), "r1")
	if got.Phase != PhaseFailed {
		t.Fatalf("phase = %s, want failed", got.Phase)
	}
	if got.ExecutionResult != ResultFailure || got.WaitReason != "execution interrupted" {
		t.Fatalf("result = %s wait = %q", got.ExecutionResult, got.WaitReason)
	}
	if f.exec.callCount() != 0 {
		t.Fatalf("executor re-ran %d times", f.exec.callCount())
	}
	// Restoration must still complete.
	if err := f.leases.AcquireTargetLease(context.Background(), testTarget, "next"); err != nil {
		t.Fatalf("lease not released: %v", err)
	}
}

func TestCompletedInRunningIsIntegrityFailure(t *testing.T) {
	f := newFixture(t, "a")
	r := f.newRun("r1")
	r.Phase = PhaseRunning
	r.ExecutionState = ExecCompleted
	r.LeaseState = LeaseAcquired
	for i := range r.Hooks {
		r.Hooks[i].Phase = HookAcquired
	}
	if err := f.store.SaveRun(context.Background(), &r); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Drain(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.LoadRun(context.Background(), "r1")
	if got.Phase != PhaseFailed || got.ExecutionResult != ResultFailure {
		t.Fatalf("phase = %s result = %s", got.Phase, got.ExecutionResult)
	}
	if f.exec.callCount() != 0 {
		t.Fatal("executor must not re-run")
	}
}

func TestLegacyFinalizingRecordMigratesToSucceeded(t *testing.T) {
	f := newFixture(t, "a")
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.engine.Drain(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	// Simulate a record written by v1.5: it waited for the controller to
	// publish. The worker must recognize it and finish without any external
	// effect (publication is not part of a run's success condition).
	r, err := f.store.LoadRun(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	r.Phase = PhaseFinalizing
	r.PublicURL = "https://legacy.invalid/runs/r1"
	r.PublishError = "legacy publication error"
	if err := f.store.SaveRun(ctx, &r); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Drain(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.LoadRun(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s, want succeeded", got.Phase)
	}
	if got.PublicURL != "https://legacy.invalid/runs/r1" {
		t.Fatalf("legacy publication fields must be preserved: %q", got.PublicURL)
	}
}

func TestReleaseErrorNeverAdvances(t *testing.T) {
	f := newFixture(t, "a", "b")
	b := f.hooks.hooks["b"].(*fakeHook)
	b.releaseErrs = []error{ErrPending, errors.New("k8s 500"), nil}
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.engine.Drain(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	r, _ := f.store.LoadRun(ctx, "r1")
	if r.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s", r.Phase)
	}
	if n := len(b.releaseErrs); n != 0 {
		t.Fatalf("release errors not consumed: %d", n)
	}
	// Both error kinds must have been recorded while releasing.
	if r.Hooks[1].Error != "" || r.Hooks[1].WaitReason == "" {
		t.Fatalf("hook state = %+v", r.Hooks[1])
	}
}

func TestLeaseReleaseErrorStaysReleasing(t *testing.T) {
	f := newFixture(t, "a")
	f.leases.releaseErrs["r1"] = errors.New("conflict")
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.engine.Drain(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	r, _ := f.store.LoadRun(ctx, "r1")
	if r.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s", r.Phase)
	}
	if r.LeaseState != LeaseReleased {
		t.Fatalf("lease state = %s", r.LeaseState)
	}
}

func TestLeaseBusyWaitsThenProceeds(t *testing.T) {
	f := newFixture(t, "a")
	f.leases.owners[testTarget] = "other-run"
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.engine.Drain(ctx, "r1") }()
	// Wait until the run observes the busy lease, then free it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, _ := f.store.LoadRun(ctx, "r1")
		if r.WaitReason == "target busy" {
			f.leases.releaseAs(testTarget, "other-run")
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never observed target busy")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r, _ := f.store.LoadRun(ctx, "r1")
	if r.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s", r.Phase)
	}
}

func TestPostEffectConflictsNeverDoubleRun(t *testing.T) {
	cases := []struct {
		rule       string
		wantPhase  Phase
		wantResult ExecutionResult
		hookCalls  int // per phase (acquire and release counts separately)
	}{
		{rule: "hookacquired", wantPhase: PhaseSucceeded, wantResult: ResultSuccess, hookCalls: 2},
		{rule: "hookreleased", wantPhase: PhaseSucceeded, wantResult: ResultSuccess, hookCalls: 2},
		{rule: "leasereleased", wantPhase: PhaseSucceeded, wantResult: ResultSuccess, hookCalls: 1},
		{rule: "execcompleted", wantPhase: PhaseFailed, wantResult: ResultFailure, hookCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			f := newFixture(t, "a")
			store := &conflictStore{inner: f.store, conflicts: map[string]int{tc.rule: 1}}
			f.engine.Store = store
			if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := f.engine.Drain(ctx, "r1"); err != nil {
				t.Fatal(err)
			}
			r, _ := f.store.LoadRun(ctx, "r1")
			if r.Phase != tc.wantPhase || r.ExecutionResult != tc.wantResult {
				t.Fatalf("phase = %s result = %s, want %s/%s", r.Phase, r.ExecutionResult, tc.wantPhase, tc.wantResult)
			}
			if f.exec.callCount() != 1 {
				t.Fatalf("executor calls = %d, want 1 (no re-execution)", f.exec.callCount())
			}
			if store.conflicts[tc.rule] != 0 {
				t.Fatalf("conflict %q was never injected: the test window is not covered", tc.rule)
			}
		})
	}
}

// conflictStore forces one version conflict per named save window:
//   - hookacquired: the save that records a hook as acquired
//   - hookreleased: the save that records a hook as released
//   - leasereleased: the save that records the target lease released
//   - execcompleted: the save that records a completed execution
type conflictStore struct {
	inner     RunStore
	conflicts map[string]int
}

// classifySave lists every conflict window the save belongs to. A save can
// match several windows (a released hook and a released lease are recorded
// together); each rule fires on the first matching save.
func classifySave(r *Run) []string {
	var rules []string
	if r.Phase == PhaseAcquiring {
		for _, h := range r.Hooks {
			if h.Phase == HookAcquired {
				rules = append(rules, "hookacquired")
				break
			}
		}
	}
	if r.Phase == PhaseReleasing {
		for _, h := range r.Hooks {
			if h.Phase == HookReleased {
				rules = append(rules, "hookreleased")
				break
			}
		}
		if r.LeaseState == LeaseReleased {
			rules = append(rules, "leasereleased")
		}
		if r.ExecutionState == ExecCompleted {
			rules = append(rules, "execcompleted")
		}
	}
	return rules
}

func (s *conflictStore) SaveRun(ctx context.Context, r *Run) error {
	for _, rule := range classifySave(r) {
		if s.conflicts[rule] > 0 {
			s.conflicts[rule]--
			return ErrVersionConflict
		}
	}
	return s.inner.SaveRun(ctx, r)
}

func (s *conflictStore) LoadRun(ctx context.Context, id string) (Run, error) {
	return s.inner.LoadRun(ctx, id)
}

func (s *conflictStore) ListUnfinished(ctx context.Context) ([]Run, error) {
	return s.inner.ListUnfinished(ctx)
}

func TestVersionConflictReloadsWithoutDoubleEffect(t *testing.T) {
	f := newFixture(t, "a")
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	// Force a conflict on the worker's first save: the write-ahead of hook a.
	// The worker must reload and re-decide instead of running the effect.
	f.store.conflicts["r1"] = 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.engine.Drain(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	r, _ := f.store.LoadRun(ctx, "r1")
	if r.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s hooks=%+v", r.Phase, r.Hooks)
	}
	a := f.hooks.hooks["a"].(*fakeHook)
	_ = a
	got := f.events.all()
	count := 0
	for _, e := range got {
		if e == "hook:a:acquire" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("hook a acquired %d times, want 1 (events=%v)", count, got)
	}
}

func TestConcurrentTargetsProceedInParallel(t *testing.T) {
	f := newFixture(t, "a")
	slow := &gateExecutor{gate: make(chan struct{})}
	f.engine.Executor = slow
	r1 := f.newRun("r1")
	if _, err := f.engine.Submit(context.Background(), r1, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go f.engine.Drain(ctx, "r1")

	// While r1 is executing, a second run on a different target must reach
	// running as well.
	slow2 := &gateExecutor{gate: slow.gate}
	f2 := &Engine{
		Store: f.store, Leases: f.leases, Hooks: f.hooks,
		Executor: slow2, Log: discardLogger(),
		Clock: time.Now, Interval: time.Millisecond,
	}
	r2 := f.newRun("r2")
	r2.Target = "other"
	if _, err := f2.Submit(context.Background(), r2, nil); err != nil {
		t.Fatal(err)
	}
	go f2.Drain(ctx, "r2")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := f.store.LoadRun(ctx, "r1")
		b, _ := f.store.LoadRun(ctx, "r2")
		if a.Phase == PhaseRunning && b.Phase == PhaseRunning {
			close(slow.gate)
			<-ctx.Done()
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(slow.gate)
	t.Fatal("runs did not proceed in parallel")
}

type gateExecutor struct {
	gate     chan struct{}
	released chan struct{}
	started  chan struct{}
}

func (g *gateExecutor) Execute(ctx context.Context, _ Run) (ExecutionOutputs, error) {
	if g.started != nil {
		select {
		case <-g.started:
		default:
			close(g.started)
		}
	}
	defer func() {
		if g.released != nil {
			close(g.released)
		}
	}()
	select {
	case <-g.gate:
		return ExecutionOutputs{Artifacts: Artifacts{Dir: "/tmp/x", ArtifactDigest: "digest"}}, nil
	case <-ctx.Done():
		return ExecutionOutputs{}, ctx.Err()
	}
}

func TestSameTargetSerializesOnLease(t *testing.T) {
	f := newFixture(t, "a")
	gate := make(chan struct{})
	f.engine.Executor = &gateExecutor{gate: gate}
	r1 := f.newRun("r1")
	if _, err := f.engine.Submit(context.Background(), r1, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go f.engine.Drain(ctx, "r1")

	// Wait until r1 holds the lease.
	deadline := time.Now().Add(3 * time.Second)
	for {
		cur, _ := f.store.LoadRun(ctx, "r1")
		if cur.LeaseState == LeaseAcquired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("r1 never acquired the lease")
		}
		time.Sleep(2 * time.Millisecond)
	}

	r2 := f.newRun("r2") // same target
	if _, err := f.engine.Submit(context.Background(), r2, nil); err != nil {
		t.Fatal(err)
	}
	drain2 := make(chan error, 1)
	go func() { drain2 <- f.engine.Drain(ctx, "r2") }()

	time.Sleep(10 * time.Millisecond)
	mid, _ := f.store.LoadRun(ctx, "r2")
	if mid.Phase == PhaseSucceeded || mid.Phase == PhaseRunning {
		t.Fatalf("r2 must wait for the lease, got %s", mid.Phase)
	}
	close(gate)
	if err := <-drain2; err != nil {
		t.Fatal(err)
	}
	r2done, _ := f.store.LoadRun(ctx, "r2")
	if r2done.Phase != PhaseSucceeded {
		t.Fatalf("r2 phase = %s", r2done.Phase)
	}
}

type fakeSnapshots struct {
	files map[string]map[string][]byte
}

func (s *fakeSnapshots) WriteInputs(_ context.Context, runID string, files map[string][]byte) error {
	s.files[runID] = files
	return nil
}

func (s *fakeSnapshots) RemoveInputs(_ context.Context, runID string) error {
	delete(s.files, runID)
	return nil
}

func TestSubmitDerivesHookStatesFromPlan(t *testing.T) {
	f := newFixture(t, "a", "b")
	snap := &fakeSnapshots{files: map[string]map[string][]byte{}}
	f.engine.Snapshots = snap
	r := f.newRun("r1")
	// A buggy caller must not be able to inject hook states.
	r.Hooks = []HookState{}
	if _, err := f.engine.Submit(context.Background(), r, map[string][]byte{"prompt.md": []byte("p")}); err != nil {
		t.Fatal(err)
	}
	stored, _ := f.store.LoadRun(context.Background(), "r1")
	if len(stored.Hooks) != 2 {
		t.Fatalf("hooks = %+v", stored.Hooks)
	}
	for _, h := range stored.Hooks {
		if h.Phase != HookNotStarted {
			t.Fatalf("hook %q phase = %s", h.Name, h.Phase)
		}
	}
	if snap.files["r1"]["prompt.md"] == nil {
		t.Fatal("inputs not written")
	}
}

func TestSubmitRejectInputsWithoutSnapshotStore(t *testing.T) {
	f := newFixture(t, "a")
	f.engine.Snapshots = nil
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), map[string][]byte{"p": []byte("x")}); err == nil {
		t.Fatal("expected error for inputs without a snapshot store")
	}
}

func TestDispatcherDrivesRunsToTerminal(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.engine.Interval = time.Millisecond
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		// Run returns only on ctx cancellation; correctness is judged by the
		// run reaching a terminal phase, then we cancel to end the test fast.
		close(done)
	}()
	go func() {
		for {
			r, err := f.store.LoadRun(ctx, "r1")
			if err == nil && r.Phase.Terminal() {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	<-done
	if err := f.engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	r, err := f.store.LoadRun(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Phase.Terminal() {
		t.Fatalf("dispatcher left run in %s", r.Phase)
	}
	if f.exec.callCount() != 1 {
		t.Fatalf("executor calls = %d", f.exec.callCount())
	}
}

func TestDrainDuringDispatchDoesNotDoubleExecute(t *testing.T) {
	f := newFixture(t, "a")
	f.engine.Interval = time.Millisecond
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go f.engine.Run(ctx)
	// Concurrent Drain of the same run must not start a second worker.
	if err := f.engine.Drain(ctx, "r1"); err != nil && !strings.Contains(err.Error(), "already being driven") {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := f.store.LoadRun(ctx, "r1")
		if r.Phase.Terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.exec.callCount() != 1 {
		t.Fatalf("executor calls = %d, want 1", f.exec.callCount())
	}
}

// blockingExecutor models an external effect that cannot be interrupted by
// context cancellation (it must finish before its worker returns).
type blockingExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingExecutor) Execute(_ context.Context, _ Run) (ExecutionOutputs, error) {
	close(b.started)
	<-b.release
	return ExecutionOutputs{Artifacts: Artifacts{Dir: "/tmp/x", ArtifactDigest: "digest"}}, nil
}

func TestRunWaitsForWorkersOnCancel(t *testing.T) {
	f := newFixture(t, "a")
	f.engine.Interval = time.Millisecond
	gate := make(chan struct{})
	released := make(chan struct{})
	started := make(chan struct{})
	f.engine.Executor = &blockingExecutor{started: started, release: gate}
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		_ = f.engine.Run(ctx)
		close(runDone)
	}()
	// Wait until the worker is inside Execute, then cancel.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never reached Execute")
	}
	cancel()
	select {
	case <-runDone:
		t.Fatal("Run returned while a worker was still executing")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after the worker finished")
	}
	_ = released
}

func TestLegacyFinalizingRecordWithoutReleasedLeaseIsRepaired(t *testing.T) {
	// A malformed legacy record must not jump to terminal: it goes through
	// releasing so nothing skips restoration.
	f := newFixture(t, "a")
	if _, err := f.engine.Submit(context.Background(), f.newRun("r1"), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.engine.Drain(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	r, err := f.store.LoadRun(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	r.Phase = PhaseFinalizing
	r.LeaseState = LeaseAcquired
	r.ExecutionResult = ResultFailure
	if err := f.store.SaveRun(ctx, &r); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Drain(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.LoadRun(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != PhaseFailed {
		t.Fatalf("phase = %s, want failed after releasing", got.Phase)
	}
	if got.LeaseState != LeaseReleased {
		t.Fatalf("lease = %s, want released", got.LeaseState)
	}
}

// TestKindSuccessConditions pins the v1.7 rule that the engine, not the
// executor's nil error, decides whether the required output exists
// (docs/optimization.md §5.1).
func TestKindSuccessConditions(t *testing.T) {
	cases := []struct {
		name      string
		kind      RunKind
		out       ExecutionOutputs
		wantPhase Phase
	}{
		{
			name:      "visual with artifact succeeds",
			kind:      RunKindVisual,
			out:       ExecutionOutputs{Artifacts: Artifacts{Dir: "/tmp/x", ArtifactDigest: "d"}},
			wantPhase: PhaseSucceeded,
		},
		{
			name:      "visual without artifact fails",
			kind:      RunKindVisual,
			out:       ExecutionOutputs{Artifacts: Artifacts{Dir: "/tmp/x"}},
			wantPhase: PhaseFailed,
		},
		{
			name: "visual with invalid evidence still succeeds",
			kind: RunKindVisual,
			out: ExecutionOutputs{
				Artifacts: Artifacts{Dir: "/tmp/x", ArtifactDigest: "d"},
				Evidence:  Evidence{Digest: "m", Valid: false, InvalidReasons: []string{"collector gap"}},
			},
			wantPhase: PhaseSucceeded,
		},
		{
			name:      "measurement without evidence fails",
			kind:      RunKindMeasurement,
			out:       ExecutionOutputs{},
			wantPhase: PhaseFailed,
		},
		{
			name: "measurement with invalid evidence fails",
			kind: RunKindMeasurement,
			out: ExecutionOutputs{
				Evidence: Evidence{Digest: "m", Valid: false, InvalidReasons: []string{"foreign process"}},
			},
			wantPhase: PhaseFailed,
		},
		{
			name: "measurement with valid evidence succeeds",
			kind: RunKindMeasurement,
			out: ExecutionOutputs{
				Evidence:           Evidence{Digest: "m", Valid: true},
				RuntimeBuildDigest: "rb", EnvironmentDigest: "env", WorkloadDigest: "wl",
			},
			wantPhase: PhaseSucceeded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "a")
			exec := &recordingExecutor{out: tc.out}
			f.engine.Executor = exec
			r := f.newRun("r1")
			r.Kind = tc.kind
			r.WorkloadDigest = "wl-frozen"
			if _, err := f.engine.Submit(context.Background(), r, nil); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := f.engine.Drain(ctx, "r1"); err != nil {
				t.Fatal(err)
			}
			got, err := f.store.LoadRun(ctx, "r1")
			if err != nil {
				t.Fatal(err)
			}
			if got.Phase != tc.wantPhase {
				t.Fatalf("phase = %s (want %s) wait=%q", got.Phase, tc.wantPhase, got.WaitReason)
			}
			// Provenance is stored regardless of the outcome: MetricsDigest
			// only means "evidence exists".
			if got.MetricsDigest != tc.out.Evidence.Digest {
				t.Fatalf("metrics digest = %q, want %q", got.MetricsDigest, tc.out.Evidence.Digest)
			}
			// Empty executor values must not erase submit-frozen identity.
			if got.RuntimeBuildDigest != tc.out.RuntimeBuildDigest || got.EnvironmentDigest != tc.out.EnvironmentDigest {
				t.Fatalf("provenance not persisted: %+v", got)
			}
			// A non-empty executor value may refine the identity; an empty one
			// must leave the submit-frozen value alone.
			want := tc.out.WorkloadDigest
			if want == "" {
				want = r.WorkloadDigest
			}
			if got.WorkloadDigest != want {
				t.Fatalf("workload digest = %q, want %q", got.WorkloadDigest, want)
			}
		})
	}
}

// recordingExecutor returns fixed outputs and records the run it saw.
type recordingExecutor struct {
	out ExecutionOutputs
	r   Run
}

func (e *recordingExecutor) Execute(_ context.Context, r Run) (ExecutionOutputs, error) {
	e.r = r
	return e.out, nil
}
