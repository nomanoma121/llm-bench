package benchmark

import (
	"context"
	"os"
	"os/exec"
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
            self.wfile.write(("data: " + json.dumps({"choices": [{"delta": {"content": c}}]}) + "\n\n").encode()); self.wfile.flush()
        self.wfile.write(("data: " + json.dumps({"choices": [], "usage": {"prompt_tokens": 4, "completion_tokens": 3}}) + "\n\n").encode())
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
	models := filepath.Join(dir, "models")
	os.MkdirAll(filepath.Join(models, "m"), 0o755)
	os.WriteFile(filepath.Join(models, "m", "weights.gguf"), []byte("weights"), 0o644)
	spec := job.Spec{
		Kind:     job.Benchmark,
		Model:    "m/weights.gguf",
		Runtime:  job.Runtime{Engine: "llamacpp", Port: 18431, ReadyTimeoutSeconds: 20},
		Workload: []job.Case{{Name: "short", PromptText: "hello", MaxTokens: 3, Repeats: 2}},
		Metrics:  []string{job.MetricRuntime},
	}
	run := func(name string) string {
		out := filepath.Join(dir, name)
		result, err := Run(context.Background(), Config{
			Spec: spec, JobID: name, Root: dir, OutDir: out, Binary: bin,
			ModelsDir: models, SampleInterval: 20e6,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Model.ID != "weights" || !strings.HasPrefix(result.Model.Digest, "sha256:") {
			t.Fatalf("model %+v", result.Model)
		}
		if !result.MeasurementValid {
			t.Fatalf("invalid: %v", result.InvalidReasons)
		}
		for _, name := range []string{"decode_tok_per_s", "itl_ms_p50", "itl_ms_p99"} {
			if _, ok := result.Metric(name, "short"); !ok {
				t.Fatalf("no %s metric: %+v", name, result.Metrics)
			}
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
	if !c.Comparable || !c.MeasurementValid || len(c.Deltas) == 0 || c.Baseline.JobID != "a" || c.Candidate.JobID != "b" {
		t.Fatalf("comparison %+v", c)
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	r, err := parseNvidiaSMI([]byte("NVIDIA RTX 5090, 575.51, 1200, 37, 12, [N/A], 61, 2400, 0x0000000000000004\n"))
	if err != nil || r.Driver != "575.51" {
		t.Fatalf("%+v %v", r, err)
	}
	got := r.GPUs[0].Values
	if got["vram_used_mib"] != 1200 || got["mem_util_percent"] != 12 || got["throttled"] != 1 {
		t.Fatalf("values %v", got)
	}
	if _, ok := got["power_w"]; ok {
		t.Fatal("unsupported power reading was recorded")
	}
	if _, err := parseNvidiaSMI([]byte("NVIDIA RTX 5090, 1200, 37, 575.51\n")); err == nil {
		t.Fatal("short line was accepted")
	}
}

func TestParseThrottled(t *testing.T) {
	for in, want := range map[string]float64{"0x0000000000000001": 0, "0x0000000000000020": 1, "0x0000000000000101": 0} {
		if got, err := parseThrottled(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
}

func TestReadDmon(t *testing.T) {
	var got []pcieReading
	readDmon(strings.NewReader("# gpu  rxpci  txpci\n# Idx   MB/s   MB/s\n    0     12      3\n    1      0     40\n"), func(p pcieReading) { got = append(got, p) })
	if len(got) != 2 || got[0] != (pcieReading{0, 12, 3}) || got[1] != (pcieReading{1, 0, 40}) {
		t.Fatalf("%+v", got)
	}
}

func TestHostReadings(t *testing.T) {
	prev, err := parseProcStat([]byte("cpu  10 0 10 80 0 0 0 0 0 0\ncpu0 5 0 5 40 0 0 0 0 0 0\ncpu1 5 0 5 40 0 0 0 0 0 0\nintr 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := parseProcStat([]byte("cpu  60 0 10 130 0 0 0 0 0 0\ncpu0 55 0 5 40 0 0 0 0 0 0\ncpu1 5 0 5 90 0 0 0 0 0 0\n"))
	all, busiest, ok := cpuUtil(prev, cur)
	if !ok || all != 50 || busiest != 100 {
		t.Fatalf("all %v busiest %v ok %v", all, busiest, ok)
	}
	used, err := parseMeminfo([]byte("MemTotal:       4194304 kB\nMemFree:  1 kB\nMemAvailable:   1048576 kB\n"))
	if err != nil || used != 3072 {
		t.Fatalf("used %v %v", used, err)
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if percentile(v, 0.5) != 5 || percentile(v, 0.95) != 10 || percentile(v, 0) != 1 {
		t.Fatal(percentile(v, 0.5), percentile(v, 0.95))
	}
}

func TestEnergyPerToken(t *testing.T) {
	gpu := func(at int64, gpu string, w float64) Sample {
		return Sample{AtMS: at, Source: "gpu", Name: "power_w", Case: "c", Value: w, Labels: map[string]string{"gpu": gpu}}
	}
	samples := []Sample{
		gpu(0, "0", 100), gpu(0, "1", 100),
		gpu(1000, "0", 150), gpu(1000, "1", 50),
		gpu(2000, "0", 100), gpu(2000, "1", 100),
		{Source: "gpu", Name: "power_w", Case: "other", Value: 1000, AtMS: 3000},
		{Source: "harness", Name: "tokens_out", Case: "c", Value: 40},
	}
	m, ok := energyPerToken(samples, "c")
	if !ok || m.Value != 10 || m.Samples != 1 {
		t.Fatalf("%+v", m)
	}
}

func TestSourceVersion(t *testing.T) {
	dir := t.TempDir()
	upstream, work := filepath.Join(dir, "upstream"), filepath.Join(dir, "work")
	git := func(repo string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(upstream, 0o755)
	git(upstream, "init", "-q")
	os.WriteFile(filepath.Join(upstream, "a"), []byte("1"), 0o644)
	git(upstream, "add", "a")
	git(upstream, "commit", "-qm", "one")
	git(upstream, "tag", "-a", "v1.0.0", "-m", "v1.0.0")
	git(upstream, "tag", "nightly")
	os.WriteFile(filepath.Join(upstream, "a"), []byte("2"), 0o644)
	git(upstream, "commit", "-qam", "two")
	git(dir, "clone", "-q", upstream, work)

	ctx := context.Background()
	if v := sourceVersion(ctx, work); !strings.HasPrefix(v, "v1.0.0-1-g") {
		t.Fatalf("upstream: %q", v)
	}
	os.WriteFile(filepath.Join(work, "a"), []byte("3"), 0o644)
	if v := sourceVersion(ctx, work); !strings.HasSuffix(v, " dirty") || strings.HasPrefix(v, "v") {
		t.Fatalf("dirty: %q", v)
	}
	git(work, "commit", "-qam", "patch")
	if v := sourceVersion(ctx, work); len(v) != 12 {
		t.Fatalf("local commit: %q", v)
	}
}

func TestDigestPathFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "real"), 0o755)
	os.WriteFile(filepath.Join(dir, "real", "w.safetensors"), []byte("w"), 0o644)
	os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link"))
	real, err1 := digestPath(filepath.Join(dir, "real"))
	link, err2 := digestPath(filepath.Join(dir, "link"))
	if err1 != nil || err2 != nil || real != link {
		t.Fatalf("%s %v / %s %v", real, err1, link, err2)
	}
}
