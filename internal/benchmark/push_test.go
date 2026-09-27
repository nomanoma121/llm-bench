package benchmark

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo creates a repository with one commit and a bare remote, which is
// enough to exercise branch, commit and push without a network.
func gitRepo(t *testing.T) (root, remote string) {
	t.Helper()
	remote = filepath.Join(t.TempDir(), "remote.git")
	run(t, "", "git", "init", "--bare", "--initial-branch=main", remote)
	root = filepath.Join(t.TempDir(), "work")
	run(t, "", "git", "init", "--initial-branch=main", root)
	run(t, root, "git", "config", "user.name", "test")
	run(t, root, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "README.md")
	run(t, root, "git", "commit", "-m", "initial")
	run(t, root, "git", "remote", "add", "origin", remote)
	run(t, root, "git", "push", "origin", "main")
	return root, remote
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
	return string(out)
}

func TestPushCreatesABranchAndNeverForces(t *testing.T) {
	root, remote := gitRepo(t)
	resultDir := filepath.Join(root, "experiments", "m", "job-1")
	if err := os.MkdirAll(resultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultDir, "result.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Push(context.Background(), PushOptions{
		RepoRoot: root,
		Paths:    []string{"experiments"},
		Branch:   "llmbench/job-1",
		Message:  "benchmark: m",
		// The local remote does not authenticate; the header is still sent and
		// must not leak anywhere.
		Token:  "ghs_secrettoken",
		Remote: "origin",
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if got.Branch != "llmbench/job-1" || got.Changed != 1 || len(got.Commit) != 40 {
		t.Fatalf("result = %+v", got)
	}
	// The branch exists on the remote and carries the change.
	if out := run(t, remote, "git", "rev-parse", "--verify", "refs/heads/llmbench/job-1"); !strings.Contains(out, got.Commit) {
		t.Fatalf("remote branch = %s, want %s", out, got.Commit)
	}
	// main is untouched.
	if out := run(t, remote, "git", "rev-parse", "refs/heads/main"); !strings.Contains(out, got.Base) {
		t.Fatalf("main moved: %s, want %s", out, got.Base)
	}
	// The token is nowhere in the repository configuration.
	cfg, err := os.ReadFile(filepath.Join(root, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "ghs_secrettoken") {
		t.Fatal("the token was written to .git/config")
	}
	// Pushing the same branch again fails: the job must not overwrite.
	if _, err := Push(context.Background(), PushOptions{
		RepoRoot: root, Paths: []string{"experiments"}, Branch: "llmbench/job-1", Message: "again", Token: "t",
	}); err == nil {
		t.Fatal("a second push to the same branch succeeded")
	}
}

func TestPushRefusesProtectedBranches(t *testing.T) {
	root, _ := gitRepo(t)
	for _, branch := range []string{"main", "master", "trunk"} {
		_, err := Push(context.Background(), PushOptions{
			RepoRoot: root, Paths: []string{"."}, Branch: branch, Token: "t",
		})
		if err == nil || !strings.Contains(err.Error(), "protected branch") {
			t.Fatalf("branch %s: %v", branch, err)
		}
	}
}

func TestPushRequiresATokenAndSomethingToCommit(t *testing.T) {
	root, _ := gitRepo(t)
	if _, err := Push(context.Background(), PushOptions{RepoRoot: root, Paths: []string{"."}, Branch: "b"}); err == nil {
		t.Fatal("a push without a token succeeded")
	}
	// Nothing staged: the command creates the branch but must not create an
	// empty commit.
	_, err := Push(context.Background(), PushOptions{
		RepoRoot: root, Paths: []string{"README.md"}, Branch: "b", Token: "t",
	})
	if err == nil || !strings.Contains(err.Error(), "nothing to commit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPushRedactsTheTokenFromErrors(t *testing.T) {
	root, _ := gitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A remote that cannot be reached: the error must not carry the token.
	_, err := Push(context.Background(), PushOptions{
		RepoRoot: root, Paths: []string{"new.txt"}, Branch: "b",
		Token: "ghs_secrettoken", Remote: filepath.Join(t.TempDir(), "does-not-exist.git"),
	})
	if err == nil {
		t.Fatal("expected the push to fail")
	}
	if strings.Contains(err.Error(), "ghs_secrettoken") {
		t.Fatalf("the token leaked into %v", err)
	}
}
