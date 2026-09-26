// Package adopt materializes an accepted run artifact into the repository.
// It never decides anything by itself: a human decides, and this package
// copies, verifies and records evidence (docs/architecture.md §4.12).
//
// The published site is built from what this package writes, so every rule
// here exists to make "what a human reviewed" and "what is in git" provably
// the same bytes.
package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/review"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// SchemaVersion is the manifest format written by this package.
const SchemaVersion = 1

// ManifestName is the manifest file inside an adopted output directory.
const ManifestName = provenance.ManifestName

// lockName is the stable destination lock file. It is created once and never
// removed: deleting it would swap the inode and let two processes hold the
// lock at the same time.
const lockName = ".adopt.lock"

// Manifest records how an artifact entered the repository.
type Manifest struct {
	SchemaVersion        int                      `json:"schema_version"`
	RunID                string                   `json:"run_id"`
	ExperimentID         string                   `json:"experiment_id"`
	Model                string                   `json:"model"`
	BenchmarkFingerprint string                   `json:"benchmark_fingerprint"`
	ArtifactDigest       string                   `json:"artifact_digest"`
	PromptSHA256         string                   `json:"prompt_sha256"`
	InputCommit          string                   `json:"input_commit,omitempty"`
	ModelTreeDigest      string                   `json:"model_tree_digest,omitempty"`
	ControllerVersion    string                   `json:"controller_version"`
	AdoptedAt            time.Time                `json:"adopted_at"`
	Review               *ReviewRef               `json:"review"`
	Artifacts            []provenance.PayloadFile `json:"artifacts"`
}

// ReviewRef records the Issue decision that authorized an adoption. Later
// votes never change it: it is the evidence of what was reviewed.
type ReviewRef struct {
	ReviewID      string `json:"review_id"`
	IssueURL      string `json:"issue_url"`
	VoteCommentID int64  `json:"vote_comment_id"`
	Choice        string `json:"choice"`
}

// RunStore is the slice of the run store adoption needs.
type RunStore interface {
	LoadRun(ctx context.Context, id string) (run.Run, error)
}

// Options configures one adoption attempt.
type Options struct {
	Root     string // repository root
	RunID    string
	Into     string // repository-relative: experiments/<model-id>/<experiment-id>
	ReviewID string // optional only for the first artifact of a model
	Write    bool   // false = dry run
	Now      func() time.Time

	Store        RunStore
	ArtifactsDir func(runID string) string
	// Decision resolves a review from the Issue (the canonical record).
	Decision func(ctx context.Context, reviewID string) (review.Decision, error)
}

// Result describes what an adoption did (or would do).
type Result struct {
	Manifest          Manifest
	Destination       string
	NoOp              bool
	ProvenanceUpdated bool
	Replaced          bool
}

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Run performs the adoption. It holds an exclusive lock on the destination for
// the whole sequence (recovery, checks, staging, swap, cleanup).
func Run(ctx context.Context, opts Options) (Result, error) {
	intoAbs, model, experimentID, err := resolveInto(opts.Root, opts.Into)
	if err != nil {
		return Result{}, err
	}
	if opts.Store == nil || opts.ArtifactsDir == nil {
		return Result{}, errors.New("adopt: store and artifact directory resolver are required")
	}
	r, err := opts.Store.LoadRun(ctx, opts.RunID)
	if err != nil {
		return Result{}, fmt.Errorf("adopt: load run: %w", err)
	}
	if r.Phase != run.PhaseSucceeded {
		return Result{}, fmt.Errorf("adopt: run %s is %s, not succeeded", r.ID, r.Phase)
	}
	if r.Artifacts.ArtifactDigest == "" {
		return Result{}, fmt.Errorf("adopt: run %s has no sealed artifact", r.ID)
	}
	if r.ControllerVersion == "" {
		// Runs recorded before the digest/version fields existed cannot be
		// adopted: their provenance cannot be reconstructed.
		return Result{}, fmt.Errorf("adopt: run %s predates artifact sealing; re-run the experiment", r.ID)
	}
	if got := path.Dir(path.Clean(r.Experiment)); got != opts.Into {
		return Result{}, fmt.Errorf("adopt: run %s belongs to %s, not %s", r.ID, got, opts.Into)
	}
	var cfg experiment.Config
	if err := json.Unmarshal([]byte(r.RecipeJSON), &cfg); err != nil {
		return Result{}, fmt.Errorf("adopt: decode recipe snapshot: %w", err)
	}
	if cfg.Model != model {
		return Result{}, fmt.Errorf("adopt: run %s used model %q but the destination is model %q", r.ID, cfg.Model, model)
	}

	sourceDir := filepath.Join(opts.ArtifactsDir(r.ID), "output")
	files, err := provenance.ValidateSingleFilePayload(sourceDir)
	if err != nil {
		return Result{}, fmt.Errorf("adopt: %w", err)
	}
	digest := provenance.PayloadDigest(files)
	if digest != r.Artifacts.ArtifactDigest {
		return Result{}, fmt.Errorf("adopt: artifact changed after sealing (%s != %s)", digest, r.Artifacts.ArtifactDigest)
	}

	unlock, err := lockDestination(intoAbs)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	if err := recoverDestination(intoAbs); err != nil {
		return Result{}, err
	}

	current, err := readManifest(intoAbs)
	if err != nil {
		return Result{}, err
	}
	res := Result{Destination: intoAbs}
	if current != nil {
		switch {
		case current.RunID == r.ID && current.ArtifactDigest == digest:
			// Re-running the same adoption is a no-op and needs no review: the
			// destination already proves the decision was made.
			res.Manifest = *current
			res.NoOp = true
			return res, nil
		case current.ArtifactDigest == digest:
			// Identical bytes, different run: update the recorded provenance
			// so the manifest matches the run the human asked to adopt.
			res.ProvenanceUpdated = true
		default:
			return Result{}, fmt.Errorf("adopt: %s already holds a different artifact (run %s)", opts.Into, current.RunID)
		}
	} else {
		res.Replaced = true
	}

	reviewRef, err := authorize(ctx, opts, r, digest, model)
	if err != nil {
		return Result{}, err
	}

	manifest := Manifest{
		SchemaVersion:        SchemaVersion,
		RunID:                r.ID,
		ExperimentID:         experimentID,
		Model:                model,
		BenchmarkFingerprint: r.Fingerprint,
		ArtifactDigest:       digest,
		PromptSHA256:         r.PromptSHA256,
		InputCommit:          r.InputCommit,
		ModelTreeDigest:      r.Artifacts.ModelTreeDigest,
		ControllerVersion:    r.ControllerVersion,
		AdoptedAt:            now(opts),
		Review:               reviewRef,
		Artifacts:            files,
	}
	res.Manifest = manifest
	if !opts.Write {
		return res, nil
	}
	if err := writeStaged(intoAbs, r.ID, sourceDir, manifest); err != nil {
		return Result{}, err
	}
	return res, nil
}

// authorize enforces the review predicate: an A/B decision naming this run and
// digest, or, only for a model that has no adopted artifact at all, an explicit
// review-less baseline.
func authorize(ctx context.Context, opts Options, r run.Run, digest, model string) (*ReviewRef, error) {
	if opts.ReviewID != "" {
		if opts.Decision == nil {
			return nil, errors.New("adopt: --review requires the operator configuration to read the Issue")
		}
		d, err := opts.Decision(ctx, opts.ReviewID)
		if err != nil {
			return nil, fmt.Errorf("adopt: resolve review: %w", err)
		}
		if !d.Decided() {
			return nil, fmt.Errorf("adopt: review %s is not resolved (choice %q)", d.ReviewID, d.Choice)
		}
		if d.Selected().RunID != r.ID {
			return nil, fmt.Errorf("adopt: review %s chose %s, not run %s", d.ReviewID, d.Selected().RunID, r.ID)
		}
		if d.Selected().Digest != digest {
			return nil, fmt.Errorf("adopt: review %s reviewed artifact %s, not %s", d.ReviewID, d.Selected().Digest, digest)
		}
		return &ReviewRef{
			ReviewID:      d.ReviewID,
			IssueURL:      d.IssueURL,
			VoteCommentID: d.CommentID,
			Choice:        d.Choice,
		}, nil
	}
	// No review: allowed only when the model has no adopted artifact yet. The
	// predicate is per model, not per destination, so picking a fresh
	// experiment ID cannot bypass review.
	adopted, err := adoptedForModel(opts.Root, model)
	if err != nil {
		return nil, err
	}
	if adopted > 0 {
		return nil, fmt.Errorf("adopt: model %q already has %d adopted artifact(s); --review is required", model, adopted)
	}
	return nil, nil
}

// adoptedForModel counts valid manifests under experiments/<model>. An
// unreadable or invalid manifest is a hard error: it must never be mistaken
// for "no adoption yet".
func adoptedForModel(root, model string) (int, error) {
	base := filepath.Join(root, "experiments", model)
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := readManifest(filepath.Join(base, e.Name()))
		if err != nil {
			return 0, err
		}
		if m != nil {
			count++
		}
	}
	return count, nil
}

// resolveInto validates the destination: it must be exactly
// experiments/<model>/<experiment> under the repository root and no component
// may be a symlink, so an adoption can never write outside the repository.
func resolveInto(root, into string) (abs, model, experimentID string, err error) {
	if root == "" {
		return "", "", "", errors.New("adopt: repository root is required")
	}
	if into == "" || strings.Contains(into, `\`) || filepath.IsAbs(into) {
		return "", "", "", fmt.Errorf("adopt: --into must be a relative path (got %q)", into)
	}
	clean := path.Clean(into)
	if clean != into {
		return "", "", "", fmt.Errorf("adopt: --into must be clean (got %q)", into)
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 3 || parts[0] != "experiments" {
		return "", "", "", fmt.Errorf("adopt: --into must look like experiments/<model-id>/<experiment-id> (got %q)", into)
	}
	model, experimentID = parts[1], parts[2]
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", "", err
	}
	cur := rootAbs
	for _, p := range parts {
		cur = filepath.Join(cur, p)
		info, statErr := os.Lstat(cur)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", "", "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", "", fmt.Errorf("adopt: refusing to follow symlink %s", cur)
		}
	}
	return filepath.Join(rootAbs, filepath.FromSlash(clean)), model, experimentID, nil
}

// lockDestination takes an exclusive advisory lock on the destination. The
// lock file is created once and deliberately never deleted.
func lockDestination(intoAbs string) (func(), error) {
	if err := os.MkdirAll(intoAbs, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(intoAbs, lockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("adopt: open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("adopt: lock %s: %w", intoAbs, err)
	}
	return func() { _ = f.Close() }, nil
}

// recoveryEntry is one observed destination state.
type recoveryEntry struct {
	output bool
	tmp    string
	bak    string
}

// recoverDestination brings a destination left behind by an interrupted
// adoption back to a state the decision table can interpret.
func recoverDestination(intoAbs string) error {
	state, err := inspect(intoAbs)
	if err != nil {
		return err
	}
	switch {
	case state.tmp != "" && state.bak != "":
		complete, err := tmpComplete(state.tmp)
		if err != nil {
			return err
		}
		if state.output {
			// The previous adoption completed; the leftovers are garbage.
			return cleanup(intoAbs, state)
		}
		if complete {
			return finishSwap(intoAbs, state)
		}
		if err := os.RemoveAll(state.tmp); err != nil {
			return err
		}
		if err := syncDir(intoAbs); err != nil {
			return err
		}
		// The backup is the only payload left: restore it and start over.
		if err := os.Rename(state.bak, filepath.Join(intoAbs, "output")); err != nil {
			return err
		}
		return syncDir(intoAbs)
	case state.tmp != "" && !state.output:
		complete, err := tmpComplete(state.tmp)
		if err != nil {
			return err
		}
		if complete {
			return finishSwap(intoAbs, state)
		}
		if err := os.RemoveAll(state.tmp); err != nil {
			return err
		}
		return syncDir(intoAbs)
	case state.bak != "" && !state.output:
		if err := os.Rename(state.bak, filepath.Join(intoAbs, "output")); err != nil {
			return err
		}
		return syncDir(intoAbs)
	case state.bak != "" && state.output:
		return cleanup(intoAbs, state)
	}
	return nil
}

// inspect reports the destination state, failing closed when several staging
// or backup directories exist (the recovery table would be ambiguous).
func inspect(intoAbs string) (recoveryEntry, error) {
	var e recoveryEntry
	if _, err := os.Lstat(filepath.Join(intoAbs, "output")); err == nil {
		e.output = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return e, err
	}
	tmps, err := filepath.Glob(filepath.Join(intoAbs, ".adopt-tmp-*"))
	if err != nil {
		return e, err
	}
	baks, err := filepath.Glob(filepath.Join(intoAbs, ".adopt-bak-*"))
	if err != nil {
		return e, err
	}
	if len(tmps) > 1 || len(baks) > 1 {
		return e, fmt.Errorf("adopt: %s holds multiple staging or backup directories; resolve them by hand", intoAbs)
	}
	if len(tmps) == 1 {
		e.tmp = tmps[0]
	}
	if len(baks) == 1 {
		e.bak = baks[0]
	}
	return e, nil
}

// finishSwap completes an interrupted swap: staging becomes output.
func finishSwap(intoAbs string, e recoveryEntry) error {
	if err := os.Rename(e.tmp, filepath.Join(intoAbs, "output")); err != nil {
		return err
	}
	if err := syncDir(intoAbs); err != nil {
		return err
	}
	return cleanup(intoAbs, e)
}

// cleanup removes leftover staging and backup directories.
func cleanup(intoAbs string, e recoveryEntry) error {
	for _, dir := range []string{e.tmp, e.bak} {
		if dir == "" {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return syncDir(intoAbs)
}

// tmpComplete reports whether a staging directory holds a complete, valid
// adoption. Anything short of that is discarded (fail closed).
func tmpComplete(dir string) (bool, error) {
	m, err := readManifestFile(filepath.Join(dir, ManifestName))
	if err != nil {
		// A missing or invalid manifest means the staging directory is
		// incomplete: discard it (fail closed). Real I/O errors still surface.
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errInvalidManifest) {
			return false, nil
		}
		return false, err
	}
	if err := validateManifestAgainst(dir, m); err != nil {
		return false, nil
	}
	return true, nil
}

var errInvalidManifest = errors.New("adopt: invalid manifest")

// readManifest loads the manifest of a destination; it returns nil when the
// destination holds no manifest (an unadopted placeholder or empty directory).
func readManifest(intoAbs string) (*Manifest, error) {
	m, err := readManifestFile(filepath.Join(intoAbs, "output", ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

func readManifestFile(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidManifest, err)
	}
	return &m, nil
}

// validateManifest checks a manifest against the directory that holds it.
func validateManifest(m *Manifest) error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version %d", errInvalidManifest, m.SchemaVersion)
	}
	if !runIDPattern.MatchString(m.RunID) {
		return fmt.Errorf("%w: run_id %q", errInvalidManifest, m.RunID)
	}
	if m.ExperimentID == "" || m.Model == "" || m.ArtifactDigest == "" || m.ControllerVersion == "" {
		return fmt.Errorf("%w: missing required fields", errInvalidManifest)
	}
	if m.AdoptedAt.IsZero() {
		return fmt.Errorf("%w: adopted_at is required", errInvalidManifest)
	}
	if len(m.Artifacts) == 0 {
		return fmt.Errorf("%w: empty artifact inventory", errInvalidManifest)
	}
	found := false
	for _, f := range m.Artifacts {
		if f.Path == ManifestName {
			return fmt.Errorf("%w: the manifest must not list itself", errInvalidManifest)
		}
		if !safeRel(f.Path) {
			return fmt.Errorf("%w: unsafe artifact path %q", errInvalidManifest, f.Path)
		}
		if f.Path == "index.html" {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: index.html is required", errInvalidManifest)
	}
	// The sealing contract is single-file and bounded: a manifest claiming
	// anything else was not produced by adopt and cannot be published.
	if len(m.Artifacts) != 1 {
		return fmt.Errorf("%w: the payload must be exactly index.html", errInvalidManifest)
	}
	if m.Artifacts[0].Size > provenance.MaxArtifactBytes {
		return fmt.Errorf("%w: index.html is %d bytes, over the %d byte limit", errInvalidManifest, m.Artifacts[0].Size, provenance.MaxArtifactBytes)
	}
	if m.Review != nil {
		if m.Review.ReviewID == "" || m.Review.VoteCommentID == 0 ||
			(m.Review.Choice != review.ChoiceA && m.Review.Choice != review.ChoiceB) {
			return fmt.Errorf("%w: review evidence is incomplete", errInvalidManifest)
		}
	}
	return nil
}

// validateManifestAgainst checks the manifest against the payload on disk:
// every listed file exists with the recorded size and hash, and no unlisted
// payload file is present.
func validateManifestAgainst(dir string, m *Manifest) error {
	if err := validateManifest(m); err != nil {
		return err
	}
	onDisk, err := provenance.ArtifactPayload(dir)
	if err != nil {
		return err
	}
	if !sameInventory(onDisk, m.Artifacts) {
		return fmt.Errorf("%w: inventory does not match the payload", errInvalidManifest)
	}
	if provenance.PayloadDigest(m.Artifacts) != m.ArtifactDigest {
		return fmt.Errorf("%w: artifact_digest does not match the inventory", errInvalidManifest)
	}
	return nil
}

// sameInventory compares two inventories exactly, including order.
func sameInventory(a, b []provenance.PayloadFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// safeRel mirrors the payload path rules: relative, slash-separated, no
// traversal and no empty segments.
func safeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.ContainsAny(rel, "\\\x00") {
		return false
	}
	if path.IsAbs(rel) || path.Clean(rel) != rel {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// writeStaged writes the payload and manifest to a staging directory, then
// swaps it into place with a backup so an interrupted run stays recoverable.
func writeStaged(intoAbs, runID, sourceDir string, m Manifest) error {
	tmp := filepath.Join(intoAbs, ".adopt-tmp-"+runID)
	bak := filepath.Join(intoAbs, ".adopt-bak-"+runID)
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	for _, f := range m.Artifacts {
		src := filepath.Join(sourceDir, filepath.FromSlash(f.Path))
		body, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if provenance.SHA256Hex(body) != f.SHA256 {
			return fmt.Errorf("adopt: %s changed while copying", f.Path)
		}
		if err := os.WriteFile(filepath.Join(tmp, filepath.FromSlash(f.Path)), body, 0o644); err != nil {
			return err
		}
	}
	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	manifestJSON = append(manifestJSON, '\n')
	if err := os.WriteFile(filepath.Join(tmp, ManifestName), manifestJSON, 0o644); err != nil {
		return err
	}
	if err := syncTree(tmp); err != nil {
		return err
	}
	outputDir := filepath.Join(intoAbs, "output")
	if _, err := os.Lstat(outputDir); err == nil {
		if err := os.Rename(outputDir, bak); err != nil {
			return err
		}
		if err := syncDir(intoAbs); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, outputDir); err != nil {
		return err
	}
	if err := syncDir(intoAbs); err != nil {
		return err
	}
	if err := os.RemoveAll(bak); err != nil {
		return err
	}
	return syncDir(intoAbs)
}

func syncTree(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if err := syncTree(p); err != nil {
				return err
			}
			continue
		}
		f, err := os.Open(p)
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

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func now(opts Options) time.Time {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now().UTC()
}
