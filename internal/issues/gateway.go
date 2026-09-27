package issues

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/google/go-github/v89/github"

	"github.com/nomanoma121/llm-bench/internal/controller"
	"github.com/nomanoma121/llm-bench/internal/operator"
)

// Gateway is the controller's view of GitHub: it lists requests, mirrors the
// job state as labels, and opens the result pull request. It is implemented
// over go-github with an installation-token client, so the App private key
// stays in the controller (docs/mvp.md §7).
type Gateway struct {
	gh    *github.Client
	owner string
	repo  string
	// BotLogin is the login the App's comments appear as, used to keep the
	// controller from answering its own comments.
	BotLogin string
}

// NewGateway binds a GitHub client to one repository.
func NewGateway(gh *github.Client, owner, repository, botLogin string) *Gateway {
	return &Gateway{gh: gh, owner: owner, repo: repository, BotLogin: botLogin}
}

// labelSet is the set of state labels the controller owns.
func labelSet(labels operator.Labels) map[string]bool {
	return map[string]bool{labels.Claimed: true, labels.Done: true, labels.Failed: true}
}

// Pending lists open issues carrying a kind label and no state label, oldest
// first, so requests are served in the order they were made.
func (g *Gateway) Pending(ctx context.Context, labels operator.Labels) ([]controller.Issue, error) {
	state := labelSet(labels)
	byNumber := map[int]controller.Issue{}
	// GitHub's label filter is an AND: asking for both kind labels at once
	// returns only issues that carry both, which no request does. The two
	// kinds are therefore listed separately and merged here.
	for _, kindLabel := range []string{labels.Benchmark, labels.Optimize} {
		if kindLabel == "" {
			continue
		}
		opts := &github.IssueListByRepoOptions{
			State:  "open",
			Labels: []string{kindLabel},
			// Oldest first: requests are served in the order they were made,
			// and a stable order keeps recovery deterministic.
			Sort:        "created",
			Direction:   "asc",
			ListOptions: github.ListOptions{PerPage: 100},
		}
		for {
			issues, resp, err := g.gh.Issues.ListByRepo(ctx, g.owner, g.repo, opts)
			if err != nil {
				return nil, fmt.Errorf("issues: list: %w", err)
			}
			for _, issue := range issues {
				// A pull request is an issue in the API and must never be read
				// as a request.
				if issue.IsPullRequest() {
					continue
				}
				var names []string
				var hasState bool
				for _, label := range issue.Labels {
					name := label.GetName()
					names = append(names, name)
					if state[name] {
						hasState = true
					}
				}
				if hasState {
					continue
				}
				byNumber[issue.GetNumber()] = controller.Issue{
					Number: issue.GetNumber(),
					Title:  issue.GetTitle(),
					Body:   issue.GetBody(),
					Labels: names,
				}
			}
			if resp == nil || resp.NextPage == 0 {
				break
			}
			opts.ListOptions.Page = resp.NextPage
		}
	}
	out := make([]controller.Issue, 0, len(byNumber))
	for _, issue := range byNumber {
		out = append(out, issue)
	}
	// Issue numbers increase with creation, so sorting by number restores the
	// order the two listings were merged from.
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// Label adds a label to an issue.
func (g *Gateway) Label(ctx context.Context, issue int, label string) error {
	_, _, err := g.gh.Issues.AddLabelsToIssue(ctx, g.owner, g.repo, issue, []string{label})
	if err != nil {
		return fmt.Errorf("issues: add label %q: %w", label, err)
	}
	return nil
}

// Unlabel removes a label from an issue. A missing label is not an error.
func (g *Gateway) Unlabel(ctx context.Context, issue int, label string) error {
	resp, err := g.gh.Issues.RemoveLabelForIssue(ctx, g.owner, g.repo, issue, label)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("issues: remove label %q: %w", label, err)
	}
	return nil
}

// Comment posts one comment.
func (g *Gateway) Comment(ctx context.Context, issue int, body string) error {
	_, _, err := g.gh.Issues.CreateComment(ctx, g.owner, g.repo, issue, &github.IssueComment{Body: github.Ptr(body)})
	if err != nil {
		return fmt.Errorf("issues: comment: %w", err)
	}
	return nil
}

// OpenPR opens the result pull request. An existing PR for the same head is
// returned instead of failing, so a retry after a crash does not open a second
// one for the same branch.
func (g *Gateway) OpenPR(ctx context.Context, pr controller.PullRequest) (int, error) {
	existing, _, err := g.gh.PullRequests.List(ctx, g.owner, g.repo, &github.PullRequestListOptions{
		State: "all", Head: g.owner + ":" + pr.Head, ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return 0, fmt.Errorf("issues: list pull requests: %w", err)
	}
	if len(existing) > 0 {
		return existing[0].GetNumber(), nil
	}
	created, _, err := g.gh.PullRequests.Create(ctx, g.owner, g.repo, &github.NewPullRequest{
		Title: github.Ptr(pr.Title),
		Head:  github.Ptr(pr.Head),
		Base:  github.Ptr(pr.Base),
		Body:  github.Ptr(pr.Body),
	})
	if err != nil {
		return 0, fmt.Errorf("issues: create pull request: %w", err)
	}
	return created.GetNumber(), nil
}

// Claimed reports whether the issue carries one of the state labels.
func (g *Gateway) Claimed(ctx context.Context, issue int, labels operator.Labels) (bool, error) {
	got, _, err := g.gh.Issues.Get(ctx, g.owner, g.repo, issue)
	if err != nil {
		return false, fmt.Errorf("issues: get: %w", err)
	}
	state := labelSet(labels)
	for _, label := range got.Labels {
		if state[label.GetName()] {
			return true, nil
		}
	}
	return false, nil
}

// EnsureLabels creates the labels the controller writes when the repository
// does not have them yet. GitHub silently ignores an unknown label in an Issue
// template, so the controller has to make them exist.
func (g *Gateway) EnsureLabels(ctx context.Context, labels operator.Labels) error {
	want := []struct {
		name  string
		color string
	}{
		{labels.Benchmark, "0e8a16"},
		{labels.Optimize, "1d76db"},
		{labels.Claimed, "fbca04"},
		{labels.Done, "0e8a16"},
		{labels.Failed, "b60205"},
	}
	for _, w := range want {
		if w.name == "" {
			continue
		}
		if _, _, err := g.gh.Issues.GetLabel(ctx, g.owner, g.repo, w.name); err == nil {
			continue
		}
		if _, _, err := g.gh.Issues.CreateLabel(ctx, g.owner, g.repo, &github.Label{
			Name: github.Ptr(w.name), Color: github.Ptr(w.color),
		}); err != nil {
			// A race with another replica is fine: the label exists now.
			if _, _, getErr := g.gh.Issues.GetLabel(ctx, g.owner, g.repo, w.name); getErr != nil {
				return fmt.Errorf("issues: create label %q: %w", w.name, err)
			}
		}
	}
	return nil
}
