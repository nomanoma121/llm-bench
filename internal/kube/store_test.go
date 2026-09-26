package kube

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/leaderelection"

	"github.com/nomanoma121/llm-bench/internal/run"
)

// newRVClientset emulates the API server's resourceVersion bookkeeping that
// the plain fake clientset omits: creates assign a version, updates enforce
// compare-and-swap.
func newRVClientset() *k8sfake.Clientset {
	client := k8sfake.NewSimpleClientset()
	var mu sync.Mutex
	counter := 0
	next := func() string {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return fmt.Sprint(counter)
	}
	client.PrependReactor("create", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		cm := action.(k8stesting.CreateAction).GetObject().(*corev1.ConfigMap)
		cm.ResourceVersion = next()
		return false, nil, nil
	})
	client.PrependReactor("update", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		upd := action.(k8stesting.UpdateAction).GetObject().(*corev1.ConfigMap)
		obj, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("configmaps"), upd.Namespace, upd.Name)
		if err != nil {
			return false, nil, err
		}
		current := obj.(*corev1.ConfigMap)
		if current.ResourceVersion != upd.ResourceVersion {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, upd.Name, errors.New("resourceVersion mismatch"))
		}
		upd.ResourceVersion = next()
		return false, nil, nil
	})
	return client
}

func newTestRunStore(t *testing.T) (*RunStore, *LeaseStore) {
	t.Helper()
	client := newRVClientset()
	return NewRunStore(client, "bench"), NewLeaseStore(client, "bench")
}

func sampleRun(id, version string) run.Run {
	return run.Run{ID: id, Target: "gpu", Phase: run.PhasePending, LeaseState: run.LeaseAcquiring, StoreVersion: version}
}

func TestRunStoreLifecycle(t *testing.T) {
	store, _ := newTestRunStore(t)
	ctx := context.Background()

	r := sampleRun("r1", "")
	if err := store.SaveRun(ctx, &r); err != nil {
		t.Fatal(err)
	}
	if r.StoreVersion == "" {
		t.Fatal("resourceVersion not captured")
	}
	firstVersion := r.StoreVersion
	// Duplicate create with empty version conflicts.
	dup := sampleRun("r1", "")
	if err := store.SaveRun(ctx, &dup); !errors.Is(err, run.ErrVersionConflict) {
		t.Fatalf("want conflict on duplicate create, got %v", err)
	}
	// Update with the current version works and bumps the version.
	r.Phase = run.PhaseAcquiring
	if err := store.SaveRun(ctx, &r); err != nil {
		t.Fatal(err)
	}
	if r.StoreVersion == firstVersion {
		t.Fatal("version did not advance")
	}
	// CAS: a save carrying the previous version now conflicts.
	stale := sampleRun("r1", firstVersion)
	if err := store.SaveRun(ctx, &stale); !errors.Is(err, run.ErrVersionConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	loaded, err := store.LoadRun(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != run.PhaseAcquiring || loaded.StoreVersion != r.StoreVersion {
		t.Fatalf("loaded = %+v", loaded)
	}
	// ListUnfinished hides terminal runs.
	loaded.Phase = run.PhaseSucceeded
	if err := store.SaveRun(ctx, &loaded); err != nil {
		t.Fatal(err)
	}
	unfinished, err := store.ListUnfinished(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfinished) != 0 {
		t.Fatalf("unfinished = %+v", unfinished)
	}
	// Unknown run.
	if _, err := store.LoadRun(ctx, "missing"); !errors.Is(err, run.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestLeaseStoreSemantics(t *testing.T) {
	_, leases := newTestRunStore(t)
	ctx := context.Background()

	if err := leases.AcquireTargetLease(ctx, "gpu", "r1"); err != nil {
		t.Fatal(err)
	}
	// Same owner re-acquire is idempotent.
	if err := leases.AcquireTargetLease(ctx, "gpu", "r1"); err != nil {
		t.Fatal(err)
	}
	// Another run cannot acquire.
	if err := leases.AcquireTargetLease(ctx, "gpu", "r2"); !errors.Is(err, run.ErrLeaseBusy) {
		t.Fatalf("want busy, got %v", err)
	}
	// Another run cannot release.
	if err := leases.ReleaseTargetLease(ctx, "gpu", "r2"); !errors.Is(err, run.ErrLeaseBusy) {
		t.Fatalf("want busy on foreign release, got %v", err)
	}
	// Owner releases; a second release is success (NotFound).
	if err := leases.ReleaseTargetLease(ctx, "gpu", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := leases.ReleaseTargetLease(ctx, "gpu", "r1"); err != nil {
		t.Fatal(err)
	}
	// Lease is free.
	if err := leases.AcquireTargetLease(ctx, "gpu", "r2"); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithLeadershipRunsAndStops(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		err := RunWithLeadership(ctx, LeaderConfig{
			Client:        client,
			Namespace:     "bench",
			LeaseName:     "llmbench-leader",
			Identity:      "test-1",
			LeaseDuration: 2 * time.Second,
			RenewDeadline: 1 * time.Second,
			RetryPeriod:   100 * time.Millisecond,
		}, func(leaderCtx context.Context) error {
			close(started)
			<-leaderCtx.Done() // the leader context is cancelled on shutdown
			close(stopped)
			return nil
		})
		if err != nil {
			t.Errorf("RunWithLeadership: %v", err)
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("leadership was never acquired")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("losing the lease did not cancel the leader context")
	}
}

func TestAcquireTargetLeaseRetriesAcrossHandoff(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	leases := NewLeaseStore(client, "bench")
	ctx := context.Background()

	// The previous holder deletes the lease between our Create and Get: the
	// waiting run must retry the create instead of failing.
	if err := leases.AcquireTargetLease(ctx, "gpu", "old"); err != nil {
		t.Fatal(err)
	}
	deleted := false
	client.PrependReactor("get", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if deleted {
			return false, nil, nil
		}
		deleted = true
		// Simulate the holder releasing the lease just before our Get.
		_ = client.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("configmaps"), "bench", "llmbench-lease-gpu")
		return true, nil, apierrors.NewNotFound(corev1.Resource("configmaps"), "llmbench-lease-gpu")
	})
	if err := leases.AcquireTargetLease(ctx, "gpu", "new"); err != nil {
		t.Fatalf("handoff race must not fail the waiting run: %v", err)
	}
	owner, err := client.CoreV1().ConfigMaps("bench").Get(ctx, "llmbench-lease-gpu", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if owner.Data["owner"] != "new" {
		t.Fatalf("lease owner = %q", owner.Data["owner"])
	}
}

type fakeElector struct {
	calls   int
	returns int // how many times Run returns before honoring ctx
	onStart func(context.Context)
}

func (f *fakeElector) Run(ctx context.Context) {
	f.calls++
	if f.calls <= f.returns {
		return // simulate "lost leadership"
	}
	if f.onStart != nil {
		go f.onStart(ctx)
	}
	<-ctx.Done()
}

// lateElector returns from Run before invoking the stored OnStartedLeading
// callback, modelling a goroutine that is scheduled after leadership was
// already lost. It captures the first round's callback only.
type lateElector struct {
	mu      sync.Mutex
	onStart func(context.Context)
	calls   int
}

func (l *lateElector) capture(cb func(context.Context)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.onStart == nil {
		l.onStart = cb
	}
}

func (l *lateElector) callback() func(context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.onStart
}

func (l *lateElector) Run(ctx context.Context) {
	l.mu.Lock()
	l.calls++
	call := l.calls
	l.mu.Unlock()
	if call == 1 {
		return // lost before the callback goroutine ran
	}
	<-ctx.Done()
}

func TestLateLeadingCallbackDoesNotRun(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	elec := &lateElector{}
	ran := make(chan struct{}, 1)
	go func() {
		_ = RunWithLeadership(ctx, LeaderConfig{
			Client: client, Namespace: "bench", LeaseName: "l", Identity: "test",
			// A long retry period keeps the second round from starting while
			// the test delivers the late callback.
			RetryPeriod: 2 * time.Second,
			newElector: func(cfg leaderelection.LeaderElectionConfig) (elector, error) {
				elec.capture(cfg.Callbacks.OnStartedLeading)
				return elec, nil
			},
		}, func(context.Context) error {
			select {
			case ran <- struct{}{}:
			default:
			}
			return nil
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		elec.mu.Lock()
		calls := elec.calls
		elec.mu.Unlock()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first round never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // let runOnce close accepting
	if cb := elec.callback(); cb != nil {
		cb(ctx)
	}
	select {
	case <-ran:
		t.Fatal("a callback delivered after the round ended must not run the leader function")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRunWithLeadershipReentersElection(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	elec := &fakeElector{returns: 1} // lose the lease once, then lead
	started := make(chan struct{})
	go func() {
		_ = RunWithLeadership(ctx, LeaderConfig{
			Client: client, Namespace: "bench", LeaseName: "l", Identity: "test",
			RetryPeriod: 10 * time.Millisecond,
			newElector: func(cfg leaderelection.LeaderElectionConfig) (elector, error) {
				elec.onStart = cfg.Callbacks.OnStartedLeading
				return elec, nil
			},
		}, func(leaderCtx context.Context) error {
			select {
			case <-started:
			default:
				close(started)
			}
			<-leaderCtx.Done()
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("leadership was never acquired")
	}
	deadline := time.Now().Add(5 * time.Second)
	for elec.calls < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if elec.calls < 2 {
		t.Fatalf("election was not retried after losing the lease (calls=%d)", elec.calls)
	}
	cancel()
}
