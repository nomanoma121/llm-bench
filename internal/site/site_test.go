package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	write("m/2026-09-27-issue1/result.json", `{"job_id":"x","measurement_valid":true,"metrics":[{"name":"decode_tok_per_s","case":"a","unit":"tok/s","value":42}]}`)
	write("m/visual/output/index.html", `<script>alert("x")</script>"`)
	write("m/empty/README.md", "nothing")

	if err := Build(root, out); err != nil {
		t.Fatal(err)
	}
	index, _ := os.ReadFile(filepath.Join(out, "index.html"))
	if !strings.Contains(string(index), "2026-09-27-issue1") || strings.Contains(string(index), "empty") {
		t.Fatalf("index:\n%s", index)
	}
	result, _ := os.ReadFile(filepath.Join(out, "m", "2026-09-27-issue1", "index.html"))
	if !strings.Contains(string(result), "42.00") {
		t.Fatalf("result page:\n%s", result)
	}
	visual, _ := os.ReadFile(filepath.Join(out, "m", "visual", "index.html"))
	if !strings.Contains(string(visual), `sandbox="allow-scripts"`) || strings.Contains(string(visual), "<script>alert") {
		t.Fatalf("artifact is not escaped into a sandboxed iframe:\n%s", visual)
	}
}
