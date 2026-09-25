package filestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/run"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sampleRun(id, version string) run.Run {
	return run.Run{
		ID:           id,
		Target:       "gpu",
		Phase:        run.PhasePending,
		LeaseState:   run.LeaseAcquiring,
		StoreVersion: version,
	}
}

func TestSaveRunVersionCAS(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := sampleRun("r1", "")
	if err := s.SaveRun(ctx, &r); err != nil {
		t.Fatal(err)
	}
	if r.StoreVersion == "" {
		t.Fatal("version not assigned")
	}

	// Saving with the current version succeeds and bumps it.
	if err := s.SaveRun(ctx, &r); err != nil {
		t.Fatal(err)
	}

	// Saving a stale copy conflicts.
	stale := sampleRun("r1", "0")
	if err := s.SaveRun(ctx, &stale); !errors.Is(err, run.ErrVersionConflict) {
		t.Fatalf("want conflict, got %v", err)
	}

	// Saving a new run with a non-empty version conflicts too.
	fresh := sampleRun("r2", "99")
	if err := s.SaveRun(ctx, &fresh); !errors.Is(err, run.ErrVersionConflict) {
		t.Fatalf("want conflict for unknown run with version, got %v", err)
	}
}

func TestLoadRunNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.LoadRun(context.Background(), "missing"); !errors.Is(err, run.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListUnfinished(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1 := sampleRun("r1", "")
	if err := s.SaveRun(ctx, &r1); err != nil {
		t.Fatal(err)
	}
	r2 := sampleRun("r2", "")
	r2.Phase = run.PhaseSucceeded
	if err := s.SaveRun(ctx, &r2); err != nil {
		t.Fatal(err)
	}
	unfinished, err := s.ListUnfinished(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfinished) != 1 || unfinished[0].ID != "r1" {
		t.Fatalf("unfinished = %+v", unfinished)
	}
}

func TestLeaseSemantics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.AcquireTargetLease(ctx, "gpu", "r1"); err != nil {
		t.Fatal(err)
	}
	// Idempotent re-acquire by the owner.
	if err := s.AcquireTargetLease(ctx, "gpu", "r1"); err != nil {
		t.Fatal(err)
	}
	// Another run is rejected.
	if err := s.AcquireTargetLease(ctx, "gpu", "r2"); !errors.Is(err, run.ErrLeaseBusy) {
		t.Fatalf("want busy, got %v", err)
	}
	// Another run must not be able to release someone else's lease.
	if err := s.ReleaseTargetLease(ctx, "gpu", "r2"); !errors.Is(err, run.ErrLeaseBusy) {
		t.Fatalf("want busy on foreign release, got %v", err)
	}
	// Owner releases.
	if err := s.ReleaseTargetLease(ctx, "gpu", "r1"); err != nil {
		t.Fatal(err)
	}
	// Release is idempotent: NotFound is success.
	if err := s.ReleaseTargetLease(ctx, "gpu", "r1"); err != nil {
		t.Fatal(err)
	}
	// The lease is free again.
	if err := s.AcquireTargetLease(ctx, "gpu", "r2"); err != nil {
		t.Fatal(err)
	}
}

func TestWriteInputsAtomic(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteInputs(context.Background(), "r1", map[string][]byte{
		"config.yaml": []byte("model: m"),
		"prompt.md":   []byte("Create a scene."),
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.InputDir("r1"), "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "Create a scene." {
		t.Fatalf("prompt = %q", b)
	}
	// No leftover temp directories.
	entries, err := os.ReadDir(s.ArtifactsDir("r1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && filepath.Base(e.Name()) != "input" {
			t.Fatalf("unexpected entry %q", e.Name())
		}
	}
	if err := s.RemoveInputs(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
}
