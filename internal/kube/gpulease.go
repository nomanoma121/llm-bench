package kube

import (
	"context"
	"fmt"
	"strconv"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/nomanoma121/llm-bench/internal/lease"
)

// The record and phase names live in internal/lease, which the controller
// declares its dependency on; this package only persists them.
type (
	Phase       = lease.Phase
	LeaseRecord = lease.Record
)

// Phase names, re-exported so callers of this package do not need both.
const (
	PhaseReleased      = lease.PhaseReleased
	PhaseAcquired      = lease.PhaseAcquired
	PhasePausing       = lease.PhasePausing
	PhasePaused        = lease.PhasePaused
	PhaseClaiming      = lease.PhaseClaiming
	PhaseClaimReady    = lease.PhaseClaimReady
	PhaseExecuting     = lease.PhaseExecuting
	PhaseExecuted      = lease.PhaseExecuted
	PhaseOpeningPR     = lease.PhaseOpeningPR
	PhasePROpen        = lease.PhasePROpen
	PhaseDeletingClaim = lease.PhaseDeletingClaim
	PhaseClaimDeleted  = lease.PhaseClaimDeleted
	PhaseRestoring     = lease.PhaseRestoring
	PhaseRestored      = lease.PhaseRestored
	PhaseReleasing     = lease.PhaseReleasing
)

// GPULease is the single global GPU ownership record.
type GPULease struct {
	client    kubernetes.Interface
	namespace string
	name      string
	duration  time.Duration
	// now is injectable for tests.
	now func() time.Time
}

// NewGPULease binds the lease helper to one Lease object.
func NewGPULease(client kubernetes.Interface, namespace, name string, duration time.Duration) *GPULease {
	if duration <= 0 {
		duration = 30 * time.Minute
	}
	return &GPULease{client: client, namespace: namespace, name: name, duration: duration, now: time.Now}
}

// Acquire takes the lease for holder. It returns false when another holder has
// a live lease, in which case the caller must not touch the GPU or the job.
//
// The lease is taken *before* the issue is claimed, so it doubles as the job
// mutex: two controllers (or an old and a new Pod during a rollout) cannot
// both decide to run the same request.
func (l *GPULease) Acquire(ctx context.Context, holder string, record LeaseRecord, reentrant bool) (bool, error) {
	leases := l.client.CoordinationV1().Leases(l.namespace)
	for attempt := 0; attempt < 5; attempt++ {
		existing, err := leases.Get(ctx, l.name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			record.Holder = holder
			record.AcquiredAt = l.now()
			record.ExpiresAt = record.AcquiredAt.Add(l.duration)
			_, err := leases.Create(ctx, l.leaseObject(record), metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				continue // someone created it first; read it again
			}
			return err == nil, err
		case err != nil:
			return false, fmt.Errorf("kube: read gpu lease: %w", err)
		}

		current := decodeLease(existing)
		if current.Holder != "" && l.now().Before(current.ExpiresAt) {
			// Someone holds it and the lease has not expired. Only the exact
			// same holder, and only for recovery, may take it over: a live
			// lease belongs to a process that may still be measuring.
			if current.Holder != holder || !reentrant {
				return false, nil
			}
		}
		if current.IsUnfinished() {
			// The record describes work that still owes the system something
			// (a pending restore, a release that never completed). Only the
			// same job, and only the recovery path, may take it over: a normal
			// run must not erase that state, and no other job may take the GPU
			// while the workload may still be paused.
			if current.JobID != record.JobID || !reentrant {
				return false, nil
			}
		}
		record.Holder = holder
		if current.JobID == record.JobID {
			// Re-acquiring the same job keeps its progress (recovery), but the
			// attempt counter still advances when the caller asked for it.
			record.AcquiredAt = current.AcquiredAt
		} else {
			record.AcquiredAt = l.now()
		}
		record.ExpiresAt = l.now().Add(l.duration)
		updated := l.leaseObject(record)
		updated.ResourceVersion = existing.ResourceVersion
		if _, err := leases.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return false, fmt.Errorf("kube: take gpu lease: %w", err)
		}
		return true, nil
	}
	return false, fmt.Errorf("kube: gpu lease %s/%s kept changing; giving up this attempt", l.namespace, l.name)
}

// Renew extends the lease. A job that takes longer than the lease duration
// must renew, or another controller would take the GPU away mid-run.
func (l *GPULease) Renew(ctx context.Context, holder string) error {
	return l.update(ctx, holder, func(r *LeaseRecord) {
		r.ExpiresAt = l.now().Add(l.duration)
	})
}

// Annotate updates the durable phase record. The holder must still own the
// lease: writing someone else's job state would corrupt the recovery path.
func (l *GPULease) Annotate(ctx context.Context, holder string, mutate func(*LeaseRecord)) error {
	return l.update(ctx, holder, mutate)
}

func (l *GPULease) update(ctx context.Context, holder string, mutate func(*LeaseRecord)) error {
	leases := l.client.CoordinationV1().Leases(l.namespace)
	for attempt := 0; attempt < 5; attempt++ {
		existing, err := leases.Get(ctx, l.name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("kube: read gpu lease: %w", err)
		}
		record := decodeLease(existing)
		if record.Holder != holder {
			return fmt.Errorf("kube: gpu lease %s/%s is held by %q, not %q", l.namespace, l.name, record.Holder, holder)
		}
		mutate(&record)
		updated := l.leaseObject(record)
		updated.ResourceVersion = existing.ResourceVersion
		if _, err := leases.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return fmt.Errorf("kube: update gpu lease: %w", err)
		}
		return nil
	}
	return fmt.Errorf("kube: gpu lease %s/%s kept changing; the update may not have landed", l.namespace, l.name)
}

// Release hands the lease back. It is the last step of a job: until it
// succeeds, the job is not finished and restoration is still due.
func (l *GPULease) Release(ctx context.Context, holder string) error {
	leases := l.client.CoordinationV1().Leases(l.namespace)
	for attempt := 0; attempt < 5; attempt++ {
		existing, err := leases.Get(ctx, l.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("kube: read gpu lease: %w", err)
		}
		record := decodeLease(existing)
		if record.Holder != holder {
			return fmt.Errorf("kube: refusing to release a lease held by %q", record.Holder)
		}
		record.Phase = PhaseReleased
		record.Holder = ""
		record.ExpiresAt = time.Time{}
		// The annotations stay: they are the record of what the last job did,
		// and the next job's write-ahead replaces them.
		updated := l.leaseObject(record)
		updated.ResourceVersion = existing.ResourceVersion
		if _, err := leases.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return fmt.Errorf("kube: release gpu lease: %w", err)
		}
		return nil
	}
	return fmt.Errorf("kube: gpu lease %s/%s kept changing; the release may not have landed", l.namespace, l.name)
}

// Get reads the current record. A missing lease is an empty record.
func (l *GPULease) Get(ctx context.Context) (LeaseRecord, error) {
	lease, err := l.client.CoordinationV1().Leases(l.namespace).Get(ctx, l.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return LeaseRecord{}, nil
	}
	if err != nil {
		return LeaseRecord{}, fmt.Errorf("kube: read gpu lease: %w", err)
	}
	return decodeLease(lease), nil
}

func (l *GPULease) leaseObject(r LeaseRecord) *coordinationv1.Lease {
	seconds := int32(l.duration.Seconds())
	renew := metav1.NewMicroTime(l.now())
	annotations := map[string]string{
		lease.AnnPhase:         string(r.Phase),
		lease.AnnJobID:         r.JobID,
		lease.AnnJobSpecDigest: r.JobSpecDigest,
	}
	setIfNotEmpty(annotations, lease.AnnBranch, r.Branch)
	setIfNotEmpty(annotations, lease.AnnCommit, r.Commit)
	if r.PullRequest > 0 {
		annotations[lease.AnnPR] = strconv.Itoa(r.PullRequest)
	}
	if !r.AcquiredAt.IsZero() {
		annotations[lease.AnnAcquiredAt] = r.AcquiredAt.Format(time.RFC3339Nano)
	}
	setIfNotEmpty(annotations, lease.AnnOutcome, string(r.Outcome))
	if r.Issue > 0 {
		annotations[lease.AnnIssue] = strconv.Itoa(r.Issue)
	}
	if r.Attempt > 0 {
		annotations[lease.AnnAttempt] = strconv.Itoa(r.Attempt)
	}
	holder := r.Holder
	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:        l.name,
			Namespace:   l.namespace,
			Annotations: annotations,
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holder,
			LeaseDurationSeconds: &seconds,
			RenewTime:            &renew,
		},
	}
}

func setIfNotEmpty(m map[string]string, key, value string) {
	if value != "" {
		m[key] = value
	}
}

func decodeLease(obj *coordinationv1.Lease) LeaseRecord {
	out := LeaseRecord{}
	if obj.Spec.HolderIdentity != nil {
		out.Holder = *obj.Spec.HolderIdentity
	}
	if obj.Spec.RenewTime != nil {
		// Expiry is derived from the last renewal, so a stale holder cannot
		// keep the GPU by doing nothing.
		out.ExpiresAt = obj.Spec.RenewTime.Time.Add(time.Duration(ptrInt32(obj.Spec.LeaseDurationSeconds, 0)) * time.Second)
	}
	for key, value := range obj.Annotations {
		switch key {
		case lease.AnnPhase:
			out.Phase = Phase(value)
		case lease.AnnJobID:
			out.JobID = value
		case lease.AnnIssue:
			out.Issue, _ = strconv.Atoi(value)
		case lease.AnnJobSpecDigest:
			out.JobSpecDigest = value
		case lease.AnnBranch:
			out.Branch = value
		case lease.AnnCommit:
			out.Commit = value
		case lease.AnnPR:
			out.PullRequest, _ = strconv.Atoi(value)
		case lease.AnnAttempt:
			out.Attempt, _ = strconv.Atoi(value)
		case lease.AnnAcquiredAt:
			out.AcquiredAt, _ = time.Parse(time.RFC3339Nano, value)
		case lease.AnnOutcome:
			out.Outcome = lease.Outcome(value)
		}
	}
	return out
}

func ptrInt32(v *int32, fallback int32) int32 {
	if v == nil {
		return fallback
	}
	return *v
}
