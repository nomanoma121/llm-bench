package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/operator"
)

type fakeGH struct {
	files    map[string]map[string]string // ref -> path -> content
	branches map[string]string            // branch -> source sha
	prs      map[string]string            // head -> "open"|"merged"|"closed"
	baseSHA  string
	events   []string
	// onFileAt is invoked on every read (test hook for race scenarios).
	onFileAt func()
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
	if ref == g.baseSHA {
		ref = "main" // the pinned revision resolves to the base content
	}
	if g.onFileAt != nil {
		g.onFileAt()
	}
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

func (g *fakeGH) DeleteBranch(_ context.Context, branch string) error {
	delete(g.branches, branch)
	delete(g.files, branch)
	g.events = append(g.events, "delete-branch:"+branch)
	return nil
}

type fakeKube struct {
	synced   bool
	stopped  bool
	ready    bool
	replicas int32
	// revisions records every revision ArgoSynced was asked about.
	revisions []string
}

func (k *fakeKube) ArgoSynced(_ context.Context, _, _, rev string) (bool, error) {
	k.revisions = append(k.revisions, rev)
	return k.synced, nil
}

func (k *fakeKube) WorkloadStopped(_ context.Context, _, _ string) (bool, error) {
	return k.stopped, nil
}

func (k *fakeKube) WorkloadReady(_ context.Context, _, _ string, _ int32) (bool, error) {
	return k.ready, nil
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
	h := hookOn(gh, &fakeKube{synced: false, stopped: false})
	err := h.Acquire(context.Background())
	if !errors.Is(err, ErrNotConverged) {
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
	if err := h.Acquire(context.Background()); !errors.Is(err, ErrNotConverged) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	if len(gh.events) != 3 { // branch + commit + pr, no duplicates
		t.Fatalf("events = %v", gh.events)
	}
}

func TestConvergenceUsesTheDecisionRevision(t *testing.T) {
	// The base branch moves between the snapshot and the Argo check: Argo
	// must be asked about the revision the decision was based on.
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	gh.baseSHA = "rev-A"
	kube := &fakeKube{synced: true, stopped: true}
	h := hookOn(gh, kube)
	gh.onFileAt = func() { gh.baseSHA = "rev-B" } // the branch moves after the read
	if err := h.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire should use the decision revision: %v", err)
	}
	for _, rev := range kube.revisions {
		if rev != "rev-A" {
			t.Fatalf("Argo was asked about %q, want the decision revision rev-A", rev)
		}
	}
}

func TestReleaseConvergenceUsesTheDecisionRevision(t *testing.T) {
	gh := newFakeGH(manifest)
	gh.prs["llmbench/pause-abc"] = "merged"
	gh.prs["llmbench/restore-abc"] = "merged"
	gh.baseSHA = "rev-A"
	kube := &fakeKube{synced: true, ready: true}
	h := hookOn(gh, kube)
	gh.onFileAt = func() { gh.baseSHA = "rev-B" }
	if err := h.Release(context.Background()); err != nil {
		t.Fatalf("release should use the decision revision: %v", err)
	}
	for _, rev := range kube.revisions {
		if rev != "rev-A" {
			t.Fatalf("Argo was asked about %q, want the decision revision rev-A", rev)
		}
	}
}

func TestAcquireConvergedAfterMerge(t *testing.T) {
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	h := hookOn(gh, &fakeKube{synced: true, stopped: true})
	if err := h.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire should complete, got %v", err)
	}
}

func TestAcquireConvergeWaitsForArgoAndPods(t *testing.T) {
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	kube := &fakeKube{synced: false, stopped: false}
	h := hookOn(gh, kube)
	if err := h.Acquire(context.Background()); !errors.Is(err, ErrNotConverged) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	kube.synced = true
	if err := h.Acquire(context.Background()); !errors.Is(err, ErrNotConverged) {
		t.Fatalf("pods still running: got %v", err)
	}
	kube.stopped = true
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
	h := hookOn(gh, &fakeKube{stopped: false, ready: true})
	if err := h.Release(context.Background()); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if gh.prs["llmbench/pause-abc"] != "closed" {
		t.Fatal("pause PR not closed")
	}
	if _, ok := gh.branches["llmbench/pause-abc"]; ok {
		t.Fatal("rollback must delete the pause branch")
	}
}

func TestReleaseRestoreCycle(t *testing.T) {
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	kube := &fakeKube{synced: true, stopped: true, ready: false}
	h := hookOn(gh, kube)

	// First release creates the restore PR.
	if err := h.Release(context.Background()); !errors.Is(err, ErrNotConverged) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	if gh.prs["llmbench/restore-abc"] != "open" {
		t.Fatal("restore PR not created")
	}

	// Human merges it: manifest flips to active.
	gh.files["main"]["apps/inference/values.yaml"] = `replicaCount: "1"`
	gh.prs["llmbench/restore-abc"] = "merged"

	// Convergence: pods must come back.
	if err := h.Release(context.Background()); !errors.Is(err, ErrNotConverged) {
		t.Fatalf("want ErrPending while pods restart, got %v", err)
	}
	kube.ready = true
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

func TestReleaseRefusesForeignPause(t *testing.T) {
	// Paused, but this run never merged a pause PR: not ours to restore.
	gh := newFakeGH(`replicaCount: "0"`)
	h := hookOn(gh, &fakeKube{synced: true, stopped: true})
	err := h.Release(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not create") {
		t.Fatalf("foreign pause must be refused, got %v", err)
	}
	if gh.prs["llmbench/restore-abc"] == "open" {
		t.Fatal("no restore PR may be opened for a foreign pause")
	}
}

func TestReleaseRefusesForeignRestore(t *testing.T) {
	// Our pause merged, the manifest is active again without our restore PR:
	// someone else brought the workload back.
	gh := newFakeGH(manifest)
	gh.prs["llmbench/pause-abc"] = "merged"
	h := hookOn(gh, &fakeKube{synced: true, ready: true})
	err := h.Release(context.Background())
	if err == nil || !strings.Contains(err.Error(), "foreign change") {
		t.Fatalf("foreign restore must be refused, got %v", err)
	}
}

func TestReleaseDetectsRestoreDrift(t *testing.T) {
	// Our restore PR merged but the manifest still shows paused.
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	gh.prs["llmbench/restore-abc"] = "merged"
	h := hookOn(gh, &fakeKube{synced: true, stopped: true})
	err := h.Release(context.Background())
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("restore drift must be permanent, got %v", err)
	}
}

func TestAcquireReReadsBeforeDeclaringDrift(t *testing.T) {
	// The pause merge is observed while the pinned revision still shows the
	// active value; the re-read sees the merged value and stays pending
	// instead of declaring drift.
	gh := newFakeGH(manifest)
	gh.prs["llmbench/pause-abc"] = "merged"
	count := 0
	gh.onFileAt = func() {
		count++
		if count == 2 { // the re-read
			gh.files["main"]["apps/inference/values.yaml"] = `replicaCount: "0"`
		}
	}
	h := hookOn(gh, &fakeKube{synced: true, stopped: true})
	if err := h.Acquire(context.Background()); !errors.Is(err, ErrNotConverged) {
		t.Fatalf("want pending after the merge lands, got %v", err)
	}
}

func TestOpenPausePRRefusesUnexpectedValue(t *testing.T) {
	gh := newFakeGH(`replicaCount: "7"`)
	h := hookOn(gh, &fakeKube{})
	err := h.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected value") {
		t.Fatalf("unexpected value must be refused before writing, got %v", err)
	}
	if gh.prs["llmbench/pause-abc"] == "open" {
		t.Fatal("no PR may be opened for an unexpected value")
	}
}

func TestReleaseReReadsBeforeDeclaringRestoreDrift(t *testing.T) {
	// The restore merge is observed while the pinned revision still shows
	// paused; the re-read sees the active value and stays pending.
	gh := newFakeGH(`replicaCount: "0"`)
	gh.prs["llmbench/pause-abc"] = "merged"
	gh.prs["llmbench/restore-abc"] = "merged"
	count := 0
	gh.onFileAt = func() {
		count++
		if count == 2 { // the re-read
			gh.files["main"]["apps/inference/values.yaml"] = `replicaCount: "1"`
		}
	}
	h := hookOn(gh, &fakeKube{synced: true, stopped: true, ready: false})
	if err := h.Release(context.Background()); !errors.Is(err, ErrNotConverged) {
		t.Fatalf("want pending after the restore merge lands, got %v", err)
	}
}

func TestScalarRejectsNonStringTypes(t *testing.T) {
	// A numeric scalar must be refused, not silently rewritten as a string.
	doc := []byte("replicaCount: 1\n")
	if _, err := GetYAMLScalar(doc, []string{"replicaCount"}); err == nil || !strings.Contains(err.Error(), "string scalar") {
		t.Fatalf("numeric scalar accepted: %v", err)
	}
	// Quoted values are the supported form and keep their type on write.
	quoted := []byte("replicaCount: \"1\"\n")
	v, err := GetYAMLScalar(quoted, []string{"replicaCount"})
	if err != nil || v != "1" {
		t.Fatalf("quoted scalar: %q err=%v", v, err)
	}
	updated, err := SetYAMLScalar(quoted, []string{"replicaCount"}, "0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `replicaCount: "0"`) {
		t.Fatalf("string type lost on write:\n%s", updated)
	}
	if _, err := GetYAMLScalar([]byte("replicas: true\n"), []string{"replicas"}); err == nil {
		t.Fatal("boolean scalar accepted")
	}
}
