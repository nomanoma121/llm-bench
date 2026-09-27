// Package lease defines the GPU ownership contract and the durable job phase
// record the MVP keeps on it.
//
// The controller takes the lease *before* it claims a request, so the lease is
// also the job mutex: two controllers, or an old and a new Pod during a
// rollout, cannot both decide to run the same request (docs/mvp.md §6.1). The
// phase is written ahead of every external effect, so a crash leaves a record
// of what may have happened, which is what recovery reads.
package lease

import (
	"context"
	"time"
)

// Annotation keys of the durable record.
const (
	AnnPhase         = "llmbench.io/phase"
	AnnJobID         = "llmbench.io/job-id"
	AnnIssue         = "llmbench.io/issue"
	AnnJobSpecDigest = "llmbench.io/jobspec-digest"
	AnnBranch        = "llmbench.io/branch"
	AnnCommit        = "llmbench.io/commit"
	AnnPR            = "llmbench.io/pr"
	AnnAttempt       = "llmbench.io/attempt"
	AnnAcquiredAt    = "llmbench.io/acquired-at"
)

// Phase is the job's position in the write-ahead sequence. A phase is written
// before the effect it describes: recovery must treat the effect as possibly
// done, never as certainly not done.
type Phase string

const (
	PhaseAcquired      Phase = "acquired"
	PhasePausing       Phase = "pausing"
	PhasePaused        Phase = "paused"
	PhaseClaiming      Phase = "claiming"
	PhaseClaimReady    Phase = "claim_ready"
	PhaseExecuting     Phase = "executing"
	PhaseExecuted      Phase = "executed"
	PhaseOpeningPR     Phase = "opening_pr"
	PhasePROpen        Phase = "pr_open"
	PhaseDeletingClaim Phase = "deleting_claim"
	PhaseClaimDeleted  Phase = "claim_deleted"
	PhaseRestoring     Phase = "restoring"
	PhaseRestored      Phase = "restored"
	PhaseReleasing     Phase = "releasing"
)

// Record is the durable state of one job.
type Record struct {
	Holder        string
	Phase         Phase
	JobID         string
	Issue         int
	JobSpecDigest string
	Branch        string
	Commit        string
	// PullRequest is the number of the result PR, 0 before it exists.
	PullRequest int
	Attempt     int
	AcquiredAt  time.Time
	ExpiresAt   time.Time
}

// Lease is the single global GPU ownership record.
type Lease interface {
	// Acquire takes the lease for holder. It returns false when the lease is
	// still live and belongs to someone else, in which case the caller must
	// not touch the GPU or the job.
	//
	// A live lease held by holder itself is ambiguous: it is either another
	// writer working on the same job or this controller's own interrupted job.
	// reentrant selects the second reading, and only recovery uses it.
	Acquire(ctx context.Context, holder string, record Record, reentrant bool) (bool, error)
	// Renew extends the lease so a long job is not taken over mid-run.
	Renew(ctx context.Context, holder string) error
	// Annotate updates the durable record. It fails if holder no longer owns
	// the lease, because writing another job's state would corrupt recovery.
	Annotate(ctx context.Context, holder string, mutate func(*Record)) error
	// Release hands the lease back. It is the last step of a job.
	Release(ctx context.Context, holder string) error
	// Get reads the current record; a missing lease is an empty record.
	Get(ctx context.Context) (Record, error)
}
