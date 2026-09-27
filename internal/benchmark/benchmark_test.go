package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/job"
)

const fakeServer = `#!/bin/sh
while [ $# -gt 0 ]; do [ "$1" = "--port" ] && port=$2; shift; done
exec python3 -c '
import http.server, json, sys, time
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        body = b"{\"status\":\"ok\"}" if self.path == "/health" else b"llamacpp:kv_cache_usage_ratio 0.25\n"
        self.send_response(200); self.end_headers(); self.wfile.write(body)
    def do_POST(self):
        self.rfile.read(int(self.headers["Content-Length"]))
        self.send_response(200); self.end_headers()
        for c in ["a", "b", "c"]:
            time.sleep(0.01)
            self.wfile.write(("data: " + json.dumps({"content": c}) + "\n\n").encode()); self.wfile.flush()
        self.wfile.write(("data: " + json.dumps({"content": "", "timings": {"prompt_n": 4, "predicted_n": 3}}) + "\n\n").encode())
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
' "$port"
`

func TestRunAndCompare(t *testing.T) {
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 is required for the fake runtime")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(bin, []byte(fakeServer), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := job.Spec{
		Kind:     job.Benchmark,
		Model:    "m",
		Runtime:  job.Runtime{Engine: "llamacpp", Port: 18431, ReadyTimeoutSeconds: 20},
		Workload: []job.Case{{Name: "short", PromptText: "hello", MaxTokens: 3, Repeats: 2}},
		Metrics:  []string{job.MetricRuntime},
	}
	run := func(name string) string {
		out := filepath.Join(dir, name)
		result, err := Run(context.Background(), Config{
			Spec: spec, JobID: name, Root: dir, OutDir: out, Binary: bin,
			Model: Model{Path: "/models/m", Digest: "sha256:abc"}, SampleInterval: 20e6,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.MeasurementValid {
			t.Fatalf("invalid: %v", result.InvalidReasons)
		}
		if _, ok := result.Metric("decode_tok_per_s", "short"); !ok {
			t.Fatalf("no decode metric: %+v", result.Metrics)
		}
		for _, f := range []string{"jobspec.yaml", "series.jsonl", "result.json", "README.md", "raw/runtime.log"} {
			if _, err := os.Stat(filepath.Join(out, f)); err != nil {
				t.Fatal(err)
			}
		}
		series, _ := os.ReadFile(filepath.Join(out, "series.jsonl"))
		if !strings.Contains(string(series), "llamacpp:kv_cache_usage_ratio") {
			t.Fatal("runtime metrics were not sampled")
		}
		return out
	}
	a, b := run("a"), run("b")
	c, err := Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Comparable || !c.MeasurementValid || len(c.Deltas) == 0 {
		t.Fatalf("comparison %+v", c)
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	r, err := parseNvidiaSMI([]byte("NVIDIA RTX 5090, 1200, 37, 575.51\n"))
	if err != nil || r.Driver != "575.51" || r.GPUs[0].UsedMiB != 1200 {
		t.Fatalf("%+v %v", r, err)
	}
}
