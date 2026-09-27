package gitops

import (
	"context"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/github"
)

type fakeManifests struct {
	file     []byte
	branches map[string][]byte
	prs      map[string]*github.PullRequest
}

func (f *fakeManifests) HeadSHA(context.Context) (string, error) { return "sha", nil }
func (f *fakeManifests) File(context.Context, string, string) ([]byte, error) {
	return f.file, nil
}
func (f *fakeManifests) CreateBranch(_ context.Context, b, _ string) error {
	f.branches[b] = f.file
	return nil
}
func (f *fakeManifests) DeleteBranch(_ context.Context, b string) error {
	delete(f.branches, b)
	return nil
}
func (f *fakeManifests) CommitFile(_ context.Context, b, _, _ string, c []byte) error {
	f.branches[b] = c
	return nil
}
func (f *fakeManifests) PullRequest(_ context.Context, head string) (*github.PullRequest, error) {
	return f.prs[head], nil
}
func (f *fakeManifests) OpenPullRequest(_ context.Context, head, _, _ string) (int, error) {
	f.prs[head] = &github.PullRequest{Number: len(f.prs) + 1, State: "open"}
	return len(f.prs), nil
}
func (f *fakeManifests) ClosePullRequest(_ context.Context, n int) error {
	for _, pr := range f.prs {
		if pr.Number == n {
			pr.State = "closed"
		}
	}
	return nil
}

func (f *fakeManifests) merge(branch string) {
	f.file = f.branches[branch]
	f.prs[branch].State, f.prs[branch].Merged = "closed", true
}

type fakeCluster struct{ running bool }

func (fakeCluster) ArgoSynced(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (c fakeCluster) DeploymentStopped(context.Context, string, string) (bool, error) {
	return !c.running, nil
}
func (c fakeCluster) DeploymentReady(context.Context, string, string, int32) (bool, error) {
	return c.running, nil
}

func TestPauseAndRestore(t *testing.T) {
	ctx := context.Background()
	m := &fakeManifests{
		file:     []byte("spec:\n  replicas: 1\n"),
		branches: map[string][]byte{},
		prs:      map[string]*github.PullRequest{},
	}
	cluster := &fakeCluster{running: true}
	g := &GitOps{
		Target:    Target{File: "d.yaml", Path: []string{"spec", "replicas"}, ActiveValue: "1", PausedValue: "0"},
		Manifests: m,
	}
	g.Cluster = cluster

	for i := 0; i < 2; i++ {
		if done, err := g.Pause(ctx, "j"); done || err != nil {
			t.Fatalf("pause before merge: %v %v", done, err)
		}
	}
	if len(m.prs) != 1 {
		t.Fatalf("want one pause PR, got %d", len(m.prs))
	}
	m.merge("llmbench/pause-j")
	cluster.running = false
	if done, err := g.Pause(ctx, "j"); !done || err != nil {
		t.Fatalf("pause after merge: %v %v", done, err)
	}

	if done, err := g.Restore(ctx, "j"); done || err != nil {
		t.Fatalf("restore before merge: %v %v", done, err)
	}
	m.merge("llmbench/restore-j")
	cluster.running = true
	if done, err := g.Restore(ctx, "j"); !done || err != nil {
		t.Fatalf("restore after merge: %v %v", done, err)
	}
}

func TestRestoreClosesUnmergedPause(t *testing.T) {
	ctx := context.Background()
	m := &fakeManifests{file: []byte("replicas: \"1\"\n"), branches: map[string][]byte{}, prs: map[string]*github.PullRequest{}}
	g := &GitOps{
		Target:    Target{File: "d.yaml", Path: []string{"replicas"}, ActiveValue: "1", PausedValue: "0"},
		Manifests: m,
		Cluster:   fakeCluster{running: true},
	}
	if _, err := g.Pause(ctx, "j"); err != nil {
		t.Fatal(err)
	}
	if done, err := g.Restore(ctx, "j"); !done || err != nil {
		t.Fatalf("restore: %v %v", done, err)
	}
	if m.prs["llmbench/pause-j"].State != "closed" || len(m.branches) != 0 {
		t.Fatal("pause PR was not rolled back")
	}
}
