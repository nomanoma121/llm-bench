// Package issues implements the review package's Issues interface over the
// GitHub API. Only comments authored by the configured bot login are
// surfaced, so other participants cannot forge controller records.
package issues

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v89/github"
	"golang.org/x/oauth2"
)

// Issues posts and searches Issue comments on one repository.
type Issues struct {
	gh    *github.Client
	owner string
	repo  string
	// BotLogin restricts which comments count as controller records.
	BotLogin string
}

// NewFromEnv builds the client using LLMBENCH_GITHUB_TOKEN.
func NewFromEnv(ctx context.Context, owner, repository, botLogin string) (*Issues, error) {
	token := os.Getenv("LLMBENCH_GITHUB_TOKEN")
	if token == "" {
		return nil, errors.New("issues: LLMBENCH_GITHUB_TOKEN is required")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(ctx, ts)
	gh, err := github.NewClient(github.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("issues: github client: %w", err)
	}
	return &Issues{gh: gh, owner: owner, repo: repository, BotLogin: botLogin}, nil
}

// PostComment implements review.Issues.
func (i *Issues) PostComment(ctx context.Context, issue int, body string) error {
	_, _, err := i.gh.Issues.CreateComment(ctx, i.owner, i.repo, issue, &github.IssueComment{Body: github.Ptr(body)})
	return err
}

// FindComments implements review.Issues. Comments are filtered to the bot
// login when configured.
func (i *Issues) FindComments(ctx context.Context, issue int, marker string) ([]string, error) {
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	var out []string
	for {
		comments, resp, err := i.gh.Issues.ListComments(ctx, i.owner, i.repo, issue, opts)
		if err != nil {
			return nil, err
		}
		for _, c := range comments {
			if c.Body == nil {
				continue
			}
			if i.BotLogin != "" {
				if c.User == nil || c.User.Login == nil || *c.User.Login != i.BotLogin {
					continue
				}
			}
			if strings.Contains(*c.Body, marker) {
				out = append(out, *c.Body)
			}
		}
		if resp == nil || resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}
