// Package filestore implements run persistence on the local filesystem.
// It serves exactly one controller process: atomicity comes from temp-file
// renames and O_EXCL creation, and there is no cross-process locking.
package filestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/nomanoma121/llm-bench/internal/run"
)

// writeTemp writes b to a temp file next to target and returns its path.
func writeTemp(target string, b []byte) (string, error) {
	dir := filepath.Dir(target)
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.tmp-%s", filepath.Base(target), hex.EncodeToString(rnd[:])))
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	return tmp, nil
}

// Store persists run records, target leases and recipe input snapshots.
// It serves a single controller process; a mutex makes the read-compare-write
// sequences (the save CAS and lease transitions) atomic within the process.
type Store struct {
	mu         sync.Mutex
	stateDir   string // run records and leases
	outputRoot string // <output>/runs: per-run artifact and input directories
}

// New creates the directory layout and returns a Store.
func New(stateDir, outputRoot string) (*Store, error) {
	for _, d := range []string{
		filepath.Join(stateDir, "runs"),
		filepath.Join(stateDir, "leases"),
		outputRoot,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("filestore: mkdir %s: %w", d, err)
		}
	}
	return &Store{stateDir: stateDir, outputRoot: outputRoot}, nil
}

func (s *Store) runPath(id string) string {
	return filepath.Join(s.stateDir, "runs", id+".json")
}

func (s *Store) leasePath(target string) string {
	return filepath.Join(s.stateDir, "leases", target+".json")
}

// SaveRun implements run.RunStore with compare-and-swap on StoreVersion.
// Versions are opaque strings containing a per-run monotonically increasing
// counter.
func (s *Store) SaveRun(_ context.Context, r *run.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.runPath(r.ID)
	next := 1
	if b, err := os.ReadFile(path); err == nil {
		var stored run.Run
		if err := json.Unmarshal(b, &stored); err != nil {
			return fmt.Errorf("filestore: decode %s: %w", path, err)
		}
		if stored.StoreVersion != r.StoreVersion {
			return fmt.Errorf("filestore: %w (have %q, sent %q)", run.ErrVersionConflict, stored.StoreVersion, r.StoreVersion)
		}
		next, err = strconv.Atoi(stored.StoreVersion)
		if err != nil {
			next = 0
		}
		next++
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filestore: read %s: %w", path, err)
	} else if r.StoreVersion != "" {
		return fmt.Errorf("filestore: %w: run %s does not exist", run.ErrVersionConflict, r.ID)
	}
	// The caller's version is only advanced after the write succeeded:
	// a failed write must not make the caller conflict with itself.
	nextRun := *r
	nextRun.StoreVersion = strconv.Itoa(next)
	b, err := json.MarshalIndent(&nextRun, "", "  ")
	if err != nil {
		return fmt.Errorf("filestore: encode: %w", err)
	}
	if err := writeAtomic(path, b); err != nil {
		return fmt.Errorf("filestore: write %s: %w", path, err)
	}
	r.StoreVersion = nextRun.StoreVersion
	return nil
}

// LoadRun implements run.RunStore.
func (s *Store) LoadRun(_ context.Context, id string) (run.Run, error) {
	b, err := os.ReadFile(s.runPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return run.Run{}, fmt.Errorf("filestore: %w: %s", run.ErrNotFound, id)
	}
	if err != nil {
		return run.Run{}, fmt.Errorf("filestore: read: %w", err)
	}
	var r run.Run
	if err := json.Unmarshal(b, &r); err != nil {
		return run.Run{}, fmt.Errorf("filestore: decode: %w", err)
	}
	return r, nil
}

// ListUnfinished implements run.RunStore.
func (s *Store) ListUnfinished(_ context.Context) ([]run.Run, error) {
	entries, err := os.ReadDir(filepath.Join(s.stateDir, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("filestore: list: %w", err)
	}
	var out []run.Run
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := s.LoadRun(context.Background(), strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		if !r.Phase.Terminal() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// AcquireTargetLease implements run.LeaseStore. The owner is written to a
// temp file first and then hard-linked into place: link(2) creates the name
// only if it does not exist, so the lease either appears complete (with its
// owner recorded) or not at all. A crash can therefore never leave an empty
// lease behind.
func (s *Store) AcquireTargetLease(_ context.Context, target, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.leasePath(target)
	// Bounded reclaim loop: a legacy empty lease (an artifact the O_EXCL
	// implementation could leave behind; this version never writes one) is
	// removed and the acquisition retried.
	for attempt := 0; attempt < 2; attempt++ {
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) == 0 {
			_ = os.Remove(path)
		}
		tmp, err := writeTemp(path, []byte(runID))
		if err != nil {
			return fmt.Errorf("filestore: lease temp: %w", err)
		}
		err = os.Link(tmp, path)
		if err == nil {
			// The lease at path now holds the owner; drop our temp link.
			_ = os.Remove(tmp)
			return nil
		}
		_ = os.Remove(tmp)
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("filestore: lease link: %w", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("filestore: lease read: %w", err)
		}
		if len(strings.TrimSpace(string(b))) == 0 {
			continue // raced reclaim: try again
		}
		if string(b) == runID {
			return nil // already ours: idempotent
		}
		return fmt.Errorf("filestore: %w: target %s is held by another run", run.ErrLeaseBusy, target)
	}
	return fmt.Errorf("filestore: %w: target %s could not be reclaimed", run.ErrLeaseBusy, target)
}

// ReleaseTargetLease implements run.LeaseStore. NotFound is success.
func (s *Store) ReleaseTargetLease(_ context.Context, target, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.leasePath(target)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // already released: idempotent
	}
	if err != nil {
		return fmt.Errorf("filestore: lease read: %w", err)
	}
	if string(b) != runID {
		return fmt.Errorf("filestore: %w: target %s is held by another run", run.ErrLeaseBusy, target)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filestore: lease remove: %w", err)
	}
	return nil
}

// WriteInputs implements run.InputSnapshotter: files are staged in a temp
// directory and renamed into place, so <output>/<runID>/input either does not
// exist or is complete.
func (s *Store) WriteInputs(_ context.Context, runID string, files map[string][]byte) error {
	base := filepath.Join(s.outputRoot, runID)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	// The <runID> directory entry itself must be durable before the run
	// record references it, so sync the parent that holds it.
	if err := syncDir(filepath.Dir(base)); err != nil {
		return fmt.Errorf("filestore: sync run parent: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(base), "."+runID+".tmp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	input := filepath.Join(tmp, "input")
	if err := os.MkdirAll(input, 0o755); err != nil {
		return err
	}
	for name, content := range files {
		target := filepath.Join(input, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeAtomic(target, content); err != nil {
			return err
		}
	}
	// The rename makes the snapshot visible; fsyncing the parent directory
	// makes that directory entry durable against a host crash too.
	if err := os.Rename(input, filepath.Join(base, "input")); err != nil {
		return err
	}
	if err := syncDir(base); err != nil {
		return fmt.Errorf("filestore: sync snapshot dir: %w", err)
	}
	return nil
}

// RemoveInputs implements run.InputSnapshotter (orphan GC).
func (s *Store) RemoveInputs(_ context.Context, runID string) error {
	return os.RemoveAll(filepath.Join(s.outputRoot, runID, "input"))
}

// InputDir returns the frozen input directory for a run.
func (s *Store) InputDir(runID string) string {
	return filepath.Join(s.outputRoot, runID, "input")
}

// ArtifactsDir returns the artifact directory for a run.
func (s *Store) ArtifactsDir(runID string) string {
	return filepath.Join(s.outputRoot, runID)
}

// writeAtomic persists b at path durably: the temp file is written and
// fsynced, then renamed over path and the directory entry is synced too, so
// "persisted" survives host crashes, not only clean process exits.
func writeAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.tmp-%s", filepath.Base(path), hex.EncodeToString(rnd[:])))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("filestore: sync %s: %w", dir, err)
	}
	return nil
}

// syncDir fsyncs a directory so an entry created in it survives a host crash.
// The error is returned: silently skipping the sync would make the durability
// promise depend on the filesystem rather than on us.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}
