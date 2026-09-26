package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nomanoma121/llm-bench/internal/provenance"
)

// sealOutput turns a fully written staging directory into the run's sealed
// payload and returns the artifact digest. The order is fixed by
// docs/architecture.md §4.9: every file and the staging directory are fsynced,
// the directory is renamed into place, the parent is fsynced, and only then may
// the caller persist ArtifactDigest.
//
// A sealed payload is never modified afterwards: the digest is only meaningful
// while the bytes stay as they were hashed.
func sealOutput(parent, staging string) (digest string, files []provenance.PayloadFile, err error) {
	files, err = provenance.ValidateSingleFilePayload(staging)
	if err != nil {
		return "", nil, fmt.Errorf("runner: %w", err)
	}
	if err := syncTree(staging); err != nil {
		return "", nil, fmt.Errorf("runner: sync staging: %w", err)
	}
	final := filepath.Join(parent, "output")
	if _, statErr := os.Lstat(final); statErr == nil {
		return "", nil, fmt.Errorf("runner: refusing to overwrite existing %s", final)
	} else if !os.IsNotExist(statErr) {
		return "", nil, statErr
	}
	if err := os.Rename(staging, final); err != nil {
		return "", nil, fmt.Errorf("runner: seal payload: %w", err)
	}
	if err := syncDir(parent); err != nil {
		return "", nil, fmt.Errorf("runner: sync artifact dir: %w", err)
	}
	return provenance.PayloadDigest(files), files, nil
}

// stagingOutput returns the sibling staging directory for the payload. It must
// live on the same filesystem as the final directory for the rename to be
// atomic.
func stagingOutput(artifactDir string) string {
	return filepath.Join(artifactDir, "output.staging")
}

// syncTree fsyncs every regular file below dir and then dir itself.
func syncTree(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := syncTree(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
			continue
		}
		if !e.Type().IsRegular() {
			return fmt.Errorf("runner: %s is not a regular file", filepath.Join(dir, e.Name()))
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return syncDir(dir)
}

// syncDir fsyncs a directory so a rename or unlink inside it survives a crash.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
