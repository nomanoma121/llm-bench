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
	opened   int
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

// prs holds the open pull requests by head branch; opened counts every one.
func (f *fakeManifests) PullRequest(_ context.Context, head string) (*github.PullRequest, error) {
	return f.prs[head], nil
}
func (f *fakeManifests) OpenPullRequest(_ context.Context, head, _, _ string) (int, error) {
	f.opened++
	f.prs[head] = &github.PullRequest{Number: f.opened}
	return f.opened, nil
}
func (f *fakeManifests) ClosePullRequest(_ context.Context, n int) error {
	for head, pr := range f.prs {
		if pr.Number == n {
			delete(f.prs, head)
		}
	}
	return nil
}

func (f *fakeManifests) merge(branch string) {
	f.file = f.branches[branch]
	delete(f.prs, branch)
	delete(f.branches, branch)
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

	// The second run is a rerun of the same job: its branch names are reused.
	for run := 1; run <= 2; run++ {
		for i := 0; i < 2; i++ {
			if done, err := g.Pause(ctx, "j"); done || err != nil {
				t.Fatalf("run %d: pause before merge: %v %v", run, done, err)
			}
		}
		if len(m.prs) != 1 {
			t.Fatalf("run %d: want one pause PR, got %d", run, len(m.prs))
		}
		m.merge("llmbench/pause-j")
		cluster.running = false
		if done, err := g.Pause(ctx, "j"); !done || err != nil {
			t.Fatalf("run %d: pause after merge: %v %v", run, done, err)
		}

		if done, err := g.Restore(ctx, "j"); done || err != nil || m.prs["llmbench/restore-j"] == nil {
			t.Fatalf("run %d: restore before merge: %v %v", run, done, err)
		}
		m.merge("llmbench/restore-j")
		cluster.running = true
		if done, err := g.Restore(ctx, "j"); !done || err != nil {
			t.Fatalf("run %d: restore after merge: %v %v", run, done, err)
		}
	}
	if m.opened != 4 {
		t.Fatalf("want 4 PRs over two runs, got %d", m.opened)
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
	if m.prs["llmbench/pause-j"] != nil || len(m.branches) != 0 {
		t.Fatal("pause PR was not rolled back")
	}
}
