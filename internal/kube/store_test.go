package kube

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

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
