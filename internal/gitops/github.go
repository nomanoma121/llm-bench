package gitops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/google/go-github/v89/github"
	"golang.org/x/oauth2"
)

// NewGitHubAPIFromEnv builds the GitHubAPI implementation using
// LLMBENCH_GITHUB_TOKEN from the environment.
func NewGitHubAPIFromEnv(ctx context.Context, owner, repository, baseBranch string) (GitHubAPI, error) {
	token := os.Getenv("LLMBENCH_GITHUB_TOKEN")
	if token == "" {
		return nil, errors.New("gitops: LLMBENCH_GITHUB_TOKEN is required")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(ctx, ts)
	client, err := github.NewClient(github.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("gitops: github client: %w", err)
	}
	return &githubAPI{gh: client, owner: owner, repo: repository, base: baseBranch}, nil
}

type githubAPI struct {
	gh    *github.Client
	owner string
	repo  string
	base  string
}

// FileAt implements GitHubAPI.
func (g *githubAPI) FileAt(ctx context.Context, path, ref string) ([]byte, string, error) {
	fc, _, resp, err := g.gh.Repositories.GetContents(ctx, g.owner, g.repo, path,
		&github.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, "", fmt.Errorf("gitops: %s not found at %s: %w", path, ref, os.ErrNotExist)
		}
		return nil, "", err
	}
	if fc == nil {
		return nil, "", errors.New("gitops: path is a directory")
	}
	content, err := fc.GetContent()
	if err != nil {
		return nil, "", err
	}
	sha := ""
	if fc.SHA != nil {
		sha = *fc.SHA
	}
	return []byte(content), sha, nil
}

// CommitFile implements GitHubAPI. The blob SHA is resolved on the target
// branch so retries after partial progress keep working.
func (g *githubAPI) CommitFile(ctx context.Context, path, branch, message string, content []byte) error {
	opts := &github.RepositoryContentFileOptions{
		Message: github.Ptr(message),
		Content: content,
		Branch:  github.Ptr(branch),
	}
	if fc, _, _, err := g.gh.Repositories.GetContents(ctx, g.owner, g.repo, path,
		&github.RepositoryContentGetOptions{Ref: branch}); err == nil && fc != nil && fc.SHA != nil {
		opts.SHA = fc.SHA
	} else if err != nil && !isNotFound(err) {
		return err
	}
	_, _, err := g.gh.Repositories.CreateFile(ctx, g.owner, g.repo, path, opts)
	return err
}

// CreateBranchFrom implements GitHubAPI. Creating an existing ref counts as
// success so retries are idempotent.
func (g *githubAPI) CreateBranchFrom(ctx context.Context, branch, fromSHA string) error {
	ref, _, err := g.gh.Git.CreateRef(ctx, g.owner, g.repo, github.CreateRef{
		Ref: "refs/heads/" + branch,
		SHA: fromSHA,
	})
	if err != nil {
		if isAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("gitops: create branch %s: %w", branch, err)
	}
	if ref == nil {
		return errors.New("gitops: create branch returned no reference")
	}
	return nil
}

// BaseBranchSHA implements GitHubAPI.
func (g *githubAPI) BaseBranchSHA(ctx context.Context) (string, error) {
	ref, _, err := g.gh.Git.GetRef(ctx, g.owner, g.repo, "heads/"+g.base)
	if err != nil {
		return "", fmt.Errorf("gitops: get ref heads/%s: %w", g.base, err)
	}
	if ref.Object == nil || ref.Object.SHA == nil {
		return "", errors.New("gitops: base branch has no head commit")
	}
	return *ref.Object.SHA, nil
}

// PRState implements GitHubAPI.
func (g *githubAPI) PRState(ctx context.Context, headBranch string) (string, error) {
	prs, _, err := g.gh.PullRequests.List(ctx, g.owner, g.repo, &github.PullRequestListOptions{
		Head:  g.owner + ":" + headBranch,
		State: "all",
	})
	if err != nil {
		return "", err
	}
	for _, pr := range prs {
		if pr.Head != nil && pr.Head.Ref != nil && *pr.Head.Ref == headBranch {
			if pr.MergedAt != nil {
				return "merged", nil
			}
			if pr.State != nil {
				return *pr.State, nil
			}
		}
	}
	return "", nil
}

// CreatePR implements GitHubAPI.
func (g *githubAPI) CreatePR(ctx context.Context, title, headBranch, body string) error {
	_, _, err := g.gh.PullRequests.Create(ctx, g.owner, g.repo, &github.NewPullRequest{
		Title: github.Ptr(title),
		Head:  github.Ptr(headBranch),
		Base:  github.Ptr(g.base),
		Body:  github.Ptr(body),
	})
	if err != nil && isAlreadyExists(err) {
		return nil // an open PR for this head already exists
	}
	return err
}

// ClosePR implements GitHubAPI.
func (g *githubAPI) ClosePR(ctx context.Context, headBranch string) error {
	prs, _, err := g.gh.PullRequests.List(ctx, g.owner, g.repo, &github.PullRequestListOptions{
		Head:  g.owner + ":" + headBranch,
		State: "open",
	})
	if err != nil {
		return err
	}
	for _, pr := range prs {
		if pr.Head != nil && pr.Head.Ref != nil && *pr.Head.Ref == headBranch && pr.Number != nil {
			_, _, err := g.gh.PullRequests.Edit(ctx, g.owner, g.repo, *pr.Number, &github.PullRequest{State: github.Ptr("closed")})
			return err
		}
	}
	return nil
}

// DeleteBranch implements GitHubAPI.
func (g *githubAPI) DeleteBranch(ctx context.Context, branch string) error {
	_, err := g.gh.Git.DeleteRef(ctx, g.owner, g.repo, "heads/"+branch)
	if isNotFound(err) {
		return nil
	}
	return err
}

func isNotFound(err error) bool {
	var ghErr *github.ErrorResponse
	return errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound
}

func isAlreadyExists(err error) bool {
	if errors.Is(err, os.ErrExist) {
		return true
	}
	var ghErr *github.ErrorResponse
	if errors.As(err, &ghErr) {
		return ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusUnprocessableEntity &&
			strings.Contains(fmt.Sprint(ghErr.Message), "exists")
	}
	return false
}
