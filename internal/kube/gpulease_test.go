package kube

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/nomanoma121/llm-bench/internal/lease"
)

func newTestLease() (*GPULease, *fake.Clientset) {
	client := fake.NewSimpleClientset()
	l := NewGPULease(client, "llmb", "gpu", time.Minute)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	return l, client
}

func TestAcquireIsMutuallyExclusive(t *testing.T) {
	l, _ := newTestLease()
	ctx := context.Background()
	record := lease.Record{JobID: "job-a", Issue: 1, JobSpecDigest: "d", Phase: lease.PhaseAcquired}
	ok, err := l.Acquire(ctx, "controller/job-a", record, false)
	if err != nil || !ok {
		t.Fatalf("first acquire = %v, %v", ok, err)
	}
	// Another job may not take it while the lease is live.
	ok, err = l.Acquire(ctx, "controller/job-b", lease.Record{JobID: "job-b", Issue: 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a second holder took a live lease")
	}
	// The same job, without reentrancy, is also refused: another writer may be
	// working on it.
	ok, err = l.Acquire(ctx, "controller/job-a", record, false)
	if err != nil || ok {
		t.Fatalf("re-acquire without reentrancy = %v, %v", ok, err)
	}
	// Recovery re-acquires its own interrupted job.
	ok, err = l.Acquire(ctx, "controller/job-a", record, true)
	if err != nil || !ok {
		t.Fatalf("reentrant acquire = %v, %v", ok, err)
	}
}

func TestAcquireTakesOverAnExpiredLease(t *testing.T) {
	l, _ := newTestLease()
	ctx := context.Background()
	if ok, err := l.Acquire(ctx, "controller/job-a", lease.Record{JobID: "job-a", Phase: lease.PhaseAcquired}, false); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	// Ten minutes later the one-minute lease has expired: a different job may
	// take it over, which is how a crashed controller stops blocking the GPU.
	l.now = func() time.Time { return time.Date(2026, 9, 27, 12, 10, 0, 0, time.UTC) }
	ok, err := l.Acquire(ctx, "controller/job-b", lease.Record{JobID: "job-b", Issue: 2, Phase: lease.PhaseAcquired}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("an expired lease was not taken over")
	}
	got, err := l.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.JobID != "job-b" || got.Holder != "controller/job-b" {
		t.Fatalf("record = %+v", got)
	}
}

func TestPhasesAreDurableAndHolderScoped(t *testing.T) {
	l, _ := newTestLease()
	ctx := context.Background()
	holder := "controller/job-a"
	if ok, err := l.Acquire(ctx, holder, lease.Record{JobID: "job-a", Issue: 42, JobSpecDigest: "d", Phase: lease.PhaseAcquired}, false); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if err := l.Annotate(ctx, holder, func(r *lease.Record) {
		r.Phase = lease.PhaseExecuted
		r.Branch = "llmbench/job-a"
		r.Commit = "deadbeef"
		r.PullRequest = 7
	}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != lease.PhaseExecuted || got.Branch != "llmbench/job-a" || got.Commit != "deadbeef" || got.PullRequest != 7 || got.Issue != 42 || got.JobSpecDigest != "d" {
		t.Fatalf("record = %+v", got)
	}
	// A different holder may not write this job's state.
	if err := l.Annotate(ctx, "controller/job-b", func(r *lease.Record) { r.Phase = lease.PhaseRestored }); err == nil {
		t.Fatal("a non-holder annotated the lease")
	}
	// Renewal only moves the expiry.
	if err := l.Renew(ctx, holder); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseRequiresTheHolderAndClearsOwnership(t *testing.T) {
	l, _ := newTestLease()
	ctx := context.Background()
	holder := "controller/job-a"
	if ok, err := l.Acquire(ctx, holder, lease.Record{JobID: "job-a", Phase: lease.PhaseAcquired}, false); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if err := l.Release(ctx, "controller/other"); err == nil {
		t.Fatal("a non-holder released the lease")
	}
	if err := l.Release(ctx, holder); err != nil {
		t.Fatal(err)
	}
	got, err := l.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Holder != "" {
		t.Fatalf("holder after release = %q", got.Holder)
	}
	// With no holder, any job may take the lease again.
	if ok, err := l.Acquire(ctx, "controller/job-c", lease.Record{JobID: "job-c", Phase: lease.PhaseAcquired}, false); err != nil || !ok {
		t.Fatalf("acquire after release: %v %v", ok, err)
	}
}

func TestGetOnAMissingLeaseIsEmpty(t *testing.T) {
	l, _ := newTestLease()
	got, err := l.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Holder != "" || got.JobID != "" {
		t.Fatalf("record = %+v", got)
	}
}
