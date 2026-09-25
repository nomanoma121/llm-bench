// Package review implements the Issue-based A/B review record: one review
// ties two runs (baseline/candidate) to an originating Issue, posts a marker
// comment with the two public URLs and records A/B/tie/invalid votes. The
// Issue is the canonical vote history; the local store is an index.
package review

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Choices accepted for a vote.
const (
	ChoiceA       = "A"
	ChoiceB       = "B"
	ChoiceTie     = "tie"
	ChoiceInvalid = "invalid"
)

// MarkerPrefix precedes the review ID inside the Issue comment so controller
// comments are machine-recognizable. Comments from other authors are ignored.
const MarkerPrefix = "<!-- llmbench:review:"

// Vote is one recorded judgement.
type Vote struct {
	Choice string    `json:"choice"`
	Notes  string    `json:"notes,omitempty"`
	Voter  string    `json:"voter,omitempty"`
	At     time.Time `json:"at"`
}

// Review is the durable review record. Votes mirror the Issue history.
type Review struct {
	ID           string    `json:"id"`
	Issue        int       `json:"issue"`
	Baseline     string    `json:"baseline"`
	Candidate    string    `json:"candidate"`
	BaselineURL  string    `json:"baseline_url"`
	CandidateURL string    `json:"candidate_url"`
	Fingerprint  string    `json:"fingerprint"`
	CreatedAt    time.Time `json:"created_at"`
	Votes        []Vote    `json:"votes,omitempty"`
}

// RunView is the slice of a finished run the service needs. Declared here so
// the service does not import the run package.
type RunView struct {
	ID          string
	PublicURL   string
	Fingerprint string
	Model       string
	ModelDigest string
}

// Store persists review records (last-write-wins; the Issue is the canonical
// vote history).
type Store interface {
	SaveReview(ctx context.Context, r Review) error
	LoadReview(ctx context.Context, id string) (Review, error)
}

// Issues reads and writes the review comments on the originating Issue.
// Implementations must ensure only comments authored by the configured bot
// login are ever interpreted as controller records.
type Issues interface {
	// PostComment creates a new Issue comment with the given body.
	PostComment(ctx context.Context, issue int, body string) error
	// FindComments returns the bodies of comments containing the marker.
	FindComments(ctx context.Context, issue int, marker string) ([]string, error)
}

// Notifier sends an out-of-band notification (Discord) linking the Issue.
type Notifier interface {
	Notify(ctx context.Context, message string) error
}

// Service implements the review workflow.
type Service struct {
	Store    Store
	Issues   Issues
	Notifier Notifier // optional
	Clock    func() time.Time
}

// ErrNotFound is returned for unknown review IDs.
var ErrNotFound = errors.New("review: not found")

// Request validates both runs, posts the comparison comment on the Issue and
// stores the review record.
func (s *Service) Request(ctx context.Context, baseline, candidate RunView, issue int) (Review, error) {
	if baseline.Fingerprint != candidate.Fingerprint {
		return Review{}, fmt.Errorf("review: fingerprint mismatch: runs are not A/B comparable")
	}
	if baseline.PublicURL == "" || candidate.PublicURL == "" {
		return Review{}, errors.New("review: both runs must be published before requesting a review")
	}
	if baseline.Model == candidate.Model && baseline.ModelDigest != candidate.ModelDigest {
		return Review{}, errors.New("review: same-model comparison requires matching model digests")
	}

	id := newID()
	now := s.now()
	body := fmt.Sprintf(
		"%s%s -->\n\n**A/B review requested**\n\n- **A (baseline)**: [%s](%s)\n- **B (candidate)**: [%s](%s)\n\nVote by replying through the controller: `A`, `B`, `tie` or `invalid`.\n",
		MarkerPrefix, id, baseline.ID, baseline.PublicURL, candidate.ID, candidate.PublicURL,
	)
	if err := s.Issues.PostComment(ctx, issue, body); err != nil {
		return Review{}, fmt.Errorf("review: post comment: %w", err)
	}
	r := Review{
		ID: id, Issue: issue,
		Baseline: baseline.ID, Candidate: candidate.ID,
		BaselineURL: baseline.PublicURL, CandidateURL: candidate.PublicURL,
		Fingerprint: baseline.Fingerprint,
		CreatedAt:   now,
	}
	if err := s.Store.SaveReview(ctx, r); err != nil {
		return Review{}, fmt.Errorf("review: save: %w", err)
	}
	if s.Notifier != nil {
		_ = s.Notifier.Notify(ctx, fmt.Sprintf("llmbench: A/B review requested on issue #%d (%s vs %s)", issue, baseline.ID, candidate.ID))
	}
	return r, nil
}

// Vote records a choice: the marker comment goes to the Issue (the canonical
// history) and the local record is updated.
func (s *Service) Vote(ctx context.Context, reviewID, choice, notes, voter string) (Review, error) {
	switch choice {
	case ChoiceA, ChoiceB, ChoiceTie, ChoiceInvalid:
	default:
		return Review{}, fmt.Errorf("review: invalid choice %q", choice)
	}
	r, err := s.Store.LoadReview(ctx, reviewID)
	if err != nil {
		return Review{}, err
	}
	body := fmt.Sprintf("%s%s|vote:%s|%s -->\n\n**Vote recorded: %s**%s\n",
		MarkerPrefix, reviewID, choice, voter, choice, notesSuffix(notes))
	if err := s.Issues.PostComment(ctx, r.Issue, body); err != nil {
		return Review{}, fmt.Errorf("review: post vote comment: %w", err)
	}
	r.Votes = append(r.Votes, Vote{Choice: choice, Notes: notes, Voter: voter, At: s.now()})
	if err := s.Store.SaveReview(ctx, r); err != nil {
		return Review{}, fmt.Errorf("review: save: %w", err)
	}
	return r, nil
}

// Status loads a review record.
func (s *Service) Status(ctx context.Context, reviewID string) (Review, error) {
	return s.Store.LoadReview(ctx, reviewID)
}

func notesSuffix(notes string) string {
	if notes == "" {
		return ""
	}
	return " — " + notes
}

func (s *Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b[:])
}
