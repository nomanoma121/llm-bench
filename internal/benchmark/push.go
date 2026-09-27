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
// protected against direct pushes on the GitHub side.
type PushOptions struct {
	// RepoRoot is the checkout to commit in.
	RepoRoot string
	// Paths are repository-relative paths to add.
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

// ProtectedBranches are the branch names a run may never push to. The check is
// a safety net in front of the branch protection that the operator configures:
// a bug here must not be able to rewrite the default branch.
var ProtectedBranches = []string{"main", "master", "trunk"}

// Push creates the branch, commits the paths and pushes the branch.
//
// It is deliberately fail-closed about the branch name and never forces: the
// job may only add a branch, and an existing branch is an error rather than
// something to overwrite.
func Push(ctx context.Context, o PushOptions) (PushCommits, error) {
	if o.RepoRoot == "" {
		return PushCommits{}, fmt.Errorf("benchmark: push: repo root is required")
	}
	if o.Branch == "" {
		return PushCommits{}, fmt.Errorf("benchmark: push: branch is required")
	}
	for _, protected := range ProtectedBranches {
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

	base, err := o.git(ctx, nil, "rev-parse", "HEAD")
	if err != nil {
		return PushCommits{}, err
	}
	// Start from the current HEAD: the sandbox checked out the source it was
	// asked to measure, and the branch has to carry that commit as its parent.
	if _, err := o.git(ctx, nil, "checkout", "-b", o.Branch); err != nil {
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
	// `diff --cached --quiet` exits 1 when something is staged, which is the
	// normal case; any other code is a real failure.
	if _, err := o.git(ctx, []int{1}, "diff", "--cached", "--quiet"); err != nil {
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
	// cannot leak through the checkout's configuration or the error we print.
	// The header itself is never logged.
	if _, err := o.git(ctx, nil, "-c", o.authHeader(), "push", remote, "HEAD:refs/heads/"+o.Branch); err != nil {
		return PushCommits{}, err
	}
	o.logf("pushed %s (%s) to %s", o.Branch, short(head), remote)
	return PushCommits{Branch: o.Branch, Commit: strings.TrimSpace(head), Base: strings.TrimSpace(base), Remote: remote, Changed: changed}, nil
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
	if basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + o.Token)); basic != "" {
		s = strings.ReplaceAll(s, basic, "***")
	}
	return strings.ReplaceAll(s, "basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+o.Token)), "***")
}

func short(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
