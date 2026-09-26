// Package review implements the Issue-based A/B review record: one review
// ties two runs (baseline/candidate) to an originating Issue, posts a marker
// comment with the two public URLs and records A/B/tie/invalid votes. The
// Issue is the canonical vote history; the local store is an index.
package review

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// VoteMarker separates the review ID from the vote payload inside a marker
// comment: "<!-- llmbench:review:<id>|vote:<base64(json Vote)> -->". The
// payload carries the choice, notes, voter and timestamp so the Issue alone
// can reproduce a review's history.
const VoteMarker = "|vote:"

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

// Service implements the review workflow. The Issue is the canonical vote
// history: the local store is only an index (to find the Issue for a review
// ID) and a cache.
type Service struct {
	Store    Store
	Issues   Issues
	Notifier Notifier // optional; receives the Issue link only
	// IssueURL renders the canonical link for a notification.
	IssueURL func(issue int) string
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
	if baseline.Model == candidate.Model {
		// The digest guards against a silent model swap; an absent digest
		// proves nothing, so it cannot satisfy the guard.
		if baseline.ModelDigest == "" || candidate.ModelDigest == "" {
			return Review{}, errors.New("review: same-model comparison requires recorded model digests")
		}
		if baseline.ModelDigest != candidate.ModelDigest {
			return Review{}, errors.New("review: same-model comparison requires matching model digests")
		}
	}

	id := deterministicID(issue, baseline.ID, candidate.ID)
	now := s.now()

	// Retrying a request must not post a duplicate comment: the marker is the
	// idempotency key. A failed lookup must abort the request instead of
	// risking a second marker.
	existing, err := s.Issues.FindComments(ctx, issue, MarkerPrefix+id+" -->")
	if err != nil {
		return Review{}, fmt.Errorf("review: check existing request: %w", err)
	}
	if len(existing) > 0 {
		r, err := s.project(ctx, issue, id, baseline, candidate, now)
		if err != nil {
			return Review{}, err
		}
		if err := s.Store.SaveReview(ctx, r); err != nil {
			return Review{}, fmt.Errorf("review: save: %w", err)
		}
		return r, nil
	}
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
	if s.Notifier != nil && s.IssueURL != nil {
		// Discord is a link notification only: no review content.
		_ = s.Notifier.Notify(ctx, s.IssueURL(issue))
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
	vote := Vote{Choice: choice, Notes: notes, Voter: voter, At: s.now()}
	payload, err := encodeVote(vote)
	if err != nil {
		return Review{}, err
	}
	body := fmt.Sprintf("%s%s|vote:%s -->\n\n**Vote recorded: %s**%s\n",
		MarkerPrefix, reviewID, payload, choice, notesSuffix(notes))
	if err := s.Issues.PostComment(ctx, r.Issue, body); err != nil {
		return Review{}, fmt.Errorf("review: post vote comment: %w", err)
	}
	// Re-project the votes from the Issue: it is the canonical history, so a
	// concurrent vote or a failed local save never loses or invents one.
	projected, err := s.projectVotes(ctx, r)
	if err != nil {
		return Review{}, err
	}
	if err := s.Store.SaveReview(ctx, projected); err != nil {
		return Review{}, fmt.Errorf("review: save: %w", err)
	}
	return projected, nil
}

// Status loads the review index entry and rebuilds the votes from the Issue.
func (s *Service) Status(ctx context.Context, reviewID string) (Review, error) {
	index, err := s.Store.LoadReview(ctx, reviewID)
	if err != nil {
		return Review{}, err
	}
	return s.projectVotes(ctx, index)
}

// projectVotes replaces the cached votes with the markers found on the Issue.
func (s *Service) projectVotes(ctx context.Context, index Review) (Review, error) {
	marker := MarkerPrefix + index.ID + VoteMarker
	comments, err := s.Issues.FindComments(ctx, index.Issue, marker)
	if err != nil {
		return Review{}, fmt.Errorf("review: read votes: %w", err)
	}
	votes := make([]Vote, 0, len(comments))
	for _, body := range comments {
		vote, ok := parseVoteMarker(body, index.ID)
		if !ok {
			continue
		}
		votes = append(votes, vote)
	}
	index.Votes = votes
	return index, nil
}

// project rebuilds a Review from the Issue markers (used when a request is
// retried and the local index may be missing).
func (s *Service) project(ctx context.Context, issue int, id string, baseline, candidate RunView, created time.Time) (Review, error) {
	r := Review{
		ID: id, Issue: issue,
		Baseline: baseline.ID, Candidate: candidate.ID,
		BaselineURL: baseline.PublicURL, CandidateURL: candidate.PublicURL,
		Fingerprint: baseline.Fingerprint,
		CreatedAt:   created,
	}
	return s.projectVotes(ctx, r)
}

// encodeVote renders a vote as the marker payload.
func encodeVote(v Vote) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("review: encode vote: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// parseVoteMarker decodes a vote comment, preserving choice, notes, voter and
// timestamp.
func parseVoteMarker(body, reviewID string) (Vote, bool) {
	marker := MarkerPrefix + reviewID + VoteMarker
	i := strings.Index(body, marker)
	if i < 0 {
		return Vote{}, false
	}
	rest := body[i+len(marker):]
	end := strings.Index(rest, " -->")
	if end < 0 {
		return Vote{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(rest[:end]))
	if err != nil {
		return Vote{}, false
	}
	var v Vote
	if err := json.Unmarshal(raw, &v); err != nil {
		return Vote{}, false
	}
	switch v.Choice {
	case ChoiceA, ChoiceB, ChoiceTie, ChoiceInvalid:
	default:
		return Vote{}, false
	}
	return v, true
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

// deterministicID derives the review ID from the comparison it identifies, so
// a retried request reuses the same marker instead of opening a second one.
func deterministicID(issue int, baseline, candidate string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s", issue, baseline, candidate)))
	return hex.EncodeToString(sum[:8])
}
