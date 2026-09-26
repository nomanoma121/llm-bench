package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/nomanoma121/llm-bench/internal/operator"
)

// Engine drives runs through the state machine. It contains no concrete
// external effects: stores, leases, hooks, execution and publication are
// injected.
type Engine struct {
	// inFlight is an engine-wide registry of running workers. It prevents
	// duplicate workers for one run across Run, Drain and Drive.
	mu       sync.Mutex
	inFlight map[string]bool

	Store     RunStore
	Leases    LeaseStore
	Hooks     HookSource
	Executor  Executor
	Snapshots InputSnapshotter
	Log       logr.Logger
	Clock     Clock
	// Interval is the retry delay used whenever a step reports "wait"
	// (ErrPending, lease busy, publication failure). Default: 30s.
	Interval time.Duration
	// MaxExecutionDuration resolves the operator-enforced execution ceiling
	// for a target. Optional: nil means no forced timeout.
	MaxExecutionDuration func(target string) time.Duration
}

// Submit persists the recipe inputs and then the initial run record. The
// order matters: the input snapshot must exist before the run does, so a
// crash never leaves a run without its frozen inputs. The orphaned reverse
// case (snapshot without run) is harmless and garbage-collected on boot.
//
// The caller prepares the Run with ID, target, recipe snapshot fields, hook
// plan and per-hook states; Submit fixes the initial progress state.
func (e *Engine) Submit(ctx context.Context, r Run, inputs map[string][]byte) (Run, error) {
	if r.ID == "" || r.Target == "" {
		return Run{}, errors.New("run: submit requires id and target")
	}
	now := e.now()
	r.Phase = PhasePending
	r.ExecutionResult = ResultNone
	r.ExecutionState = ExecNotStarted
	r.LeaseState = LeaseAcquiring // write-ahead: the worker performs the acquire
	r.CreatedAt = now
	r.UpdatedAt = now
	// The hook states are derived from the frozen plan; caller-supplied
	// values are ignored so a buggy submit can never skip hooks.
	r.Hooks = make([]HookState, len(r.HookPlan))
	for i, p := range r.HookPlan {
		r.Hooks[i] = HookState{Name: p.Name, Phase: HookNotStarted}
	}
	if r.HookPlan == nil {
		r.HookPlan = []operator.PlannedHook{}
	}
	if e.Snapshots != nil {
		if err := e.Snapshots.WriteInputs(ctx, r.ID, inputs); err != nil {
			return Run{}, fmt.Errorf("run: write inputs: %w", err)
		}
	} else if len(inputs) > 0 {
		return Run{}, errors.New("run: engine has no snapshot store but inputs were provided")
	}
	if err := e.Store.SaveRun(ctx, &r); err != nil {
		return Run{}, fmt.Errorf("run: save: %w", err)
	}
	return r, nil
}

// Run is the serve-mode dispatcher: it periodically scans unfinished runs and
// drives each with its own worker goroutine. Runs on different targets
// therefore proceed concurrently; runs on the same target serialize on the
// TargetLease.
//
// When ctx is cancelled Run waits for every worker it started to return, so
// callers can rely on "Run returned" meaning "no worker is mid-effect".
func (e *Engine) Run(ctx context.Context) error {
	var workers sync.WaitGroup
	scan := func() {
		runs, err := e.Store.ListUnfinished(ctx)
		if err != nil {
			e.log().Error(err, "scan unfinished runs")
			return
		}
		for _, r := range runs {
			// Drive registers itself engine-wide; a no-op when a worker for
			// this run already exists.
			workers.Add(1)
			go func(id string) {
				defer workers.Done()
				e.Drive(ctx, id)
			}(r.ID)
		}
	}

	scan()
	tick := time.NewTicker(e.interval())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			workers.Wait() // no worker is mid-effect when we return
			return ctx.Err()
		case <-tick.C:
			scan()
		}
	}
}

// register marks a run as being driven; it reports false when a worker for
// the run already exists. The registry spans Run, Drain and Drive.
func (e *Engine) register(runID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.inFlight == nil {
		e.inFlight = map[string]bool{}
	}
	if e.inFlight[runID] {
		return false
	}
	e.inFlight[runID] = true
	return true
}

func (e *Engine) unregister(runID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.inFlight, runID)
}

// Drain drives a single run synchronously until it reaches a terminal phase.
// It is the submit-CLI path.
func (e *Engine) Drain(ctx context.Context, runID string) error {
	if !e.register(runID) {
		return fmt.Errorf("run: %s is already being driven", runID)
	}
	defer e.unregister(runID)
	e.drive(ctx, runID)
	r, err := e.Store.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	if !r.Phase.Terminal() {
		return fmt.Errorf("run: %s ended in phase %s", runID, r.Phase)
	}
	return nil
}

// Drive is the worker loop for the dispatcher. It returns when the run is
// terminal or ctx is cancelled.
func (e *Engine) Drive(ctx context.Context, runID string) {
	if !e.register(runID) {
		return
	}
	defer e.unregister(runID)
	e.drive(ctx, runID)
}

func (e *Engine) drive(ctx context.Context, runID string) {
	for {
		terminal, wait, err := e.step(ctx, runID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, ErrNotFound) {
				e.log().Info("run disappeared", "run", runID)
				return
			}
			e.log().Error(err, "drive error", "run", runID)
			return
		}
		if terminal {
			return
		}
		if wait {
			if err := sleep(ctx, e.interval()); err != nil {
				return
			}
		}
	}
}

// step performs at most one state-machine step for the run and persists the
// result. It returns terminal=true when the run reached a terminal phase and
// wait=true when the step made no progress and must be retried after the
// retry interval (pending hook, busy lease, publication failure).
//
// Error semantics of this method itself: errors returned to the caller are
// infrastructure failures (store unreachable, run missing). Run-level
// failures (hook errors, execution failure) are persisted on the record and
// reported through the state machine instead.
func (e *Engine) step(ctx context.Context, id string) (terminal bool, wait bool, err error) {
	r, err := e.Store.LoadRun(ctx, id)
	if err != nil {
		return false, false, err
	}
	if r.Phase.Terminal() {
		return true, false, nil
	}
	wait, stepErr := func() (bool, error) {
		switch r.Phase {
		case PhasePending, PhaseAcquiring:
			return e.stepAcquiring(ctx, &r)
		case PhaseRunning:
			return e.stepRunning(ctx, &r)
		case PhaseReleasing:
			return e.stepReleasing(ctx, &r)
		case PhaseFinalizing:
			return e.stepFinalizing(ctx, &r)
		default:
			return false, fmt.Errorf("run: unknown phase %q", r.Phase)
		}
	}()
	if errors.Is(stepErr, ErrVersionConflict) {
		// Another writer updated the record: discard our local copy, reload
		// and re-decide from the stored state. The external effect paired with
		// the conflicted save has not happened yet (saves precede effects).
		return false, false, nil
	}
	return false, wait, stepErr
}

func (e *Engine) stepAcquiring(ctx context.Context, r *Run) (wait bool, err error) {
	if r.Phase == PhasePending {
		r.Phase = PhaseAcquiring
		return false, e.save(ctx, r)
	}
	if r.LeaseState == LeaseAcquiring {
		err := e.Leases.AcquireTargetLease(ctx, r.Target, r.ID)
		switch {
		case err == nil:
			r.LeaseState = LeaseAcquired
			r.WaitReason = ""
			return false, e.save(ctx, r)
		case errors.Is(err, ErrLeaseBusy):
			r.WaitReason = "target busy"
			return true, e.save(ctx, r) // caller keeps waiting
		default:
			// Ambiguous outcomes (the acquire may have succeeded) are safe:
			// the idempotent ReleaseTargetLease recovers them during releasing.
			r.ExecutionResult = ResultFailure
			r.WaitReason = fmt.Sprintf("lease acquire failed: %v", err)
			r.Phase = PhaseReleasing
			return false, e.save(ctx, r)
		}
	}

	idx, acquiring := findCurrentHook(r.Hooks)
	switch {
	case acquiring:
		hooks, err := e.hooksFor(ctx, *r)
		if err != nil {
			return false, err // persistent: cannot rebuild hooks
		}
		hook := hooks[idx]
		err = hook.Acquire(ctx)
		switch {
		case err == nil:
			r.Hooks[idx].Phase = HookAcquired
			r.Hooks[idx].WaitReason = ""
			return false, e.save(ctx, r)
		case errors.Is(err, ErrPending):
			r.Hooks[idx].WaitReason = err.Error()
			r.WaitReason = fmt.Sprintf("hook %q pending", hook.Name())
			return true, e.save(ctx, r)
		default:
			// No special rollback route: the common releasing phase releases
			// this (acquiring) hook and every acquired one, in reverse order.
			r.ExecutionResult = ResultFailure
			r.Hooks[idx].Error = err.Error()
			r.WaitReason = fmt.Sprintf("hook %q acquire failed: %v", hook.Name(), err)
			r.Phase = PhaseReleasing
			return false, e.save(ctx, r)
		}
	case allAcquired(r.Hooks):
		r.Phase = PhaseRunning
		r.WaitReason = ""
		return false, e.save(ctx, r)
	default:
		// The next hook has not been marked acquiring yet: write-ahead it.
		i := nextHookIndex(r.Hooks)
		if i < 0 {
			return false, fmt.Errorf("run: inconsistent hook states: %v", r.Hooks)
		}
		r.Hooks[i].Phase = HookAcquiring
		return false, e.save(ctx, r)
	}
}

func (e *Engine) stepRunning(ctx context.Context, r *Run) (wait bool, err error) {
	switch r.ExecutionState {
	case ExecInvoking:
		// Interrupted execution: never re-run; the outcome is unverifiable.
		r.ExecutionResult = ResultFailure
		r.WaitReason = "execution interrupted"
		r.Phase = PhaseReleasing
		return false, e.save(ctx, r)
	case ExecCompleted:
		// Integrity violation: completed execution but still running phase.
		r.ExecutionResult = ResultFailure
		r.WaitReason = "inconsistent state: completed execution in running phase"
		r.Phase = PhaseReleasing
		return false, e.save(ctx, r)
	}
	r.ExecutionState = ExecInvoking // write-ahead
	r.WaitReason = ""
	if err := e.save(ctx, r); err != nil {
		return false, err
	}

	exCtx := ctx
	if e.MaxExecutionDuration != nil {
		if d := e.MaxExecutionDuration(r.Target); d > 0 {
			var cancel context.CancelFunc
			exCtx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}
	out, execErr := e.Executor.Execute(exCtx, *r)
	r.ExecutionState = ExecCompleted
	// Provenance is persisted even when execution failed: a failed run may
	// still own sealed evidence, and MetricsDigest only means "evidence
	// exists" (docs/optimization.md §5.1).
	r.Artifacts = out.Artifacts
	r.MetricsDigest = out.Evidence.Digest
	r.RuntimeBuildDigest = out.RuntimeBuildDigest
	r.EnvironmentDigest = out.EnvironmentDigest
	r.WorkloadDigest = out.WorkloadDigest
	switch {
	case execErr != nil:
		r.ExecutionResult = ResultFailure
		r.WaitReason = fmt.Sprintf("execute failed: %v", execErr)
	default:
		if reason := successViolation(*r, out); reason != "" {
			// The executor reported success, but the kind's required outputs
			// are missing: success must not be inferred from a nil error.
			r.ExecutionResult = ResultFailure
			r.WaitReason = reason
		} else {
			r.ExecutionResult = ResultSuccess
			r.WaitReason = ""
		}
	}
	r.Phase = PhaseReleasing
	return false, e.save(ctx, r)
}

func (e *Engine) stepReleasing(ctx context.Context, r *Run) (wait bool, err error) {
	// Release hooks with phase acquiring/acquired/releasing, in strict
	// reverse order. Hooks that never started are never touched.
	for i := len(r.Hooks) - 1; i >= 0; i-- {
		switch r.Hooks[i].Phase {
		case HookReleased, HookNotStarted:
			continue
		}
		hooks, err := e.hooksFor(ctx, *r)
		if err != nil {
			return false, err
		}
		hook := hooks[i]
		if r.Hooks[i].Phase != HookReleasing {
			r.Hooks[i].Phase = HookReleasing // write-ahead
			return false, e.save(ctx, r)
		}
		relErr := hook.Release(ctx)
		switch {
		case relErr == nil:
			r.Hooks[i].Phase = HookReleased
			r.Hooks[i].Error = ""
			return false, e.save(ctx, r)
		case errors.Is(relErr, ErrPending):
			r.Hooks[i].WaitReason = relErr.Error()
			r.WaitReason = fmt.Sprintf("hook %q release pending", hook.Name())
			return true, e.save(ctx, r) // stay releasing; retry after interval
		default:
			// Any other error must not advance: restoration completes before
			// the run can become terminal, whatever the error kind.
			r.Hooks[i].Error = relErr.Error()
			r.WaitReason = fmt.Sprintf("hook %q release failed: %v", hook.Name(), relErr)
			return true, e.save(ctx, r)
		}
	}

	// All hooks released: release the target lease (both directions are
	// write-ahead; NotFound counts as success).
	switch r.LeaseState {
	case LeaseAcquiring, LeaseAcquired:
		r.LeaseState = LeaseReleasing
		return false, e.save(ctx, r)
	case LeaseReleasing:
		if err := e.Leases.ReleaseTargetLease(ctx, r.Target, r.ID); err != nil {
			r.WaitReason = fmt.Sprintf("target lease release failed: %v", err)
			return true, e.save(ctx, r) // stay releasing; retry
		}
		r.LeaseState = LeaseReleased
		if r.ExecutionResult == ResultSuccess {
			r.WaitReason = ""
		} // keep the failure reason visible on failed runs
		return false, e.save(ctx, r)
	}
	// LeaseState == released: decide the outcome now. Publication is not part
	// of a run's success condition (§1.5): a run that executed and restored
	// everything is succeeded, and adoption/publication is a human decision
	// handled by CI (§4.12).
	if r.ExecutionResult == ResultSuccess {
		r.Phase = PhaseSucceeded
		r.WaitReason = ""
		return false, e.save(ctx, r)
	}
	r.Phase = PhaseFailed
	return false, e.save(ctx, r)
}

// stepFinalizing migrates records written before v1.6: they waited for the
// controller to publish, which no longer exists. Only a consistent record
// (successful execution with the lease already released) may go straight to
// succeeded; anything else is repaired through the normal releasing path, so
// a malformed record can never reach terminal without releasing its lease.
func (e *Engine) stepFinalizing(ctx context.Context, r *Run) (wait bool, err error) {
	if r.ExecutionResult == ResultSuccess && r.LeaseState == LeaseReleased {
		r.Phase = PhaseSucceeded
		r.WaitReason = ""
		return false, e.save(ctx, r)
	}
	r.WaitReason = "legacy finalizing record is inconsistent; releasing before finishing"
	r.Phase = PhaseReleasing
	return false, e.save(ctx, r)
}

// hooksFor rebuilds the hook list for the run, verifying the plan digest.
func (e *Engine) hooksFor(ctx context.Context, r Run) ([]Hook, error) {
	if e.Hooks == nil {
		return nil, errors.New("run: no hook source configured")
	}
	return e.Hooks.HooksFor(r)
}

func (e *Engine) save(ctx context.Context, r *Run) error {
	r.UpdatedAt = e.now()
	return e.Store.SaveRun(ctx, r)
}

func (e *Engine) now() time.Time {
	if e.Clock == nil {
		return time.Now()
	}
	return e.Clock()
}

func (e *Engine) interval() time.Duration {
	if e.Interval <= 0 {
		return 30 * time.Second
	}
	return e.Interval
}

func (e *Engine) log() logr.Logger {
	if e.Log.GetSink() == nil {
		return logr.Discard()
	}
	return e.Log
}

// findCurrentHook returns the index of the hook currently marked acquiring,
// or (0, false) when none is marked.
func findCurrentHook(hooks []HookState) (int, bool) {
	for i, h := range hooks {
		if h.Phase == HookAcquiring {
			return i, true
		}
	}
	return 0, false
}

// nextHookIndex returns the index of the first hook not yet started.
func nextHookIndex(hooks []HookState) int {
	for i, h := range hooks {
		if h.Phase == HookNotStarted {
			return i
		}
	}
	return -1
}

// allAcquired reports whether every hook reached the acquired phase. An
// empty hook plan counts as acquired: a target without hooks goes straight
// from acquiring to running.
func allAcquired(hooks []HookState) bool {
	for _, h := range hooks {
		if h.Phase != HookAcquired {
			return false
		}
	}
	return true
}

// allReleased reports whether every hook reached the released phase.
func allReleased(hooks []HookState) bool {
	for _, h := range hooks {
		if h.Phase != HookReleased {
			return false
		}
	}
	return len(hooks) > 0
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// successViolation reports why an apparently successful execution does not
// satisfy its kind's success condition, or "" when it does. The engine owns
// this check so a buggy or optimistic executor cannot mark a run succeeded
// without the artifact (visual) or valid evidence (measurement) it promises
// (docs/optimization.md §5.1, §14 phase A).
func successViolation(r Run, out ExecutionOutputs) string {
	switch KindOrDefault(r.Kind) {
	case RunKindMeasurement:
		if out.Evidence.Digest == "" {
			return "measurement run finished without sealed evidence"
		}
		if !out.Evidence.Valid {
			return "measurement evidence is not valid: " + strings.Join(out.Evidence.InvalidReasons, "; ")
		}
	case RunKindVisual:
		if out.Artifacts.ArtifactDigest == "" {
			return "visual run finished without a sealed artifact"
		}
		// A visual run that also carries a protocol still only requires the
		// artifact: invalid evidence must not fail the visual result.
	}
	return ""
}
