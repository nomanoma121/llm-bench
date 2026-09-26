package preview

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
)

type fakeStore struct {
	runs map[string]run.Run
}

func (f *fakeStore) LoadRun(_ context.Context, id string) (run.Run, error) {
	r, ok := f.runs[id]
	if !ok {
		return run.Run{}, run.ErrNotFound
	}
	return r, nil
}

// sealedFixture writes a payload and returns a handler serving it.
func sealedFixture(t *testing.T, body string) (*Handler, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "r1", "output")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := provenance.ArtifactDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		Store: &fakeStore{runs: map[string]run.Run{
			"r1": {ID: "r1", Phase: run.PhaseSucceeded, Artifacts: run.Artifacts{Dir: filepath.Join(root, "r1"), ArtifactDigest: digest}},
		}},
		ArtifactsDir: func(runID string) string { return filepath.Join(root, runID) },
	}
	return h, dir
}

func get(t *testing.T, h *Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServesSealedArtifactWithSandboxHeaders(t *testing.T) {
	h, _ := sealedFixture(t, "<html></html>")
	rec := get(t, h, http.MethodGet, "/v1/runs/r1/artifacts/index.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "<html></html>" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox allow-scripts") ||
		strings.Contains(csp, "allow-same-origin") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("csp = %q", csp)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache-control = %q", got)
	}
}

func TestEmptyPathServesIndex(t *testing.T) {
	h, _ := sealedFixture(t, "<html/>")
	rec := get(t, h, http.MethodGet, "/v1/runs/r1/artifacts/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHeadHasNoBody(t *testing.T) {
	h, _ := sealedFixture(t, "<html/>")
	rec := get(t, h, http.MethodHead, "/v1/runs/r1/artifacts/index.html")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	h, _ := sealedFixture(t, "<html/>")
	if rec := get(t, h, http.MethodPost, "/v1/runs/r1/artifacts/index.html"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestUnsealedOrUnknownRunIsNotFound(t *testing.T) {
	h, dir := sealedFixture(t, "<html/>")
	h.Store = &fakeStore{runs: map[string]run.Run{"r1": {ID: "r1", Artifacts: run.Artifacts{Dir: dir}}}}
	if rec := get(t, h, http.MethodGet, "/v1/runs/r1/artifacts/index.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("unsealed status = %d", rec.Code)
	}
	h2, _ := sealedFixture(t, "<html/>")
	if rec := get(t, h2, http.MethodGet, "/v1/runs/nope/artifacts/index.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown status = %d", rec.Code)
	}
}

func TestPathTraversalAndBadPathsAreRefused(t *testing.T) {
	h, dir := sealedFixture(t, "<html/>")
	// A file outside the payload would also break the digest; make sure the
	// path is refused before it is even read.
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/v1/runs/r1/artifacts/../secret.txt",
		"/v1/runs/r1/artifacts/..%2fsecret.txt",
		"/v1/runs/r1/artifacts/%2e%2e/secret.txt",
		"/v1/runs/../r1/artifacts/index.html",
		"/v1/runs/r1/artifacts/%5cindex.html",
	} {
		if rec := get(t, h, http.MethodGet, target); rec.Code == http.StatusOK {
			t.Fatalf("%s: expected refusal, got 200", target)
		}
	}
}

func TestTamperedArtifactIsRefused(t *testing.T) {
	h, dir := sealedFixture(t, "<html>sealed</html>")
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>swapped</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := get(t, h, http.MethodGet, "/v1/runs/r1/artifacts/index.html"); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestOversizeFileIsRefused(t *testing.T) {
	h, dir := sealedFixture(t, "<html/>")
	big := strings.Repeat("a", MaxFileSize+1)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := get(t, h, http.MethodGet, "/v1/runs/r1/artifacts/index.html")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSymlinkedPayloadIsRefused(t *testing.T) {
	h, dir := sealedFixture(t, "<html/>")
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "link.html")); err != nil {
		t.Fatal(err)
	}
	if rec := get(t, h, http.MethodGet, "/v1/runs/r1/artifacts/link.html"); rec.Code == http.StatusOK {
		t.Fatal("expected refusal for a symlink")
	}
}

func TestParsePath(t *testing.T) {
	cases := []struct {
		path string
		ok   bool
		rel  string
	}{
		{"/v1/runs/r1/artifacts/index.html", true, "index.html"},
		{"/v1/runs/r1/artifacts/", true, "index.html"},
		{"/v1/runs/r1/artifacts", true, "index.html"},
		{"/v1/runs/r1/index.html", false, ""},
		{"/v1/other/r1/artifacts/index.html", false, ""},
		{"/v1/runs/r1/artifacts/a/b.html", true, "a/b.html"},
	}
	for _, c := range cases {
		runID, rel, ok := parsePath(c.path)
		if ok != c.ok {
			t.Fatalf("%s: ok = %v", c.path, ok)
		}
		if ok && (rel != c.rel || runID != "r1") {
			t.Fatalf("%s: runID = %q rel = %q", c.path, runID, rel)
		}
	}
}
