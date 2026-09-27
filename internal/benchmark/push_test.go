package benchmark

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testToken is realistic in shape: redaction replaces the whole token, and a
// one-character token would mangle every other string it appears in.
const testToken = "ghs_012345678901234567890123456789012345"

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
		Token:  testToken,
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
	if strings.Contains(string(cfg), testToken) {
		t.Fatal("the token was written to .git/config")
	}
	// Pushing the same branch again fails: the job must not overwrite.
	if _, err := Push(context.Background(), PushOptions{
		RepoRoot: root, Paths: []string{"experiments"}, Branch: "llmbench/job-1", Message: "again", Token: testToken,
	}); err == nil {
		t.Fatal("a second push to the same branch succeeded")
	}
}

func TestPushRefusesProtectedBranches(t *testing.T) {
	root, _ := gitRepo(t)
	for _, branch := range []string{"main", "master", "trunk"} {
		_, err := Push(context.Background(), PushOptions{
			RepoRoot: root, Paths: []string{"."}, Branch: branch, Token: testToken, Message: "m",
		})
		if err == nil || !strings.Contains(err.Error(), "protected branch") {
			t.Fatalf("branch %s: %v", branch, err)
		}
	}
}

func TestPushRequiresATokenAndSomethingToCommit(t *testing.T) {
	root, _ := gitRepo(t)
	if _, err := Push(context.Background(), PushOptions{RepoRoot: root, Paths: []string{"."}, Branch: "b", Message: "m"}); err == nil {
		t.Fatal("a push without a token succeeded")
	}
	if _, err := Push(context.Background(), PushOptions{RepoRoot: root, Paths: []string{"."}, Branch: "b", Token: testToken}); err == nil {
		t.Fatal("a push without a commit message succeeded")
	}
	// Nothing staged: the command creates the branch but must not create an
	// empty commit.
	_, err := Push(context.Background(), PushOptions{
		RepoRoot: root, Paths: []string{"README.md"}, Branch: "b", Token: testToken, Message: "m",
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
		RepoRoot: root, Paths: []string{"new.txt"}, Branch: "b", Message: "m",
		Token: testToken, Remote: filepath.Join(t.TempDir(), "does-not-exist.git"),
	})
	if err == nil {
		t.Fatal("expected the push to fail")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the token leaked into %v", err)
	}
}

func TestPushRefusesAnExistingRemoteBranch(t *testing.T) {
	root, remote := gitRepo(t)
	// A branch with the same name already exists on the remote: another run
	// owns it, and overwriting it would need a force push.
	run(t, root, "git", "checkout", "-b", "llmbench/job-1")
	if err := os.WriteFile(filepath.Join(root, "other.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "other.txt")
	run(t, root, "git", "commit", "-m", "other")
	run(t, root, "git", "push", "origin", "llmbench/job-1")
	run(t, root, "git", "checkout", "main")
	// The local branch is gone, but the remote one is not: the push must
	// refuse before it starts.
	run(t, root, "git", "branch", "-D", "llmbench/job-1")
	run(t, root, "git", "fetch", "origin")
	if _, err := os.Stat(remote); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "experiments", "r.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Push(context.Background(), PushOptions{
		RepoRoot: root, Paths: []string{"experiments"}, Branch: "llmbench/job-1", Token: testToken, Message: "m", Remote: "origin",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists on") {
		t.Fatalf("error = %v", err)
	}
}

func TestPushResolvesTheRemoteDefaultBranch(t *testing.T) {
	root, remote := gitRepo(t)
	// The repository's default branch is "develop", which the fixed deny-list
	// does not contain: the remote has to be asked.
	run(t, root, "git", "checkout", "-b", "develop")
	run(t, root, "git", "push", "origin", "develop")
	run(t, root, "git", "checkout", "main")
	run(t, root, "git", "branch", "-D", "develop")
	run(t, "", "git", "-C", remote, "symbolic-ref", "HEAD", "refs/heads/develop")
	run(t, root, "git", "remote", "set-head", "origin", "-a")

	o := PushOptions{RepoRoot: root, Remote: "origin"}
	got, err := o.defaultBranch(context.Background(), "origin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "develop" {
		t.Fatalf("default branch = %q, want develop", got)
	}
}

func TestPushRefusesToCommitPreStagedFiles(t *testing.T) {
	root, _ := gitRepo(t)
	// Something else staged a file the run was not asked to publish.
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "secret.txt")
	if err := os.MkdirAll(filepath.Join(root, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "experiments", "r.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(context.Background(), PushOptions{
		RepoRoot: root, Paths: []string{"experiments"}, Branch: "b", Token: testToken, Message: "benchmark: r",
	}); err != nil {
		t.Fatalf("push: %v", err)
	}
	// The staged secret is still staged locally and was not committed.
	out := run(t, root, "git", "show", "--stat", "--name-only", "HEAD")
	if strings.Contains(out, "secret.txt") {
		t.Fatalf("the pre-staged file was committed:\n%s", out)
	}
}
