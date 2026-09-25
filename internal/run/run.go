// Package run implements the benchmark run state machine: the Run record, its
// phases, the ordered acquire/release hook protocol and the Engine that drives
// runs to completion.
//
// Core invariants (see docs/architecture.md §3):
//
//   - Every external effect (target lease, hook, execution) is preceded by a
//     persisted write-ahead state; a crash between the call and the follow-up
//     save resumes at the same step and retries idempotently. The benchmark
//     execution itself is not repeatable, so an interrupted execution becomes
//     a failure instead of being re-run.
//   - An acquire failure does not take a special rollback route: the run
//     enters the common releasing phase, which releases hooks whose recorded
//     phase is acquiring/acquired/releasing, in strict reverse order. Hooks
//     that never started are never released.
//   - Any error during release (ErrPending or otherwise) leaves the run in
//     releasing: the terminal phase is unreachable until the target lease is
//     released.
package run

import (
	"context"
	"errors"
	"time"

	"github.com/nomanoma121/llm-bench/internal/operator"
)

// Phase is the coarse state of a run.
type Phase string

const (
	PhasePending    Phase = "pending"
	PhaseAcquiring  Phase = "acquiring"
	PhaseRunning    Phase = "running"
	PhaseReleasing  Phase = "releasing"
	PhaseFinalizing Phase = "finalizing"
	PhaseSucceeded  Phase = "succeeded"
	PhaseFailed     Phase = "failed"
)

// Terminal reports whether the run reached a final phase.
func (p Phase) Terminal() bool { return p == PhaseSucceeded || p == PhaseFailed }

// ExecutionResult records the outcome of the benchmark execution itself,
// independent of the phase.
type ExecutionResult string

const (
	ResultNone    ExecutionResult = "none"
	ResultSuccess ExecutionResult = "success"
	ResultFailure ExecutionResult = "failure"
)

// ExecutionState is the write-ahead state of the benchmark execution.
type ExecutionState string

const (
	ExecNotStarted ExecutionState = "not_started"
	ExecInvoking   ExecutionState = "invoking"
	ExecCompleted  ExecutionState = "completed"
)

// HookPhase is the write-ahead state of a single hook.
type HookPhase string

const (
	HookNotStarted HookPhase = "not_started"
	HookAcquiring  HookPhase = "acquiring"
	HookAcquired   HookPhase = "acquired"
	HookReleasing  HookPhase = "releasing"
	HookReleased   HookPhase = "released"
)

// LeaseState is the write-ahead state of the target lease. Acquisition and
// release are both write-ahead.
type LeaseState string

const (
	LeaseAcquiring LeaseState = "acquiring"
	LeaseAcquired  LeaseState = "acquired"
	LeaseReleasing LeaseState = "releasing"
	LeaseReleased  LeaseState = "released"
)

// HookState is the per-hook progress stored on the run.
type HookState struct {
	Name       string    `json:"name"`
	Phase      HookPhase `json:"phase"`
	WaitReason string    `json:"wait_reason,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Artifacts identifies the run outputs persisted outside the sandbox.
type Artifacts struct {
	Dir               string `json:"dir"`
	IndexSHA256       string `json:"index_sha256,omitempty"`
	LogSHA256         string `json:"log_sha256,omitempty"`
	ModelTreeDigest   string `json:"model_tree_digest,omitempty"`
	ModelIdentityPath string `json:"model_identity_path,omitempty"`
}

// Run is the durable record of one benchmark execution.
type Run struct {
	ID                  string                 `json:"id"`
	Target              string                 `json:"target"`
	Experiment          string                 `json:"experiment"`
	InputCommit         string                 `json:"input_commit,omitempty"`
	Fingerprint         string                 `json:"fingerprint,omitempty"`
	RecipeSchemaVersion int                    `json:"recipe_schema_version"`
	RecipeJSON          string                 `json:"recipe_json"`
	PromptSHA256        string                 `json:"prompt_sha256"`
	Phase               Phase                  `json:"phase"`
	ExecutionResult     ExecutionResult        `json:"execution_result"`
	ExecutionState      ExecutionState         `json:"execution_state"`
	LeaseState          LeaseState             `json:"lease_state"`
	Hooks               []HookState            `json:"hooks"`
	HookPlan            []operator.PlannedHook `json:"hook_plan"`
	HookPlanDigest      string                 `json:"hook_plan_digest"`
	WaitReason          string                 `json:"wait_reason,omitempty"`
	PublishError        string                 `json:"publish_error,omitempty"`
	Artifacts           Artifacts              `json:"artifacts"`
	PublicURL           string                 `json:"public_url,omitempty"`
	StoreVersion        string                 `json:"store_version,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
}

// Sentinel errors. ErrPending is also produced by command hooks exiting 75.
var (
	ErrPending         = errors.New("run: not ready yet")
	ErrLeaseBusy       = errors.New("run: target lease held")
	ErrVersionConflict = errors.New("run: store version conflict")
	ErrNotFound        = errors.New("run: not found")
)

// Hook is one external effect on a run. Implementations must be idempotent
// for both Acquire and Release: Release must be safe even when the matching
// acquire never took effect.
type Hook interface {
	Name() string
	Acquire(ctx context.Context) error
	Release(ctx context.Context) error
}

// HookSource rebuilds the hook list deterministically from the persisted run.
// Implementations must verify Run.HookPlanDigest before building and fail
// with a persistent error when the plan cannot be honored.
type HookSource interface {
	HooksFor(r Run) ([]Hook, error)
}

// Executor runs the benchmark. Before calling Execute the engine persists
// ExecutionState=invoking; when Execute returns, the artifacts must already be
// persisted to Artifacts.Dir (with hashes computed at that moment).
type Executor interface {
	Execute(ctx context.Context, r Run) (Artifacts, error)
}

// FinalizeResult carries the outcome of publication.
type FinalizeResult struct{ PublicURL string }

// Finalizer publishes and finalizes artifacts. It only runs for successful
// executions, after all resources have been released.
type Finalizer interface {
	Finalize(ctx context.Context, r Run, a Artifacts) (FinalizeResult, error)
}

// RunStore persists run records with compare-and-swap semantics on
// StoreVersion. StoreVersion is opaque; implementations must not interpret it.
type RunStore interface {
	// SaveRun validates that r.StoreVersion matches the stored version,
	// persists the record and sets r.StoreVersion to the new version.
	// A mismatch yields ErrVersionConflict.
	SaveRun(ctx context.Context, r *Run) error
	LoadRun(ctx context.Context, id string) (Run, error)
	// ListUnfinished returns all runs that are not in a terminal phase.
	ListUnfinished(ctx context.Context) ([]Run, error)
}

// LeaseStore is the atomic TargetLease store: one active run per target.
// It never touches SandboxClaims.
type LeaseStore interface {
	// AcquireTargetLease succeeds when the lease is free or already owned by
	// runID (idempotent). ErrLeaseBusy means another run holds it.
	AcquireTargetLease(ctx context.Context, target, runID string) error
	// ReleaseTargetLease is fully idempotent:
	//   owner == runID  -> conditional delete, success
	//   not found       -> success (releases recorded before a crash)
	//   owner != runID  -> ErrLeaseBusy, never delete another run's lease
	ReleaseTargetLease(ctx context.Context, target, runID string) error
}

// InputSnapshotter freezes recipe inputs for a run before the run record is
// written, so restarts never consult mutable repository files.
type InputSnapshotter interface {
	// WriteInputs must persist atomically (temp dir + rename).
	WriteInputs(ctx context.Context, runID string, files map[string][]byte) error
	// RemoveInputs garbage-collects orphaned snapshot directories.
	RemoveInputs(ctx context.Context, runID string) error
}

// Clock returns the current time.
type Clock func() time.Time
