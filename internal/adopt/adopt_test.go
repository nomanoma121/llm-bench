package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/review"
	"github.com/nomanoma121/llm-bench/internal/run"
)

type fakeStore struct {
	runs map[string]run.Run
	dirs map[string]string
}

func (f *fakeStore) LoadRun(_ context.Context, id string) (run.Run, error) {
	r, ok := f.runs[id]
	if !ok {
		return run.Run{}, run.ErrNotFound
	}
	return r, nil
}

func (f *fakeStore) ArtifactsDir(id string) string { return f.dirs[id] }

const (
	runID = "0123456789abcdef0123456789abcdef"
	model = "example-model"
)

// fixture builds a repository root with one sealed run ready to adopt.
func fixture(t *testing.T, body string) (Options, string) {
	t.Helper()
	root := t.TempDir()
	artifactDir := filepath.Join(root, "runs", runID)
	outputDir := filepath.Join(artifactDir, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := provenance.ArtifactDigest(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := experiment.Config{Model: model, Benchmark: "benchmarks/visual/prompt.md",
		Target: "local", Runtime: experiment.Runtime{Engine: "example", ContextSize: 4096},
		Invoke: experiment.Invoke{Argv: []string{"/bin/true"}}}
	recipeJSON, err := experiment.CanonicalJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		runs: map[string]run.Run{runID: {
			ID: runID, Target: "local", Experiment: "experiments/example-model/exp-1/config.yaml",
			Phase: run.PhaseSucceeded, RecipeJSON: string(recipeJSON),
			Fingerprint: "fingerprint", PromptSHA256: "prompt", ControllerVersion: "test",
			Artifacts: run.Artifacts{Dir: artifactDir, ArtifactDigest: digest},
		}},
		dirs: map[string]string{runID: artifactDir},
	}
	return Options{
		Root: root, RunID: runID, Into: "experiments/example-model/exp-1", Write: true,
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
		Store: store, ArtifactsDir: store.ArtifactsDir,
	}, root
}

func TestAdoptWritesManifestAndPayload(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.NoOp || !res.Replaced {
		t.Fatalf("result = %+v", res)
	}
	dir := filepath.Join(root, "experiments", model, "exp-1", "output")
	if got, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(got) != "<html/>" {
		t.Fatalf("payload = %q", got)
	}
	m := res.Manifest
	if m.RunID != runID || m.Model != model || m.ExperimentID != "exp-1" ||
		m.SchemaVersion != SchemaVersion || m.Review != nil || len(m.Artifacts) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if m.ArtifactDigest != provenance.PayloadDigest(m.Artifacts) {
		t.Fatal("manifest digest does not match its inventory")
	}
	if err := Verify(filepath.Join(root, "experiments")); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// The staging areas and the lock file are not payload.
	entries, err := os.ReadDir(filepath.Join(root, "experiments", model, "exp-1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "output" && e.Name() != lockName {
			t.Fatalf("unexpected entry %q", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "experiments", model, "exp-1", lockName)); err != nil {
		t.Fatal("the lock file is created once and kept")
	}
}

func TestAdoptIsIdempotentAndRefusesDifferentDigest(t *testing.T) {
	opts, _ := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NoOp {
		t.Fatalf("re-adopting identical bytes must be a no-op: %+v", res)
	}

	// Same run, different bytes in the source: refused before writing.
	changed, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(root, "runs", runID, "output")
	if err := os.WriteFile(filepath.Join(outputDir, "index.html"), []byte("<html>changed</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), changed); err == nil {
		t.Fatal("expected the changed artifact to be refused")
	}
}

func TestAdoptRefusesTamperedArtifact(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	if err := os.WriteFile(filepath.Join(root, "runs", runID, "output", "index.html"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("expected a digest mismatch")
	}
}

func TestAdoptRequiresReviewOnceAModelHasAnAdoption(t *testing.T) {
	opts, _ := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	// A second experiment for the same model needs a review, even though its
	// own destination has no manifest yet.
	second, root := fixture(t, "<html/>")
	second.Root = opts.Root
	second.Into = "experiments/example-model/exp-2"
	store := second.Store.(*fakeStore)
	r := store.runs[runID]
	r.Experiment = "experiments/example-model/exp-2/config.yaml"
	store.runs[runID] = r
	_ = root
	if _, err := Run(context.Background(), second); err == nil {
		t.Fatal("expected --review to be required")
	}
}

func TestAdoptWithReviewVerifiesRunAndDigest(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	if err := os.MkdirAll(filepath.Join(root, "experiments", model, "exp-0", "output"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := provenance.ArtifactDigest(filepath.Join(root, "runs", runID, "output"))
	if err != nil {
		t.Fatal(err)
	}
	opts.ReviewID = "rev1"
	opts.Decision = func(context.Context, string) (review.Decision, error) {
		return review.Decision{
			ReviewID: "rev1", Issue: 7, IssueURL: "https://example.test/issues/7",
			Choice: review.ChoiceB, CommentID: 110,
			Baseline:  review.ReviewedRun{RunID: "other", Digest: "other-digest"},
			Candidate: review.ReviewedRun{RunID: runID, Digest: digest},
		}, nil
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.Review == nil || res.Manifest.Review.VoteCommentID != 110 ||
		res.Manifest.Review.Choice != review.ChoiceB || res.Manifest.Review.IssueURL == "" {
		t.Fatalf("review evidence = %+v", res.Manifest.Review)
	}

	// A decision for another run, another digest, or no decision at all is
	// refused before anything is written.
	bad := []review.Decision{
		// A selects the baseline; here that is another run, not this one.
		{ReviewID: "rev1", Choice: review.ChoiceA, CommentID: 5,
			Baseline: review.ReviewedRun{RunID: "other", Digest: digest}, Candidate: review.ReviewedRun{RunID: runID, Digest: "y"}},
		{ReviewID: "rev1", Choice: review.ChoiceB, CommentID: 5,
			Baseline: review.ReviewedRun{RunID: "x", Digest: "y"}, Candidate: review.ReviewedRun{RunID: runID, Digest: "wrong"}},
		{ReviewID: "rev1", Choice: review.ChoiceTie, CommentID: 5,
			Baseline: review.ReviewedRun{RunID: runID, Digest: digest}, Candidate: review.ReviewedRun{RunID: "x", Digest: "y"}},
	}
	for i, d := range bad {
		o, _ := fixture(t, "<html/>")
		o.ReviewID = "rev1"
		o.Decision = func(context.Context, string) (review.Decision, error) { return d, nil }
		if _, err := Run(context.Background(), o); err == nil {
			t.Fatalf("case %d: expected refusal", i)
		}
	}
}

func TestAdoptConfinement(t *testing.T) {
	for _, into := range []string{
		"/etc/experiments/x/y",
		"experiments/../etc/x",
		"experiments/model",
		"experiments/model/exp/extra",
		"other/model/exp",
		"experiments/model/./exp",
		`experiments\model\exp`,
	} {
		opts, _ := fixture(t, "<html/>")
		opts.Into = into
		if _, err := Run(context.Background(), opts); err == nil {
			t.Fatalf("%q: expected refusal", into)
		}
	}

	// A symlinked model directory must not be followed out of the repository.
	opts, root := fixture(t, "<html/>")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "experiments", model)); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("expected the symlinked destination to be refused")
	}
}

func TestAdoptMustMatchTheRunsExperimentDirectory(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	// Same model, different experiment directory: the recipe would not match
	// the destination.
	if err := os.MkdirAll(filepath.Join(root, "experiments", model, "exp-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts.Into = "experiments/example-model/exp-2"
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("expected the experiment/destination mismatch to be refused")
	}
}

func TestAdoptRejectsModelMismatch(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	store := opts.Store.(*fakeStore)
	r := store.runs[runID]
	r.Experiment = "experiments/other-model/exp-1/config.yaml"
	store.runs[runID] = r
	_ = root
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("expected the model mismatch to be refused")
	}
}

func TestAdoptRefusesUnsealedOrLegacyRuns(t *testing.T) {
	cases := map[string]func(*run.Run){
		"unsealed":     func(r *run.Run) { r.Artifacts.ArtifactDigest = "" },
		"legacy":       func(r *run.Run) { r.ControllerVersion = "" },
		"not finished": func(r *run.Run) { r.Phase = run.PhaseRunning },
	}
	for name, mutate := range cases {
		opts, _ := fixture(t, "<html/>")
		store := opts.Store.(*fakeStore)
		r := store.runs[runID]
		mutate(&r)
		store.runs[runID] = r
		if _, err := Run(context.Background(), opts); err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
	}
}

func TestAdoptDryRunWritesNothing(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	opts.Write = false
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "experiments", model, "exp-1", "output")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run must not write: %v", err)
	}
}

func TestAdoptReplacesAPlaceholder(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	placeholder := filepath.Join(root, "experiments", model, "exp-1", "output")
	if err := os.MkdirAll(placeholder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(placeholder, "index.html"), []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced {
		t.Fatalf("expected a replacement: %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(placeholder, "index.html")); string(got) != "<html/>" {
		t.Fatalf("payload = %q", got)
	}
}

func TestAdoptPreservesSiblingFiles(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	expDir := filepath.Join(root, "experiments", model, "exp-1")
	if err := os.MkdirAll(expDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(expDir, "config.yaml"), []byte("model: example-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(expDir, "config.yaml")); err != nil {
		t.Fatal("config.yaml must be preserved")
	}
}

// TestAdoptRecoversInterruptedSwaps walks the recovery decision table.
func TestAdoptRecoversInterruptedSwaps(t *testing.T) {
	// tmp complete, no output, no backup: finish the swap.
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expDir := filepath.Join(root, "experiments", model, "exp-1")
	tmp := filepath.Join(expDir, ".adopt-tmp-"+runID)
	if err := os.Rename(filepath.Join(expDir, "output"), tmp); err != nil {
		t.Fatal(err)
	}
	recovered, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.NoOp {
		t.Fatalf("expected the completed swap to be recognized: %+v", recovered)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging directory must be consumed")
	}

	// Backup only: restore it.
	opts2, root2 := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts2); err != nil {
		t.Fatal(err)
	}
	expDir2 := filepath.Join(root2, "experiments", model, "exp-1")
	bak := filepath.Join(expDir2, ".adopt-bak-"+runID)
	if err := os.Rename(filepath.Join(expDir2, "output"), bak); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts2); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expDir2, "output", ManifestName)); err != nil {
		t.Fatal("the backup must be restored")
	}
	if _, err := os.Stat(bak); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the backup must be consumed")
	}

	// A broken staging directory is discarded.
	opts3, root3 := fixture(t, "<html/>")
	if err := os.MkdirAll(filepath.Join(root3, "experiments", model, "exp-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root3, "experiments", model, "exp-1", ".adopt-tmp-"+runID)
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "index.html"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts3); err != nil {
		t.Fatalf("expected the broken staging to be discarded: %v", err)
	}
	if _, err := os.Stat(broken); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a broken staging directory must be discarded")
	}

	// Two staging directories: fail closed.
	opts4, root4 := fixture(t, "<html/>")
	expDir4 := filepath.Join(root4, "experiments", model, "exp-1")
	for _, name := range []string{".adopt-tmp-a", ".adopt-tmp-b"} {
		if err := os.MkdirAll(filepath.Join(expDir4, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Run(context.Background(), opts4); err == nil {
		t.Fatal("expected ambiguous staging directories to fail closed")
	}
}

func TestVerifyRejectsInventoryAndPlacementProblems(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expRoot := filepath.Join(root, "experiments")
	if err := Verify(expRoot); err != nil {
		t.Fatal(err)
	}

	// An extra payload file is not in the inventory.
	dir := filepath.Join(expRoot, model, "exp-1", "output")
	if err := os.WriteFile(filepath.Join(dir, "extra.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(expRoot); err == nil {
		t.Fatal("expected the extra payload file to be reported")
	}
	if err := os.Remove(filepath.Join(dir, "extra.js")); err != nil {
		t.Fatal(err)
	}

	// Tampering with the payload breaks the digest.
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(expRoot); err == nil {
		t.Fatal("expected the tampered payload to be reported")
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A manifest in the wrong directory is rejected.
	wrong := filepath.Join(expRoot, model, "exp-9", "output")
	if err := os.MkdirAll(wrong, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrong, "index.html"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrong, ManifestName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(expRoot); err == nil {
		t.Fatal("expected the misplaced manifest to be reported")
	}
}

func TestVerifyRejectsTwoReviewLessAdoptionsPerModel(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expRoot := filepath.Join(root, "experiments")
	// Copy the adopted directory to a second experiment of the same model,
	// as two parallel pull requests would.
	src := filepath.Join(expRoot, model, "exp-1", "output")
	dst := filepath.Join(expRoot, model, "exp-2", "output")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", ManifestName} {
		body, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Rewrite the copy's experiment_id so only the global invariant fires.
	m, err := readManifestFile(filepath.Join(dst, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	m.ExperimentID = "exp-2"
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, ManifestName), append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Verify(expRoot)
	if err == nil || !contains(err.Error(), "review-less") {
		t.Fatalf("expected the review-less invariant to fire: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func TestDryRunTouchesNothing(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	opts.Write = false
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expDir := filepath.Join(root, "experiments", model, "exp-1")
	if _, err := os.Stat(expDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created the destination: %v", err)
	}

	// An interrupted swap is left untouched by a dry run.
	opts2, root2 := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts2); err != nil {
		t.Fatal(err)
	}
	expDir2 := filepath.Join(root2, "experiments", model, "exp-1")
	tmp := filepath.Join(expDir2, ".adopt-tmp-"+runID)
	if err := os.Rename(filepath.Join(expDir2, "output"), tmp); err != nil {
		t.Fatal(err)
	}
	// The dry run projects the recovery table: a complete staging directory
	// means the swap would finish, so the same adoption is reported as no-op.
	opts2.Write = false
	res, err := Run(context.Background(), opts2)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NoOp {
		t.Fatalf("dry run should project the completed swap: %+v", res)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("dry run must not recover the staging directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expDir2, "output")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dry run must not move the staging directory into place")
	}
}

func TestInvalidExistingManifestIsAHardError(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "experiments", model, "exp-1", "output", ManifestName)
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// Break the inventory: the no-op path must not accept this manifest.
	broken := strings.Replace(string(body), `"prompt_sha256": "prompt"`, `"prompt_sha256": ""`, 1)
	if err := os.WriteFile(manifestPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("expected an invalid existing manifest to fail closed")
	}
	if err := Verify(filepath.Join(root, "experiments")); err == nil {
		t.Fatal("expected verify to report the invalid manifest")
	}
}

func TestVerifyRejectsSymlinkedModelDirectory(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expRoot := filepath.Join(root, "experiments")
	moved := filepath.Join(root, "moved-model")
	if err := os.Rename(filepath.Join(expRoot, model), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, filepath.Join(expRoot, model)); err != nil {
		t.Fatal(err)
	}
	err := Verify(expRoot)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected a symlink rejection, got %v", err)
	}
}

func TestManifestRequiresProvenanceFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := func() *Manifest {
		return &Manifest{
			SchemaVersion: SchemaVersion, RunID: runID, ExperimentID: "exp-1", Model: model,
			BenchmarkFingerprint: "fp", ArtifactDigest: "d", PromptSHA256: "p",
			ControllerVersion: "test", AdoptedAt: time.Unix(0, 0).UTC(),
			Artifacts: []provenance.PayloadFile{{Path: "index.html", Size: 1, SHA256: provenance.SHA256Hex([]byte("x"))}},
		}
	}
	for name, mutate := range map[string]func(*Manifest){
		"fingerprint": func(m *Manifest) { m.BenchmarkFingerprint = "" },
		"prompt":      func(m *Manifest) { m.PromptSHA256 = "" },
		"review url":  func(m *Manifest) { m.Review = &ReviewRef{ReviewID: "r", VoteCommentID: 1, Choice: review.ChoiceA} },
	} {
		m := base()
		mutate(m)
		if err := validateManifest(m); err == nil {
			t.Fatalf("%s: expected validation to fail", name)
		}
	}
}

func TestVerifyRejectsSymlinkedExperimentsRoot(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expRoot := filepath.Join(root, "experiments")
	moved := filepath.Join(root, "moved-experiments")
	if err := os.Rename(expRoot, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, expRoot); err != nil {
		t.Fatal(err)
	}
	if err := Verify(expRoot); err == nil {
		t.Fatal("expected a symlinked experiments root to be rejected")
	}
	if _, err := Find(expRoot); err == nil {
		t.Fatal("expected the site finder to reject a symlinked experiments root")
	}
}

func TestDryRunProjectsTheRecoveryTable(t *testing.T) {
	// output + broken staging: output wins and the staging is ignored, so the
	// dry run reports the same no-op a write would produce.
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expDir := filepath.Join(root, "experiments", model, "exp-1")
	broken := filepath.Join(expDir, ".adopt-tmp-"+runID)
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	opts.Write = false
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NoOp {
		t.Fatalf("output stays authoritative: %+v", res)
	}
	if _, err := os.Stat(broken); err != nil {
		t.Fatal("a dry run must not clean up")
	}

	// Multiple staging directories fail closed the same way in both modes.
	if err := os.MkdirAll(filepath.Join(expDir, ".adopt-tmp-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if os.Rename(filepath.Join(expDir, "output"), filepath.Join(expDir, ".adopt-bak-"+runID)) != nil {
		t.Fatal("setup")
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("expected ambiguous staging directories to fail closed in dry-run too")
	}
}

func TestManifestWithTrailingDataIsRejected(t *testing.T) {
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "experiments", model, "exp-1", "output", ManifestName)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte("\n{\"extra\":true}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(filepath.Join(root, "experiments")); err == nil {
		t.Fatal("expected trailing data to be rejected")
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("adopt must not treat a manifest with trailing data as valid")
	}
}

func TestAdoptRefusesAManifestCIWouldReject(t *testing.T) {
	// An incomplete run record cannot produce a valid manifest; the swap must
	// not happen, and an existing adoption must stay intact.
	first, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "experiments", model, "exp-1", "output", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	second, _ := fixture(t, "<html/>")
	second.Root = first.Root
	second.Into = "experiments/example-model/exp-2"
	store := second.Store.(*fakeStore)
	r := store.runs[runID]
	r.Experiment = "experiments/example-model/exp-2/config.yaml"
	r.Fingerprint = ""
	store.runs[runID] = r
	if _, err := Run(context.Background(), second); err == nil {
		t.Fatal("expected the missing fingerprint to be refused")
	}
	if _, err := os.Stat(filepath.Join(root, "experiments", model, "exp-2", "output")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no payload may be written for an invalid manifest: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "experiments", model, "exp-1", "output", "index.html")); string(got) != string(payload) {
		t.Fatal("the existing adoption must stay intact")
	}
}

func TestDryRunAndWriteAgreeOnProjectedRecovery(t *testing.T) {
	// A completed staging directory from run A means the model already has an
	// adoption: both dry-run and write must require --review for a different
	// run with identical bytes.
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expDir := filepath.Join(root, "experiments", model, "exp-1")
	if err := os.Rename(filepath.Join(expDir, "output"), filepath.Join(expDir, ".adopt-tmp-"+runID)); err != nil {
		t.Fatal(err)
	}
	dr := opts
	dr.Write = false
	dry, err := Run(context.Background(), dr)
	if err != nil {
		t.Fatal(err)
	}
	if !dry.NoOp {
		t.Fatalf("dry run must project the completed swap: %+v", dry)
	}
	written, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !written.NoOp {
		t.Fatalf("write must reach the same conclusion: %+v", written)
	}
}

func TestDryRunAuthorizationUsesTheProjectedState(t *testing.T) {
	// Run A is complete but still in staging; adopting a *different* run B with
	// identical bytes must require a review in both modes, because the
	// projection shows the model already has an adoption.
	opts, root := fixture(t, "<html/>")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	expDir := filepath.Join(root, "experiments", model, "exp-1")
	payload, err := os.ReadFile(filepath.Join(expDir, "output", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(expDir, "output"), filepath.Join(expDir, ".adopt-tmp-"+runID)); err != nil {
		t.Fatal(err)
	}
	// A second run with the same bytes and the same model but another
	// experiment directory.
	const runB = "fedcba9876543210fedcba9876543210"
	other, _ := fixture(t, string(payload))
	other.Root = root
	other.RunID = runB
	other.Into = "experiments/example-model/exp-2"
	store := other.Store.(*fakeStore)
	rb := store.runs[runID]
	rb.ID = runB
	rb.Experiment = "experiments/example-model/exp-2/config.yaml"
	store.runs = map[string]run.Run{runB: rb}
	store.dirs = map[string]string{runB: store.dirs[runID]}
	// Recompute the recorded digest for the copied payload.
	digest, err := provenance.ArtifactDigest(filepath.Join(store.dirs[runB], "output"))
	if err != nil {
		t.Fatal(err)
	}
	rb.Artifacts.ArtifactDigest = digest
	store.runs[runB] = rb

	other.Write = false
	if _, err := Run(context.Background(), other); err == nil {
		t.Fatal("dry run must require a review once the projection shows an adoption")
	}
	if _, err := Run(context.Background(), other); err == nil {
		t.Fatal("write must require a review in the same state")
	}
}
