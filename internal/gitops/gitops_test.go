package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/run"
)

type fakeGH struct {
	files    map[string]map[string]string // ref -> path -> content
	branches map[string]string            // branch -> source sha
	prs      map[string]string            // head -> "open"|"merged"|"closed"
	baseSHA  string
	events   []string
}

func newFakeGH(baseManifest string) *fakeGH {
	return &fakeGH{
		files: map[string]map[string]string{
			"main": {"apps/inference/values.yaml": baseManifest},
		},
		branches: map[string]string{},
		prs:      map[string]string{},
		baseSHA:  "basesha",
	}
}

func (g *fakeGH) FileAt(_ context.Context, path, ref string) ([]byte, string, error) {
	content, ok := g.files[ref][path]
	if !ok {
		return nil, "", errors.New("not found")
	}
	return []byte(content), "", nil
}

func (g *fakeGH) CommitFile(_ context.Context, path, branch, message string, content []byte) error {
	if g.files[branch] == nil {
		g.files[branch] = map[string]string{}
	}
	g.files[branch][path] = string(content)
	g.events = append(g.events, "commit:"+branch)
	return nil
}

func (g *fakeGH) CreateBranchFrom(_ context.Context, branch, fromSHA string) error {
	g.branches[branch] = fromSHA
	if g.files[branch] == nil {
		g.files[branch] = map[string]string{}
		for p, c := range g.files["main"] {
			g.files[branch][p] = c
		}
	}
	g.events = append(g.events, "branch:"+branch)
	return nil
}

func (g *fakeGH) BaseBranchSHA(_ context.Context) (string, error) { return g.baseSHA, nil }

func (g *fakeGH) PRState(_ context.Context, headBranch string) (string, error) {
	return g.prs[headBranch], nil
}

func (g *fakeGH) CreatePR(_ context.Context, title, headBranch, _ string) error {
	g.prs[headBranch] = "open"
	g.events = append(g.events, "pr:"+headBranch)
	return nil
}

func (g *fakeGH) ClosePR(_ context.Context, headBranch string) error {
	g.prs[headBranch] = "closed"
	g.events = append(g.events, "close:"+headBranch)
	return nil
}

type fakeKube struct {
	synced   bool
	replicas int32
}

func (k *fakeKube) ArgoSynced(_ context.Context, _, _, _ string) (bool, error) { return k.synced, nil }
func (k *fakeKube) ReadyReplicas(_ context.Context, _, _ string) (int32, error) {
	return k.replicas, nil
}

const manifest = `replicaCount: "1"
image:
  tag: latest
`

func plan() operator.GitOpsPlan {
	return operator.GitOpsPlan{
		FilePath:          "apps/inference/values.yaml",
		BaseBranch:        "main",
		YAMLPath:          []string{"replicaCount"},
		ActiveValue:       "1",
		PausedValue:       "0",
		PauseBranch:       "llmbench/pause-abc",
		RestoreBranch:     "llmbench/restore-abc",
		AppNamespace:      "argocd",
		AppName:           "inference",
		WorkloadNamespace: "inference",
		Deployment:        "inference",
		ActiveReplicas:    1,
	}
}

func hookOn(gh *fakeGH, kube *fakeKube) *PauseRestore {
	return &PauseRestore{Plan: plan(), GH: gh, Kube: kube}
}

func TestAcquireFirstTimeCreatesPausePR(t *testing.T) {
	gh := newFakeGH(manifest)
	h := hookOn(gh, &fakeKube{synced: false, replicas: 1})
	err := h.Acquire(context.Background())
	if !errors.Is(err, run.ErrPending) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	if gh.prs["llmbench/pause-abc"] != "open" {
		t.Fatal("pause PR not created")
	}
	updated := gh.files["llmbench/pause-abc"]["apps/inference/values.yaml"]
	if !strings.Contains(updated, `replicaCount: "0"`) {
		t.Fatalf("pause branch manifest not updated:\n%s", updated)
	}
	// Second acquire (PR open) must not create another PR.
	if err := h.Acquire(context.Background()); !errors.Is(err, run.ErrPending) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	if len(gh.events) != 3 { // branch + commit + pr, no duplicates
		t.Fatalf("events = %v", gh.events)
	}
}

func TestAcquireConvergedAfterMerge(t *testing.T) {
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	h := hookOn(gh, &fakeKube{synced: true, replicas: 0})
	if err := h.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire should complete, got %v", err)
	}
}

func TestAcquireConvergeWaitsForArgoAndPods(t *testing.T) {
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	kube := &fakeKube{synced: false, replicas: 1}
	h := hookOn(gh, kube)
	if err := h.Acquire(context.Background()); !errors.Is(err, run.ErrPending) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	kube.synced = true
	if err := h.Acquire(context.Background()); !errors.Is(err, run.ErrPending) {
		t.Fatalf("pods still running: got %v", err)
	}
	kube.replicas = 0
	if err := h.Acquire(context.Background()); err != nil {
		t.Fatalf("converged acquire failed: %v", err)
	}
}

func TestAcquireDriftDetection(t *testing.T) {
	gh := newFakeGH(manifest) // active value
	gh.prs["llmbench/pause-abc"] = "merged"
	h := hookOn(gh, &fakeKube{})
	err := h.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("want drift error, got %v", err)
	}
}

func TestAcquireForeignPauseRejected(t *testing.T) {
	gh := newFakeGH(`replicaCount: "0"`) // paused, but no PR of ours
	h := hookOn(gh, &fakeKube{})
	err := h.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestReleaseRollbackClosesUnmergedPausePR(t *testing.T) {
	gh := newFakeGH(manifest) // never paused
	gh.prs["llmbench/pause-abc"] = "open"
	h := hookOn(gh, &fakeKube{replicas: 1})
	if err := h.Release(context.Background()); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if gh.prs["llmbench/pause-abc"] != "closed" {
		t.Fatal("pause PR not closed")
	}
}

func TestReleaseRestoreCycle(t *testing.T) {
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	kube := &fakeKube{synced: true, replicas: 0}
	h := hookOn(gh, kube)

	// First release creates the restore PR.
	if err := h.Release(context.Background()); !errors.Is(err, run.ErrPending) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	if gh.prs["llmbench/restore-abc"] != "open" {
		t.Fatal("restore PR not created")
	}

	// Human merges it: manifest flips to active.
	gh.files["main"]["apps/inference/values.yaml"] = `replicaCount: "1"`
	gh.prs["llmbench/restore-abc"] = "merged"

	// Convergence: pods must come back.
	if err := h.Release(context.Background()); !errors.Is(err, run.ErrPending) {
		t.Fatalf("want ErrPending while pods restart, got %v", err)
	}
	kube.replicas = 1
	if err := h.Release(context.Background()); err != nil {
		t.Fatalf("release should complete, got %v", err)
	}
}

func TestScalarRoundTrip(t *testing.T) {
	doc := []byte("# lead comment\nreplicaCount: \"1\"\nimage:\n  tag: latest # keep me\n")
	v, err := GetYAMLScalar(doc, []string{"replicaCount"})
	if err != nil || v != "1" {
		t.Fatalf("get = %q err=%v", v, err)
	}
	updated, err := SetYAMLScalar(doc, []string{"replicaCount"}, "0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `replicaCount: "0"`) {
		t.Fatalf("value not set:\n%s", updated)
	}
	if !strings.Contains(string(updated), "# lead comment") || !strings.Contains(string(updated), "# keep me") {
		t.Fatalf("comments lost:\n%s", updated)
	}
	v2, err := GetYAMLScalar(updated, []string{"replicaCount"})
	if err != nil || v2 != "0" {
		t.Fatalf("re-read = %q err=%v", v2, err)
	}
	if _, err := GetYAMLScalar(doc, []string{"missing", "path"}); err == nil {
		t.Fatal("missing path must fail")
	}
}
