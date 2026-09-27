package benchmark

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PushOptions describe how a run publishes its own result.
//
// The sandbox commits and pushes; the controller only opens the pull request
// (docs/mvp.md §7). That keeps the GitHub App private key out of the sandbox:
// what the sandbox holds is a short-lived installation token, which is scoped
// to this repository but *not* to a branch, so the default branch must be
// protected against direct pushes on the GitHub side as well.
type PushOptions struct {
	// RepoRoot is the checkout to commit in.
	RepoRoot string
	// Paths are repository-relative paths to add. Nothing else may end up in
	// the commit.
	Paths []string
	// Branch is the branch to create and push.
	Branch string
	// Message is the commit message.
	Message string
	// Token authenticates the push as a GitHub App installation token. It is
	// never written to a file, a URL or a log line.
	Token string
	// Remote is the git remote to push to.
	Remote string
	// Author identities the commit. The sandbox has no git identity, and the
	// default one would attribute the commit to whoever runs the container.
	Author string
	// Logf receives progress lines with the token redacted.
	Logf func(format string, args ...any)
}

// PushCommits is what Push did.
type PushCommits struct {
	Branch  string
	Commit  string
	Base    string
	Remote  string
	Changed int
}

func (o PushOptions) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// ProtectedBranchNames are branch names a run may never push to, in addition
// to the remote's own default branch. The list is a safety net: the real check
// asks the remote which branch is default, because a repository whose default
// is "develop" must be protected too.
var ProtectedBranchNames = []string{"main", "master", "trunk"}

// Push creates the branch, commits the paths and pushes the branch.
//
// It is fail-closed: the branch must not exist on the remote, must not be the
// remote's default branch, must not be a well-known protected name, and the
// commit may only contain the requested paths. Nothing is ever forced.
func Push(ctx context.Context, o PushOptions) (PushCommits, error) {
	if o.RepoRoot == "" {
		return PushCommits{}, fmt.Errorf("benchmark: push: repo root is required")
	}
	if o.Branch == "" {
		return PushCommits{}, fmt.Errorf("benchmark: push: branch is required")
	}
	if len(o.Paths) == 0 {
		return PushCommits{}, fmt.Errorf("benchmark: push: at least one path is required")
	}
	if strings.TrimSpace(o.Message) == "" {
		return PushCommits{}, fmt.Errorf("benchmark: push: a commit message is required")
	}
	for _, protected := range ProtectedBranchNames {
		if o.Branch == protected {
			return PushCommits{}, fmt.Errorf("benchmark: push: refusing to push to the protected branch %q", o.Branch)
		}
	}
	if o.Token == "" {
		return PushCommits{}, fmt.Errorf("benchmark: push: an installation token is required")
	}
	remote := o.Remote
	if remote == "" {
		remote = "origin"
	}
	author := o.Author
	if author == "" {
		author = "llmbench[bot]"
	}

	// The branch may not exist yet, here or on the remote: an existing branch
	// belongs to another run and overwriting it would need a force push.
	if _, err := o.git(ctx, nil, "rev-parse", "--verify", "refs/heads/"+o.Branch); err == nil {
		return PushCommits{}, fmt.Errorf("benchmark: push: branch %q already exists locally", o.Branch)
	}
	if defaultBranch, err := o.defaultBranch(ctx, remote); err != nil {
		return PushCommits{}, err
	} else if o.Branch == defaultBranch {
		return PushCommits{}, fmt.Errorf("benchmark: push: refusing to push to the remote default branch %q", defaultBranch)
	}
	if exists, err := o.remoteBranchExists(ctx, remote, o.Branch); err != nil {
		return PushCommits{}, err
	} else if exists {
		return PushCommits{}, fmt.Errorf("benchmark: push: branch %q already exists on %s", o.Branch, remote)
	}

	base, err := o.git(ctx, nil, "rev-parse", "HEAD")
	if err != nil {
		return PushCommits{}, err
	}
	// Unstage anything a previous step left behind, so the commit contains the
	// requested paths and nothing else.
	if _, err := o.git(ctx, nil, "reset", "--quiet"); err != nil {
		return PushCommits{}, err
	}
	for _, path := range o.Paths {
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(o.RepoRoot, path)
		}
		if _, err := os.Stat(abs); err != nil {
			return PushCommits{}, fmt.Errorf("benchmark: push: %s: %w", path, err)
		}
		if _, err := o.git(ctx, nil, "add", "--", path); err != nil {
			return PushCommits{}, err
		}
	}
	if err := o.checkStaged(ctx); err != nil {
		return PushCommits{}, err
	}
	if _, err := o.git(ctx, nil, "checkout", "-b", o.Branch); err != nil {
		return PushCommits{}, err
	}
	changed, err := o.changedFiles(ctx)
	if err != nil {
		return PushCommits{}, err
	}
	if changed == 0 {
		return PushCommits{}, fmt.Errorf("benchmark: push: nothing to commit in %v", o.Paths)
	}
	if _, err := o.git(ctx, nil,
		"-c", "user.name="+author,
		"-c", "user.email="+author+"@users.noreply.github.com",
		"commit", "-m", o.Message,
	); err != nil {
		return PushCommits{}, err
	}
	head, err := o.git(ctx, nil, "rev-parse", "HEAD")
	if err != nil {
		return PushCommits{}, err
	}
	// The token travels in an HTTP header rather than in the remote URL, so it
	// cannot leak through the checkout's configuration or through an error we
	// print. The header itself is never logged.
	if _, err := o.gitAuth(ctx, "push", remote, "HEAD:refs/heads/"+o.Branch); err != nil {
		return PushCommits{}, err
	}
	o.logf("pushed %s (%s) to %s", o.Branch, short(head), remote)
	return PushCommits{Branch: o.Branch, Commit: strings.TrimSpace(head), Base: strings.TrimSpace(base), Remote: remote, Changed: changed}, nil
}

// checkStaged refuses a commit that would carry anything outside Paths, which
// is how a pre-staged file would otherwise ride along.
func (o PushOptions) checkStaged(ctx context.Context) error {
	out, err := o.git(ctx, nil, "diff", "--cached", "--name-only")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if !o.allowed(name) {
			return fmt.Errorf("benchmark: push: %s is staged but not one of the requested paths %v; refusing to commit it", name, o.Paths)
		}
	}
	return nil
}

func (o PushOptions) allowed(name string) bool {
	for _, path := range o.Paths {
		path = strings.TrimSuffix(path, "/")
		if name == path || strings.HasPrefix(name, path+"/") {
			return true
		}
	}
	return false
}

// defaultBranch asks the remote itself which branch is its default. Reading a
// cached refs/remotes/<remote>/HEAD would be stale, and falling back to the
// local branch would silently accept a push to a remote default that changed.
func (o PushOptions) defaultBranch(ctx context.Context, remote string) (string, error) {
	out, err := o.gitAuth(ctx, "ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && strings.HasPrefix(fields[1], "refs/heads/") && fields[2] == "HEAD" {
			return strings.TrimPrefix(fields[1], "refs/heads/"), nil
		}
	}
	return "", fmt.Errorf("benchmark: push: %s did not report its default branch; refusing to guess", remote)
}

// remoteBranchExists asks the remote, so a leftover branch from an earlier run
// is caught before the push rather than by a rejected push.
func (o PushOptions) remoteBranchExists(ctx context.Context, remote, branch string) (bool, error) {
	// The query needs the token too: a private repository rejects an
	// unauthenticated ls-remote, and a failed check would look like "the
	// branch does not exist".
	out, err := o.gitAuth(ctx, "ls-remote", "--heads", remote, branch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// gitAuth runs a git command that talks to the remote, so it carries the
// authorization header. The header never appears in a log line.
func (o PushOptions) gitAuth(ctx context.Context, args ...string) (string, error) {
	return o.git(ctx, nil, append([]string{"-c", o.authHeader()}, args...)...)
}

func (o PushOptions) authHeader() string {
	// GitHub accepts an installation token as the password of the
	// `x-access-token` user with HTTP basic authentication.
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + o.Token))
	return "http.extraheader=AUTHORIZATION: basic " + basic
}

// changedFiles counts the staged paths.
func (o PushOptions) changedFiles(ctx context.Context) (int, error) {
	out, err := o.git(ctx, nil, "diff", "--cached", "--name-only")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

// git runs one git command in the repository. expectedExit lists extra exit
// codes that are not errors.
func (o PushOptions) git(ctx context.Context, expectedExit []int, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = o.RepoRoot
	// Never pick up an ambient token or ask for credentials interactively.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		code := -1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
			for _, want := range expectedExit {
				if code == want {
					return string(out), nil
				}
			}
		}
		return "", fmt.Errorf("benchmark: git %s: %w: %s", o.redact(strings.Join(args, " ")), err, o.redact(stderr.String()))
	}
	return string(out), nil
}

// redact removes the token from anything that could be logged: the argument
// list (the auth header) and git's own error output.
func (o PushOptions) redact(s string) string {
	if o.Token == "" {
		return s
	}
	s = strings.ReplaceAll(s, o.Token, "***")
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + o.Token))
	s = strings.ReplaceAll(s, basic, "***")
	return strings.ReplaceAll(s, "basic "+basic, "***")
}

func short(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
