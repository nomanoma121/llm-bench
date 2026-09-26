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
	PhasePending   Phase = "pending"
	PhaseAcquiring Phase = "acquiring"
	PhaseRunning   Phase = "running"
	PhaseReleasing Phase = "releasing"
	// PhaseFinalizing is retained only to decode records written before v1.6.
	// New runs never enter it: releasing goes straight to succeeded or failed,
	// and a legacy record in this phase migrates to succeeded (§3.1).
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
	// LeaseNotApplicable belongs to window-bound runs, which do not own the
	// target lease (docs/architecture.md §3.1).
	LeaseNotApplicable LeaseState = "not_applicable"
)

// HookState is the per-hook progress stored on the run.
type HookState struct {
	Name       string    `json:"name"`
	Phase      HookPhase `json:"phase"`
	WaitReason string    `json:"wait_reason,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// RunKind selects the required output of a run. The state machine is shared;
// only the success condition differs (docs/optimization.md §3).
type RunKind string

const (
	// RunKindVisual requires a sealed visual artifact (output/index.html).
	RunKindVisual RunKind = "visual"
	// RunKindMeasurement requires sealed, valid measurement evidence and
	// creates no visual payload.
	RunKindMeasurement RunKind = "measurement"
)

// KindOrDefault maps an empty kind (legacy v1.6 records) to visual. New runs
// always store a non-empty kind.
func KindOrDefault(k RunKind) RunKind {
	if k == "" {
		return RunKindVisual
	}
	return k
}

// Evidence identifies the sealed measurement evidence of a run. An empty
// Digest means no evidence exists (a normal state for visual runs); Valid is
// the measurement_valid flag computed by the harness, so the engine never has
// to parse the evidence JSON itself.
type Evidence struct {
	Path           string   `json:"path,omitempty"`
	Digest         string   `json:"digest,omitempty"`
	Valid          bool     `json:"valid,omitempty"`
	InvalidReasons []string `json:"invalid_reasons,omitempty"`
}

// ExecutionOutputs is everything one execution produced. The executor returns
// it as a unit so the engine can persist provenance in the same CAS write as
// the completion state.
type ExecutionOutputs struct {
	Artifacts          Artifacts
	Evidence           Evidence
	RuntimeBuildDigest string
	EnvironmentDigest  string
	WorkloadDigest     string
}

// Artifacts identifies the run outputs persisted outside the sandbox.
type Artifacts struct {
	Dir               string `json:"dir"`
	IndexSHA256       string `json:"index_sha256,omitempty"`
	LogSHA256         string `json:"log_sha256,omitempty"`
	ModelTreeDigest   string `json:"model_tree_digest,omitempty"`
	ModelIdentityPath string `json:"model_identity_path,omitempty"`
	// ArtifactDigest is the payload digest computed at the moment the artifact
	// was sealed (docs/architecture.md §4.4). A non-empty value is what makes
	// the artifact servable by the preview and eligible for review/adopt;
	// preview, review, adopt and adopted verify all use
	// provenance.ArtifactDigest instead of re-implementing a hashing rule.
	ArtifactDigest string `json:"artifact_digest,omitempty"`
}

// Run is the durable record of one benchmark execution.
type Run struct {
	ID          string `json:"id"`
	Target      string `json:"target"`
	Experiment  string `json:"experiment"`
	InputCommit string `json:"input_commit,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// ControllerVersion is the version of the binary that submitted the run.
	// It cannot be recovered from the fingerprint, so it is persisted here for
	// the adoption manifest.
	ControllerVersion string `json:"controller_version,omitempty"`
	// ── v1.7 measurement identity(docs/optimization.md §5/§6)──
	// Kind is always set for new runs; an empty value decodes as legacy visual.
	Kind          RunKind `json:"kind,omitempty"`
	RequestID     string  `json:"request_id,omitempty"` // idempotency key; RunID derives from it
	RequestDigest string  `json:"request_digest,omitempty"`
	// MeasurementProtocolID/JSON/Digest freeze how this run was measured
	// (empty when the run carries no protocol).
	MeasurementProtocolID     string `json:"measurement_protocol_id,omitempty"`
	MeasurementProtocolJSON   string `json:"measurement_protocol_json,omitempty"`
	MeasurementProtocolDigest string `json:"measurement_protocol_digest,omitempty"`
	// WorkloadDigest covers the executed workload matrix, RuntimeSpecDigest
	// what was meant to be built and RuntimeBuildDigest what actually ran.
	WorkloadDigest     string `json:"workload_digest,omitempty"`
	RuntimeSpecDigest  string `json:"runtime_spec_digest,omitempty"`
	RuntimeBuildDigest string `json:"runtime_build_digest,omitempty"`
	EnvironmentDigest  string `json:"environment_digest,omitempty"`
	// MetricsDigest identifies sealed measurement evidence. It is independent
	// of ArtifactDigest: "evidence exists" is not "the run succeeded".
	MetricsDigest       string                 `json:"metrics_digest,omitempty"`
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
	PublishError        string                 `json:"publish_error,omitempty"` // legacy (v1.5): read-only
	Artifacts           Artifacts              `json:"artifacts"`
	PublicURL           string                 `json:"public_url,omitempty"` // legacy (v1.5): read-only; CI decides the published URL
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
	// Execute runs the benchmark (or measurement). Callers must persist
	// ExecutionState=invoking first and apply the execution timeout. The
	// returned outputs must already be durably written; provenance is stored
	// in the same CAS write as the completion state, also when Execute
	// returns an error (a failed run may still own sealed evidence).
	Execute(ctx context.Context, r Run) (ExecutionOutputs, error)
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
