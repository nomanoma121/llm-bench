// Package pages publishes run outputs to a static site repository (GitHub
// Pages or any host serving a branch). Publication is idempotent: the run ID
// determines the path, so re-publishing overwrites the same objects.
//
// Deployment requirements (operator responsibility): a dedicated origin that
// holds no credentials, a CSP that fixes the allowed script sources and
// blocks outbound connections (see docs/architecture.md §4.9).
package pages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v89/github"
	"golang.org/x/oauth2"
)

// Publisher publishes run outputs.
type Publisher struct {
	gh      *github.Client
	owner   string
	repo    string
	branch  string
	baseURL string
	extra   map[string]string // additional static files (e.g. _headers)
}

// New builds a publisher from the environment token.
func New(owner, repository, branch, baseURL string, extra map[string]string) (*Publisher, error) {
	token := os.Getenv("LLMBENCH_GITHUB_TOKEN")
	if token == "" {
		return nil, errors.New("pages: LLMBENCH_GITHUB_TOKEN is required")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(context.Background(), ts)
	gh, err := github.NewClient(github.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("pages: github client: %w", err)
	}
	return &Publisher{gh: gh, owner: owner, repo: repository, branch: branch, baseURL: strings.TrimRight(baseURL, "/"), extra: extra}, nil
}

// publishAttempts bounds the retry loop for concurrent publishes: the branch
// may move between reading the base commit and updating the ref.
const publishAttempts = 3

// Publish commits runs/<run-id>/index.html (plus extra files) to the site
// branch in a single tree commit and returns the public URL. Re-publishing is
// idempotent; a concurrent publish that moved the branch is retried from the
// new head instead of force-overwriting it.
func (p *Publisher) Publish(ctx context.Context, runID string, html []byte) (string, error) {
	var lastErr error
	for attempt := 0; attempt < publishAttempts; attempt++ {
		url, err := p.publishOnce(ctx, runID, html)
		if err == nil {
			return url, nil
		}
		lastErr = err
		if !isRefConflict(err) {
			break
		}
	}
	return "", lastErr
}

// isRefConflict reports whether the branch moved under us (non-fast-forward).
func isRefConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Update is not a fast forward") ||
		strings.Contains(msg, "does not match") ||
		strings.Contains(msg, "Unprocessable") ||
		strings.Contains(msg, "422")
}

func (p *Publisher) publishOnce(ctx context.Context, runID string, html []byte) (string, error) {
	baseRef, _, err := p.gh.Git.GetRef(ctx, p.owner, p.repo, "heads/"+p.branch)
	if err != nil {
		return "", fmt.Errorf("pages: get branch ref: %w", err)
	}
	baseCommit, _, err := p.gh.Repositories.GetCommit(ctx, p.owner, p.repo, *baseRef.Object.SHA, nil)
	if err != nil {
		return "", fmt.Errorf("pages: get base commit: %w", err)
	}

	entries := map[string][]byte{
		"runs/" + runID + "/index.html": html,
	}
	for name, content := range p.extra {
		entries[name] = []byte(content)
	}

	var treeEntries []*github.TreeEntry
	for path, content := range entries {
		blob, _, err := p.gh.Git.CreateBlob(ctx, p.owner, p.repo, github.Blob{
			Content:  github.Ptr(string(content)),
			Encoding: github.Ptr("utf-8"),
		})
		if err != nil {
			return "", fmt.Errorf("pages: blob %s: %w", path, err)
		}
		treeEntries = append(treeEntries, &github.TreeEntry{
			Path: github.Ptr(path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  blob.SHA,
		})
	}

	tree, _, err := p.gh.Git.CreateTree(ctx, p.owner, p.repo, baseCommit.Commit.GetTree().GetSHA(), treeEntries)
	if err != nil {
		return "", fmt.Errorf("pages: create tree: %w", err)
	}
	parent := baseCommit.GetSHA()
	commit, _, err := p.gh.Git.CreateCommit(ctx, p.owner, p.repo, github.Commit{
		Message: github.Ptr("llmbench: publish run " + runID),
		Tree:    tree,
		Parents: []*github.Commit{{SHA: github.Ptr(parent)}},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("pages: create commit: %w", err)
	}
	// No force: a concurrent publish must surface as a conflict and be
	// retried from the new head.
	if _, _, err := p.gh.Git.UpdateRef(ctx, p.owner, p.repo, "heads/"+p.branch, github.UpdateRef{
		SHA: commit.GetSHA(),
	}); err != nil {
		return "", fmt.Errorf("pages: update branch: %w", err)
	}
	return p.publicURL(runID), nil
}

func (p *Publisher) publicURL(runID string) string {
	return p.baseURL + "/runs/" + runID + "/"
}
