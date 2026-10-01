package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	gh "github.com/google/go-github/v89/github"
)

type Repo struct {
	c      *gh.Client
	Owner  string
	Name   string
	Branch string
}

func NewRepo(c *gh.Client, fullName, branch string) (*Repo, error) {
	owner, name, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("github: repository %q is not owner/name", fullName)
	}
	return &Repo{c: c, Owner: owner, Name: name, Branch: branch}, nil
}

type Issue struct {
	Number    int
	Body      string
	Labels    []string
	CreatedAt time.Time
}

func (i Issue) Has(label string) bool {
	for _, l := range i.Labels {
		if l == label {
			return true
		}
	}
	return false
}

func (r *Repo) Issues(ctx context.Context, label string) ([]Issue, error) {
	opts := &gh.IssueListByRepoOptions{
		State: "open", Labels: []string{label}, Sort: "created", Direction: "asc",
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	var out []Issue
	for {
		issues, resp, err := r.c.Issues.ListByRepo(ctx, r.Owner, r.Name, opts)
		if err != nil {
			return nil, err
		}
		for _, i := range issues {
			if i.IsPullRequest() {
				continue
			}
			issue := Issue{Number: i.GetNumber(), Body: i.GetBody(), CreatedAt: i.GetCreatedAt().Time}
			for _, l := range i.Labels {
				issue.Labels = append(issue.Labels, l.GetName())
			}
			out = append(out, issue)
		}
		if resp.NextPage == 0 {
			return out, nil
		}
		opts.ListOptions.Page = resp.NextPage
	}
}

func (r *Repo) AddLabel(ctx context.Context, issue int, label string) error {
	_, _, err := r.c.Issues.AddLabelsToIssue(ctx, r.Owner, r.Name, issue, []string{label})
	return err
}

func (r *Repo) RemoveLabel(ctx context.Context, issue int, label string) error {
	resp, err := r.c.Issues.RemoveLabelForIssue(ctx, r.Owner, r.Name, issue, label)
	if isNotFound(resp) {
		return nil
	}
	return err
}

func (r *Repo) Comment(ctx context.Context, issue int, body string) error {
	_, _, err := r.c.Issues.CreateComment(ctx, r.Owner, r.Name, issue, &gh.IssueComment{Body: &body})
	return err
}

func (r *Repo) BranchExists(ctx context.Context, branch string) (bool, error) {
	_, resp, err := r.c.Git.GetRef(ctx, r.Owner, r.Name, "heads/"+branch)
	if isNotFound(resp) {
		return false, nil
	}
	return err == nil, err
}

func (r *Repo) HeadSHA(ctx context.Context) (string, error) {
	ref, _, err := r.c.Git.GetRef(ctx, r.Owner, r.Name, "heads/"+r.Branch)
	if err != nil {
		return "", err
	}
	return ref.GetObject().GetSHA(), nil
}

func (r *Repo) CreateBranch(ctx context.Context, branch, sha string) error {
	_, _, err := r.c.Git.CreateRef(ctx, r.Owner, r.Name, gh.CreateRef{Ref: "refs/heads/" + branch, SHA: sha})
	return err
}

func (r *Repo) DeleteBranch(ctx context.Context, branch string) error {
	resp, err := r.c.Git.DeleteRef(ctx, r.Owner, r.Name, "heads/"+branch)
	if isNotFound(resp) || (resp != nil && resp.StatusCode == http.StatusUnprocessableEntity) {
		return nil
	}
	return err
}

func (r *Repo) File(ctx context.Context, path, ref string) ([]byte, error) {
	file, _, _, err := r.c.Repositories.GetContents(ctx, r.Owner, r.Name, path, &gh.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, fmt.Errorf("github: %s is not a file", path)
	}
	content, err := file.GetContent()
	return []byte(content), err
}

func (r *Repo) CommitFile(ctx context.Context, branch, path, message string, content []byte) error {
	current, _, _, err := r.c.Repositories.GetContents(ctx, r.Owner, r.Name, path, &gh.RepositoryContentGetOptions{Ref: branch})
	if err != nil {
		return err
	}
	_, _, err = r.c.Repositories.UpdateFile(ctx, r.Owner, r.Name, path, &gh.RepositoryContentFileOptions{
		Message: &message, Content: content, SHA: current.SHA, Branch: &branch,
	})
	return err
}

type PullRequest struct {
	Number int
}

// PullRequest returns the open pull request from head, or nil. Merged and
// closed ones are history: a rerun of the same job reuses the branch name.
func (r *Repo) PullRequest(ctx context.Context, head string) (*PullRequest, error) {
	prs, _, err := r.c.PullRequests.List(ctx, r.Owner, r.Name, &gh.PullRequestListOptions{
		State: "open", Head: r.Owner + ":" + head, ListOptions: gh.ListOptions{PerPage: 1},
	})
	if err != nil || len(prs) == 0 {
		return nil, err
	}
	pr := prs[0]
	return &PullRequest{Number: pr.GetNumber()}, nil
}

func (r *Repo) OpenPullRequest(ctx context.Context, head, title, body string) (int, error) {
	if pr, err := r.PullRequest(ctx, head); err != nil || pr != nil {
		if pr != nil {
			return pr.Number, nil
		}
		return 0, err
	}
	pr, _, err := r.c.PullRequests.Create(ctx, r.Owner, r.Name, &gh.NewPullRequest{
		Title: &title, Head: &head, Base: &r.Branch, Body: &body,
	})
	if err != nil {
		return 0, err
	}
	return pr.GetNumber(), nil
}

func (r *Repo) ClosePullRequest(ctx context.Context, number int) error {
	state := "closed"
	_, _, err := r.c.PullRequests.Edit(ctx, r.Owner, r.Name, number, &gh.PullRequest{State: &state})
	return err
}

func isNotFound(resp *gh.Response) bool {
	return resp != nil && resp.StatusCode == http.StatusNotFound
}
