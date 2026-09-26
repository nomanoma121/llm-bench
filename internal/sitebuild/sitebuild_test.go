package sitebuild

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/provenance"
)

// writeAdopted creates one adopted experiment with the given payload.
func writeAdopted(t *testing.T, root, model, experiment, payload, reviewJSON string) {
	t.Helper()
	dir := filepath.Join(root, model, experiment, "output")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(payload)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := provenance.ArtifactPayload(dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "schema_version": 1,
  "run_id": "0123456789abcdef0123456789abcdef",
  "experiment_id": "` + experiment + `",
  "model": "` + model + `",
  "benchmark_fingerprint": "fp",
  "artifact_digest": "` + provenance.PayloadDigest(files) + `",
  "prompt_sha256": "p",
  "controller_version": "test",
  "adopted_at": "2026-01-02T03:04:05Z",
  "review": ` + reviewJSON + `,
  "artifacts": [{"path": "index.html", "size": ` + strconv.Itoa(len(body)) + `, "sha256": "` + provenance.SHA256Hex(body) + `"}]
}
`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPublishesOnlyAdoptedExperiments(t *testing.T) {
	expRoot := t.TempDir()
	out := t.TempDir()
	writeAdopted(t, expRoot, "model-a", "exp-1", "<html><head><title>x</title></head><body>hi</body></html>", "null")
	// A placeholder without a manifest must not be published.
	if err := os.MkdirAll(filepath.Join(expRoot, "model-a", "exp-2", "output"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(expRoot, "model-a", "exp-2", "output", "index.html"), []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Build(BuildOptions{ExperimentsRoot: expRoot, Out: out}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "index.html")); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(out, "model-a", "exp-1", "index.html")
	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `sandbox="allow-scripts"`) || !strings.Contains(text, "srcdoc=") {
		t.Fatalf("wrapper must embed the artifact in a sandboxed iframe:\n%s", text)
	}
	if !strings.Contains(text, "not reviewed") {
		t.Fatal("a review-less adoption must be marked as such")
	}
	if _, err := os.Stat(filepath.Join(out, filepath.FromSlash("model-a/exp-2"))); !os.IsNotExist(err) {
		t.Fatal("an experiment without a manifest must not be published")
	}
	if err := VerifyOutput(out); err != nil {
		t.Fatal(err)
	}
}

func TestBuildEscapesUntrustedArtifact(t *testing.T) {
	expRoot := t.TempDir()
	out := t.TempDir()
	hostile := `<html><body><script>alert("</iframe><script>bad()</script>")</script></body></html>`
	writeAdopted(t, expRoot, "m", "e", hostile, "null")
	if err := Build(BuildOptions{ExperimentsRoot: expRoot, Out: out}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "m", "e", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	// The artifact must not be able to close the attribute or inject markup:
	// html/template has to escape it as an attribute value.
	if strings.Contains(text, `srcdoc="<html>`) {
		t.Fatal("srcdoc must be escaped, not concatenated raw")
	}
	if strings.Contains(text, "<script>alert(") || strings.Contains(text, "</iframe><script>") {
		t.Fatalf("artifact escaped the iframe attribute:\n%s", text)
	}
	if !strings.Contains(text, "&lt;script&gt;alert(") {
		t.Fatalf("expected the artifact to appear escaped:\n%s", text)
	}
	if !strings.Contains(text, "&#34;") && !strings.Contains(text, "&#39;") {
		t.Fatalf("expected escaped quotes in srcdoc:\n%s", text)
	}
}

func TestMetaCSPIsInsertedBeforeArtifactContent(t *testing.T) {
	// A script before any head must not precede the policy.
	got := withMetaCSP([]byte(`<script>fetch("https://example.test")</script><html><head></head></html>`))
	if !strings.HasPrefix(got, "<meta http-equiv=\"Content-Security-Policy\"") {
		t.Fatalf("policy must come first: %s", got)
	}
	// A <head> mentioned inside a script string must not fool the insertion.
	got = withMetaCSP([]byte(`<script>const x = "<head>"</script>`))
	meta := strings.Index(got, "http-equiv=\"Content-Security-Policy\"")
	script := strings.Index(got, "<script>")
	if meta < 0 || script < 0 || meta > script {
		t.Fatalf("policy must precede the script: %s", got)
	}
	// A leading doctype stays first so the document keeps standards mode.
	got = withMetaCSP([]byte(`<!DOCTYPE html><html><head><title>x</title></head></html>`))
	if !strings.HasPrefix(got, "<!DOCTYPE html><meta http-equiv=\"Content-Security-Policy\"") {
		t.Fatalf("doctype must stay first: %s", got)
	}
	// Leading whitespace and a BOM are tolerated.
	got = withMetaCSP([]byte("\xef\xbb\xbf\n<!doctype HTML><html></html>"))
	if !strings.HasPrefix(got, "\xef\xbb\xbf\n<!doctype HTML><meta http-equiv=") {
		t.Fatalf("bom/whitespace handling: %q", got)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	expRoot := t.TempDir()
	writeAdopted(t, expRoot, "b-model", "exp-2", "<html></html>", "null")
	writeAdopted(t, expRoot, "a-model", "exp-1", "<html></html>", `{"review_id":"r","issue_url":"https://example.test/issues/1","vote_comment_id":5,"choice":"A"}`)
	first, second := t.TempDir(), t.TempDir()
	if err := Build(BuildOptions{ExperimentsRoot: expRoot, Out: first}); err != nil {
		t.Fatal(err)
	}
	if err := Build(BuildOptions{ExperimentsRoot: expRoot, Out: second}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"index.html", "a-model/exp-1/index.html", "b-model/exp-2/index.html"} {
		a, err := os.ReadFile(filepath.Join(first, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(second, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Fatalf("%s is not deterministic", rel)
		}
	}
	index, err := os.ReadFile(filepath.Join(first, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(index)
	if strings.Index(text, "a-model") > strings.Index(text, "b-model") {
		t.Fatal("the index must be sorted by model when adoption times match")
	}
	if !strings.Contains(text, "https://example.test/issues/1") {
		// The index marks reviewed entries; the link lives on the page.
		pageBody, err := os.ReadFile(filepath.Join(first, "a-model", "exp-1", "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(pageBody), "https://example.test/issues/1") {
			t.Fatal("a reviewed adoption must link its Issue decision")
		}
	}
}
