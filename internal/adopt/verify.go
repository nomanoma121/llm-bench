package adopt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/nomanoma121/llm-bench/internal/provenance"
)

// Verify checks every adopted artifact under root (the experiments tree):
// schema, directory placement, the payload inventory and digest, and the
// global invariant that a model has at most one review-less adoption.
//
// The check lives in Go so the site toolchain never re-implements hashing.
func Verify(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var problems []error
	unreviewed := map[string]int{}
	for _, modelDir := range entries {
		if !modelDir.IsDir() {
			continue
		}
		model := modelDir.Name()
		experiments, err := os.ReadDir(filepath.Join(root, model))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		for _, expDir := range experiments {
			if !expDir.IsDir() {
				continue
			}
			dir := filepath.Join(root, model, expDir.Name(), "output")
			m, err := readManifestFile(filepath.Join(dir, ManifestName))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", dir, err))
				continue
			}
			if err := verifyOne(dir, model, expDir.Name(), m); err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", dir, err))
				continue
			}
			if m.Review == nil {
				unreviewed[model]++
			}
		}
	}
	for model, n := range unreviewed {
		if n > 1 {
			// Parallel pull requests can each look like the first adoption.
			// This check runs in CI on main, where the duplicate is visible.
			problems = append(problems, fmt.Errorf("model %q has %d review-less adoptions (at most one baseline is allowed)", model, n))
		}
	}
	return errors.Join(problems...)
}

// verifyOne validates a manifest against the directory that holds it.
func verifyOne(dir, model, experimentID string, m *Manifest) error {
	if err := validateManifestAgainst(dir, m); err != nil {
		return err
	}
	if m.Model != model {
		return fmt.Errorf("%w: manifest model %q does not match directory %q", errInvalidManifest, m.Model, model)
	}
	if m.ExperimentID != experimentID {
		return fmt.Errorf("%w: manifest experiment_id %q does not match directory %q", errInvalidManifest, m.ExperimentID, experimentID)
	}
	// Symlinks anywhere in the payload are rejected by ArtifactPayload, but a
	// symlinked manifest would bypass the inventory check.
	info, err := os.Lstat(filepath.Join(dir, ManifestName))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: manifest is not a regular file", errInvalidManifest)
	}
	return nil
}

// Load reads an adopted manifest, for the site builder.
func Load(dir string) (*Manifest, error) {
	m, err := readManifestFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	if err := validateManifestAgainst(dir, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Find returns the adopted experiments under root, sorted by adoption time
// (newest first), then model, then experiment ID. This ordering is what the
// generated index uses.
func Find(root string) ([]Adopted, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Adopted
	for _, modelDir := range entries {
		if !modelDir.IsDir() {
			continue
		}
		experiments, err := os.ReadDir(filepath.Join(root, modelDir.Name()))
		if err != nil {
			return nil, err
		}
		for _, expDir := range experiments {
			if !expDir.IsDir() {
				continue
			}
			dir := filepath.Join(root, modelDir.Name(), expDir.Name(), "output")
			m, err := readManifestFile(filepath.Join(dir, ManifestName))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("%s: %w", dir, err)
			}
			if err := verifyOne(dir, modelDir.Name(), expDir.Name(), m); err != nil {
				return nil, fmt.Errorf("%s: %w", dir, err)
			}
			out = append(out, Adopted{Model: modelDir.Name(), ExperimentID: expDir.Name(), Dir: dir, Manifest: *m})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Manifest.AdoptedAt.Equal(out[j].Manifest.AdoptedAt) {
			return out[i].Manifest.AdoptedAt.After(out[j].Manifest.AdoptedAt)
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].ExperimentID < out[j].ExperimentID
	})
	return out, nil
}

// Adopted is one published experiment.
type Adopted struct {
	Model        string
	ExperimentID string
	Dir          string
	Manifest     Manifest
}

// Payload returns the artifact bytes listed in the manifest, re-verified.
func (a Adopted) Payload() ([]byte, error) {
	for _, f := range a.Manifest.Artifacts {
		if f.Path != "index.html" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(a.Dir, f.Path))
		if err != nil {
			return nil, err
		}
		if provenance.SHA256Hex(body) != f.SHA256 {
			return nil, fmt.Errorf("adopt: %s changed after verification", f.Path)
		}
		return body, nil
	}
	return nil, errors.New("adopt: adopted artifact has no index.html")
}
