package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeStore struct{ reviews map[string]Review }

func (s *fakeStore) SaveReview(_ context.Context, r Review) error {
	s.reviews[r.ID] = r
	return nil
}

func (s *fakeStore) LoadReview(_ context.Context, id string) (Review, error) {
	r, ok := s.reviews[id]
	if !ok {
		return Review{}, ErrNotFound
	}
	return r, nil
}

type fakeIssues struct {
	comments map[int][]string // issue -> bodies
	marker   string
	nextID   int64
}

func (f *fakeIssues) PostComment(_ context.Context, issue int, body string) error {
	f.comments[issue] = append(f.comments[issue], body)
	return nil
}

// FindComments mirrors the GitHub adapter: ascending comment ID order.
func (f *fakeIssues) FindComments(_ context.Context, issue int, marker string) ([]Comment, error) {
	f.marker = marker
	var out []Comment
	for i, b := range f.comments[issue] {
		if strings.Contains(b, marker) {
			out = append(out, Comment{ID: int64(i + 1), Body: b})
		}
	}
	return out, nil
}

// findComments records markers for assertions.
func (f *fakeIssues) lastMarker() string { return f.marker }

func (f *fakeIssues) setMarker(m string) { f.marker = m }

type fakeNotifier struct{ messages []string }

func (n *fakeNotifier) Notify(_ context.Context, message string) error {
	n.messages = append(n.messages, message)
	return nil
}

func view(id, model, digest string) RunView {
	return RunView{ID: id, ArtifactDigest: "sha256:" + id, Fingerprint: "fp", Model: model, ModelDigest: digest}
}

func TestRequestValidatesComparability(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	notifier := &fakeNotifier{}
	svc := &Service{
		Store: store, Issues: iss, Notifier: notifier,
		IssueURL:   func(issue int) string { return fmt.Sprintf("https://example.test/issues/%d", issue) },
		PreviewURL: func(runID string) string { return "https://preview.test/runs/" + runID },
	}

	t.Run("fingerprint mismatch rejected", func(t *testing.T) {
		a := view("a", "m1", "d1")
		a.Fingerprint = "fp1"
		b := view("b", "m1", "d1")
		b.Fingerprint = "fp2"
		if _, err := svc.Request(context.Background(), a, b, 1); err == nil || !strings.Contains(err.Error(), "fingerprint") {
			t.Fatalf("want fingerprint error, got %v", err)
		}
	})

	t.Run("unsealed artifact rejected", func(t *testing.T) {
		a := view("a", "m1", "d1")
		b := view("b", "m1", "d1")
		b.ArtifactDigest = ""
		if _, err := svc.Request(context.Background(), a, b, 1); err == nil || !strings.Contains(err.Error(), "sealed artifact") {
			t.Fatalf("want sealed-artifact error, got %v", err)
		}
	})

	t.Run("same model requires matching digests", func(t *testing.T) {
		a := view("a", "m1", "d1")
		b := view("b", "m1", "DIFFERENT")
		if _, err := svc.Request(context.Background(), a, b, 1); err == nil || !strings.Contains(err.Error(), "digest") {
			t.Fatalf("want digest error, got %v", err)
		}
	})

	t.Run("cross-model comparison allowed", func(t *testing.T) {
		a := view("a", "modelA", "d1")
		b := view("b", "modelB", "d2")
		rec, err := svc.Request(context.Background(), a, b, 42)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Issue != 42 {
			t.Fatalf("issue = %d", rec.Issue)
		}
		if !strings.Contains(iss.comments[42][0], MarkerPrefix+rec.ID) {
			t.Fatal("marker comment missing")
		}
		if !strings.Contains(iss.comments[42][0], "https://preview.test/runs/a") ||
			!strings.Contains(iss.comments[42][0], "https://preview.test/runs/b") {
			t.Fatal("comment must contain both preview URLs")
		}
		if !strings.Contains(iss.comments[42][0], RequestMarker) {
			t.Fatal("request marker must carry the reviewed run/digest pairs")
		}
		if len(notifier.messages) != 1 || notifier.messages[0] != "https://example.test/issues/42" {
			t.Fatalf("notification must be the Issue link only: %v", notifier.messages)
		}
	})
}

func TestVoteRecordsAndPosts(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	svc := &Service{Store: store, Issues: iss}

	a := view("a", "m", "d")
	b := view("b", "m", "d")
	rec, err := svc.Request(context.Background(), a, b, 7)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Vote(context.Background(), rec.ID, "maybe", "", "judge"); err == nil {
		t.Fatal("invalid choice accepted")
	}
	if _, err := svc.Vote(context.Background(), rec.ID, ChoiceB, "cleaner geometry", "judge"); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Status(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Votes) != 1 || r.Votes[0].Choice != ChoiceB {
		t.Fatalf("votes = %+v", r.Votes)
	}
	// The vote comment carries the machine-readable marker with its payload.
	found := false
	for _, c := range iss.comments[7] {
		if strings.Contains(c, VoteMarker) && strings.Contains(c, "**Vote recorded: "+ChoiceB+"**") {
			found = true
		}
	}
	if !found {
		t.Fatalf("vote marker comment missing on Issue: %v", iss.comments[7])
	}
}

func TestStatusProjectsVotesFromIssue(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	svc := &Service{Store: store, Issues: iss}
	a := view("a", "m", "d")
	b := view("b", "m", "d")
	rec, err := svc.Request(context.Background(), a, b, 7)
	if err != nil {
		t.Fatal(err)
	}
	// A vote recorded by someone else directly on the Issue (or a local save
	// that failed) must still appear: the Issue is canonical.
	payload, err := encodeVote(Vote{Choice: ChoiceB, Notes: "cleaner", Voter: "judge", At: time.Unix(1700000000, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	iss.comments[7] = append(iss.comments[7], fmt.Sprintf("%s%s%s%s -->", MarkerPrefix, rec.ID, VoteMarker, payload))
	got, err := svc.Status(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Votes) != 1 || got.Votes[0].Choice != ChoiceB || got.Votes[0].Voter != "judge" {
		t.Fatalf("votes = %+v", got.Votes)
	}
	// Notes and the original timestamp survive the projection.
	if got.Votes[0].Notes != "cleaner" || !got.Votes[0].At.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("projection lost vote details: %+v", got.Votes[0])
	}
	if !strings.Contains(iss.lastMarker(), rec.ID) {
		t.Fatalf("status must query the Issue with the review marker: %q", iss.lastMarker())
	}
}

func TestRequestIsRetrySafe(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	svc := &Service{Store: store, Issues: iss}
	a := view("a", "m", "d")
	b := view("b", "m", "d")
	first, err := svc.Request(context.Background(), a, b, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Request(context.Background(), a, b, 7)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("review IDs differ on retry: %s vs %s", first.ID, second.ID)
	}
	if len(iss.comments[7]) != 1 {
		t.Fatalf("retry posted a duplicate comment: %d comments", len(iss.comments[7]))
	}
}

func TestSameModelWithoutDigestsIsRefused(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	svc := &Service{Store: store, Issues: iss}
	a := view("a", "m", "")
	b := view("b", "m", "")
	_, err := svc.Request(context.Background(), a, b, 7)
	if err == nil || !strings.Contains(err.Error(), "recorded model digests") {
		t.Fatalf("missing digests must be refused, got %v", err)
	}
}

func TestNotifierReceivesIssueLinkOnly(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	notifier := &fakeNotifier{}
	svc := &Service{
		Store: store, Issues: iss, Notifier: notifier,
		IssueURL:   func(issue int) string { return fmt.Sprintf("https://example.test/issues/%d", issue) },
		PreviewURL: func(runID string) string { return "https://preview.test/runs/" + runID },
	}
	a := view("a", "m", "d")
	b := view("b", "m", "d")
	if _, err := svc.Request(context.Background(), a, b, 42); err != nil {
		t.Fatal(err)
	}
	if len(notifier.messages) != 1 || notifier.messages[0] != "https://example.test/issues/42" {
		t.Fatalf("notification must be the Issue link only: %v", notifier.messages)
	}
}

func TestStatusUnknownReview(t *testing.T) {
	svc := &Service{Store: &fakeStore{reviews: map[string]Review{}}, Issues: &fakeIssues{comments: map[int][]string{}}}
	if _, err := svc.Status(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDecisionUsesLastVoteInCommentOrder(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	svc := &Service{
		Store: store, Issues: iss,
		IssueURL:   func(issue int) string { return "https://example.test/issues/7" },
		PreviewURL: func(runID string) string { return "https://preview.test/runs/" + runID },
	}
	rec, err := svc.Request(context.Background(), view("base", "m", "d"), view("cand", "m", "d"), 7)
	if err != nil {
		t.Fatal(err)
	}

	// No vote yet: the comparison is unresolved.
	if _, err := svc.Decision(context.Background(), rec.ID); err == nil {
		t.Fatal("expected an error before any vote")
	}

	if _, err := svc.Vote(context.Background(), rec.ID, ChoiceA, "", ""); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Decision(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Decided() || first.Choice != ChoiceA || first.Selected().RunID != "base" {
		t.Fatalf("decision = %+v", first)
	}
	firstComment := first.CommentID

	// A later vote wins: the ordering key is the Issue comment ID.
	if _, err := svc.Vote(context.Background(), rec.ID, ChoiceB, "better", ""); err != nil {
		t.Fatal(err)
	}
	second, err := svc.Decision(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Choice != ChoiceB || second.Selected().RunID != "cand" {
		t.Fatalf("decision = %+v", second)
	}
	if second.CommentID <= firstComment {
		t.Fatalf("comment id did not advance: %d -> %d", firstComment, second.CommentID)
	}
	if second.Baseline.RunID != "base" || second.Candidate.RunID != "cand" ||
		second.Baseline.Digest == "" || second.Candidate.Digest == "" {
		t.Fatalf("request payload was not resolved from the Issue: %+v", second)
	}
	if second.IssueURL != "https://example.test/issues/7" {
		t.Fatalf("issue url = %q", second.IssueURL)
	}

	// tie/invalid never authorize adoption, even as the last vote.
	if _, err := svc.Vote(context.Background(), rec.ID, ChoiceTie, "", ""); err != nil {
		t.Fatal(err)
	}
	tie, err := svc.Decision(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tie.Decided() {
		t.Fatalf("tie must stay undecided: %+v", tie)
	}
}

func TestDecisionRejectsLegacyRequestMarker(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{
		"rev1": {ID: "rev1", Issue: 3, Baseline: "a", Candidate: "b"},
	}}
	iss := &fakeIssues{comments: map[int][]string{
		// Marker without the request payload: the digests were never recorded.
		3: {MarkerPrefix + "rev1 -->\n\nlegacy request\n"},
	}}
	svc := &Service{Store: store, Issues: iss}
	if _, err := svc.Decision(context.Background(), "rev1"); err == nil {
		t.Fatal("expected a legacy-marker error")
	}
}
