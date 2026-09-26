package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
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
}

func (f *fakeIssues) PostComment(_ context.Context, issue int, body string) error {
	f.comments[issue] = append(f.comments[issue], body)
	return nil
}

func (f *fakeIssues) FindComments(_ context.Context, issue int, marker string) ([]string, error) {
	f.marker = marker
	var out []string
	for _, b := range f.comments[issue] {
		if strings.Contains(b, marker) {
			out = append(out, b)
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

func view(id, url, model, digest string) RunView {
	return RunView{ID: id, PublicURL: url, Fingerprint: "fp", Model: model, ModelDigest: digest}
}

func TestRequestValidatesComparability(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	notifier := &fakeNotifier{}
	svc := &Service{
		Store: store, Issues: iss, Notifier: notifier,
		IssueURL: func(issue int) string { return fmt.Sprintf("https://example.test/issues/%d", issue) },
	}

	t.Run("fingerprint mismatch rejected", func(t *testing.T) {
		a := view("a", "https://x/a", "m1", "d1")
		a.Fingerprint = "fp1"
		b := view("b", "https://x/b", "m1", "d1")
		b.Fingerprint = "fp2"
		if _, err := svc.Request(context.Background(), a, b, 1); err == nil || !strings.Contains(err.Error(), "fingerprint") {
			t.Fatalf("want fingerprint error, got %v", err)
		}
	})

	t.Run("unpublished run rejected", func(t *testing.T) {
		a := view("a", "", "m1", "d1")
		b := view("b", "https://x/b", "m1", "d1")
		if _, err := svc.Request(context.Background(), a, b, 1); err == nil || !strings.Contains(err.Error(), "published") {
			t.Fatalf("want publish error, got %v", err)
		}
	})

	t.Run("same model requires matching digests", func(t *testing.T) {
		a := view("a", "https://x/a", "m1", "d1")
		b := view("b", "https://x/b", "m1", "DIFFERENT")
		if _, err := svc.Request(context.Background(), a, b, 1); err == nil || !strings.Contains(err.Error(), "digest") {
			t.Fatalf("want digest error, got %v", err)
		}
	})

	t.Run("cross-model comparison allowed", func(t *testing.T) {
		a := view("a", "https://x/a", "modelA", "d1")
		b := view("b", "https://x/b", "modelB", "d2")
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
		if !strings.Contains(iss.comments[42][0], "https://x/a") || !strings.Contains(iss.comments[42][0], "https://x/b") {
			t.Fatal("comment must contain both URLs")
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

	a := view("a", "https://x/a", "m", "d")
	b := view("b", "https://x/b", "m", "d")
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
	// The vote comment carries the machine-readable marker.
	found := false
	for _, c := range iss.comments[7] {
		if strings.Contains(c, "vote:"+ChoiceB) {
			found = true
		}
	}
	if !found {
		t.Fatal("vote marker comment missing on Issue")
	}
}

func TestStatusProjectsVotesFromIssue(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	svc := &Service{Store: store, Issues: iss}
	a := view("a", "https://x/a", "m", "d")
	b := view("b", "https://x/b", "m", "d")
	rec, err := svc.Request(context.Background(), a, b, 7)
	if err != nil {
		t.Fatal(err)
	}
	// A vote recorded by someone else directly on the Issue (or a local save
	// that failed) must still appear: the Issue is canonical.
	iss.comments[7] = append(iss.comments[7], fmt.Sprintf("%s%s|vote:B|judge -->", MarkerPrefix, rec.ID))
	got, err := svc.Status(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Votes) != 1 || got.Votes[0].Choice != ChoiceB || got.Votes[0].Voter != "judge" {
		t.Fatalf("votes = %+v", got.Votes)
	}
	if !strings.Contains(iss.lastMarker(), rec.ID) {
		t.Fatalf("status must query the Issue with the review marker: %q", iss.lastMarker())
	}
}

func TestRequestIsRetrySafe(t *testing.T) {
	store := &fakeStore{reviews: map[string]Review{}}
	iss := &fakeIssues{comments: map[int][]string{}}
	svc := &Service{Store: store, Issues: iss}
	a := view("a", "https://x/a", "m", "d")
	b := view("b", "https://x/b", "m", "d")
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
	a := view("a", "https://x/a", "m", "")
	b := view("b", "https://x/b", "m", "")
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
		IssueURL: func(issue int) string { return fmt.Sprintf("https://example.test/issues/%d", issue) },
	}
	a := view("a", "https://x/a", "m", "d")
	b := view("b", "https://x/b", "m", "d")
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
