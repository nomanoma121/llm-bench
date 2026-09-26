package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
			PromptSHA256: "prompt", ControllerVersion: "test",
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
