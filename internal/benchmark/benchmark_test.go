package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

// fakeStarter records the command it was asked to start and returns a process
// that does nothing: the adapter talks to an httptest server instead of to a
// real runtime, so the whole measurement path is testable without a GPU.
type fakeStarter struct {
	argv  []string
	err   error
	stops atomic.Int32
}

type fakeProcess struct{ stops *atomic.Int32 }

func (p fakeProcess) Wait() error                { return nil }
func (p fakeProcess) Stop(context.Context) error { p.stops.Add(1); return nil }

func (f *fakeStarter) Start(_ context.Context, argv []string, logPath string) (Process, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.argv = argv
	if err := os.WriteFile(logPath, []byte("fake runtime\n"), 0o644); err != nil {
		return nil, err
	}
	return fakeProcess{stops: &f.stops}, nil
}

// fakeGPU returns a scripted GPU reading.
type fakeGPU struct {
	sample GPUSample
	err    error
}

func (f fakeGPU) Sample(context.Context) (GPUSample, []string, error) {
	if f.err != nil {
		return GPUSample{}, nil, f.err
	}
	return f.sample, []string{"0, NVIDIA GeForce RTX 5090, 4200, 32768, 87, 580.1"}, nil
}

func goodGPU() fakeGPU {
	return fakeGPU{sample: GPUSample{
		Driver: "580.1",
		GPUs:   []GPUState{{Index: 0, Model: "NVIDIA GeForce RTX 5090", UsedMiB: 4200, TotalMiB: 32768, UtilPercent: 87}},
	}}
}

// stubAdapter stands in for a runtime when the test is about the harness's
// reaction to an engine that cannot serve, rather than about HTTP details.
type stubAdapter struct {
	argv     []string
	ready    func(context.Context) error
	complete func(context.Context, runtime.Request) (runtime.Completion, error)
	metrics  func(context.Context) ([]measurement.Metric, error)
}

func (s stubAdapter) Name() string           { return "stub" }
func (s stubAdapter) ReservedArgs() []string { return nil }
func (s stubAdapter) Argv() []string         { return s.argv }
func (s stubAdapter) BaseURL() string        { return "http://127.0.0.1:1" }
func (s stubAdapter) Ready(ctx context.Context) error {
	if s.ready == nil {
		return nil
	}
	return s.ready(ctx)
}
func (s stubAdapter) Complete(ctx context.Context, req runtime.Request) (runtime.Completion, error) {
	if s.complete == nil {
		return runtime.Completion{TTFT: time.Millisecond, Total: 2 * time.Millisecond, PromptTokens: 1, CompletionTokens: 2}, nil
	}
	return s.complete(ctx, req)
}
func (s stubAdapter) Metrics(ctx context.Context) ([]measurement.Metric, error) {
	if s.metrics == nil {
		return nil, nil
	}
	return s.metrics(ctx)
}

// harness is one run's inputs.
type harness struct {
	starter  *fakeStarter
	gpu      GPUReader
	repoRoot string
	dir      string
	spec     job.Spec
	override runtime.Adapter
	// sampleInterval keeps the periodic collectors firing inside a test that
	// finishes in milliseconds; production uses DefaultSampleInterval.
	sampleInterval time.Duration
}

// newHarness starts a fake runtime, builds the job spec against its port, and
// returns everything Run needs.
func newHarness(t *testing.T, handler http.HandlerFunc, mutate func(*job.Spec), gpu GPUReader, starter *fakeStarter) harness {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := t.TempDir()
	s := job.Spec{
		Kind:    job.KindBenchmark,
		Model:   job.Model{ID: "qwen38-27b"},
		Runtime: job.Runtime{Engine: "llamacpp", Image: "ghcr.io/x/y@sha256:" + strings.Repeat("a", 64), Ready: job.Ready{Port: port, TimeoutSeconds: 2}},
		Workload: job.Workload{
			Cases: []job.Case{{Name: "short", PromptText: "hello", MaxTokens: 8, Repeats: 2}},
		},
		Metrics: job.Metrics{Collectors: []job.Collector{job.CollectorHarness, job.CollectorRuntime, job.CollectorGPU}},
		Output:  job.Output{Dir: "experiments/qwen38-27b"},
	}
	if mutate != nil {
		mutate(&s)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("test spec: %v", err)
	}
	if starter == nil {
		starter = &fakeStarter{}
	}
	if gpu == nil {
		gpu = goodGPU()
	}
	return harness{starter: starter, gpu: gpu, repoRoot: repoRoot, dir: filepath.Join(t.TempDir(), "out"), spec: s}
}

func (h harness) adapter(t *testing.T) runtime.Adapter {
	t.Helper()
	if h.override != nil {
		return h.override
	}
	u, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(h.spec.Runtime.Ready.Port))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	a, err := runtime.New(h.spec.Runtime.Engine, runtime.Options{
		ModelID: h.spec.Model.ID, ModelPath: "/models/x", Host: u.Hostname(), Port: port,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// run executes the job and returns the outcome, failing the test on error.
func (h harness) run(t *testing.T) Outcome {
	t.Helper()
	outcome, err := h.tryRun(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return outcome
}

func (h harness) tryRun(t *testing.T) (Outcome, error) {
	t.Helper()
	interval := h.sampleInterval
	if interval == 0 {
		interval = time.Millisecond
	}
	return Run(context.Background(), Config{
		Spec:           h.spec,
		JobID:          "2026-09-27-issue42",
		RepoRoot:       h.repoRoot,
		OutputDir:      h.dir,
		ModelPath:      "/models/x",
		ModelDigest:    strings.Repeat("d", 64),
		RequestTimeout: 5 * time.Second,
		SampleInterval: interval,
	}, h.adapter(t), Seams{Starter: h.starter, GPU: h.gpu, Now: time.Now, ProbeTimeout: 250 * time.Millisecond})
}

// llamaOK answers readiness, two streamed tokens with timings, and metrics.
func llamaOK(tokens int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/completion":
			// A real request takes longer than the sampler's interval; a
			// couple of milliseconds keeps that true without slowing the
			// suite down.
			time.Sleep(2 * time.Millisecond)
			for i := range tokens {
				fmt.Fprintf(w, "data: {\"content\":\"tok%d\"}\n\n", i)
			}
			fmt.Fprintf(w, "data: {\"content\":\"\",\"stop\":true,\"timings\":{\"prompt_n\":7,\"predicted_n\":%d,\"predicted_ms\":10}}\n\n", tokens)
		case "/metrics":
			fmt.Fprint(w, "llamacpp:kv_cache_usage_ratio 0.5\nllamacpp:requests_processing 1\n")
		default:
			http.NotFound(w, r)
		}
	}
}

func TestRunProducesAVerifiedResultDirectory(t *testing.T) {
	h := newHarness(t, llamaOK(3), nil, nil, nil)
	outcome := h.run(t)

	// The directory verifies: result.json plus the sidecar digests.
	if _, err := measurement.VerifyResultDir(outcome.Dir); err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, name := range []string{"jobspec.yaml", "result.json", "series.jsonl", "README.md", rawDir + "/" + runtimeLogName, rawDir + "/" + nvidiaRawName} {
		if _, err := os.Stat(filepath.Join(outcome.Dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	r := outcome.Result
	if !r.MeasurementValid {
		t.Fatalf("measurement is invalid: %v", r.InvalidReasons)
	}
	if r.JobID != "2026-09-27-issue42" || r.Kind != "benchmark" {
		t.Fatalf("identity = %+v", r)
	}
	if r.TrustLevel != measurement.TrustUnverifiedDriver {
		t.Fatalf("trust level = %q", r.TrustLevel)
	}
	if r.Runtime.BuildDigest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("build digest = %q", r.Runtime.BuildDigest)
	}
	if r.Runtime.SpecDigest == "" || r.ResultDigest == "" || r.SeriesDigest == "" || r.JobSpecFileDigest == "" {
		t.Fatalf("missing digests: %+v", r)
	}
	if len(r.Environment.GPUs) != 1 || r.Environment.GPUs[0].Model != "NVIDIA GeForce RTX 5090" || r.Environment.Driver != "580.1" {
		t.Fatalf("environment = %+v", r.Environment)
	}
	if len(r.Collectors) != 3 {
		t.Fatalf("collectors = %+v", r.Collectors)
	}
	for _, c := range r.Collectors {
		if c.Gaps != 0 {
			t.Errorf("collector %s reported gaps %d", c.Name, c.Gaps)
		}
	}

	// Harness metrics are per case and derived from the run; the GPU and
	// runtime sources stay distinct from the harness measurement.
	byKey := map[string]measurement.Metric{}
	for _, m := range r.Metrics {
		byKey[string(m.Source)+"/"+m.Name] = m
	}
	ttft, ok := byKey["harness/ttft_ms"]
	if !ok {
		t.Fatalf("no harness ttft_ms in %v", r.Metrics)
	}
	if ttft.Labels["case"] != "short" || ttft.Samples != 2 || ttft.Stats == nil {
		t.Fatalf("ttft metric = %+v", ttft)
	}
	decode, ok := byKey["harness/decode_tok_per_s"]
	if !ok {
		t.Fatal("no harness decode rate: token counts must come from the runtime's timings")
	}
	if decode.Stats.Median <= 0 {
		t.Fatalf("decode metric = %+v", decode)
	}
	if _, ok := byKey["external_gpu/vram_used_mib"]; !ok {
		t.Fatalf("no GPU metric in %v", r.Metrics)
	}
	// Runtime metrics are never summarized: a single reading of a cumulative
	// counter or a sliding-window rate cannot be labelled with one case.
	// They stay in the series so the Agent can read the trajectory.
	for key := range byKey {
		if strings.HasPrefix(key, "runtime/") {
			t.Fatalf("runtime metric %s was summarized", key)
		}
	}
	runtimeCases := map[string]bool{}
	for _, s := range r.Series {
		if s.Source == measurement.SourceRuntime && s.Name == "llamacpp:kv_cache_usage_ratio" {
			runtimeCases[s.Labels["case"]] = true
		}
	}
	// One reading before the workload (no case label) and one while the case
	// ran: the label says which case was running, and is absent otherwise.
	if !runtimeCases["short"] || len(runtimeCases) < 2 {
		t.Fatalf("runtime series cases = %v (series %+v)", runtimeCases, r.Series)
	}

	// Series carry the case label so two cases cannot share an identity.
	var found bool
	for _, s := range r.Series {
		if s.Name == "ttft_ms" {
			found = true
			if s.Labels["case"] != "short" || len(s.Points) != 2 {
				t.Fatalf("ttft series = %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("no ttft series")
	}

	// The runtime was started with the adapter's argv and stopped again.
	argv := strings.Join(h.starter.argv, " ")
	if !strings.Contains(argv, "/models/x") || !strings.Contains(argv, strconv.Itoa(h.spec.Runtime.Ready.Port)) {
		t.Fatalf("argv = %v", h.starter.argv)
	}
	if h.starter.stops.Load() == 0 {
		t.Fatal("the runtime was not stopped")
	}

	// series.jsonl is line-delimited JSON in observation order.
	raw, err := os.ReadFile(filepath.Join(outcome.Dir, seriesFileNameForTest))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 10 {
		t.Fatalf("series.jsonl has %d lines", len(lines))
	}
	names := map[string]bool{}
	for _, line := range lines {
		var sample Sample
		if err := json.Unmarshal([]byte(line), &sample); err != nil {
			t.Fatalf("series.jsonl line %q: %v", line, err)
		}
		names[sample.Name] = true
	}
	for _, want := range []string{"ttft_ms", "decode_step_ms", "vram_used_mib", "llamacpp:kv_cache_usage_ratio"} {
		if !names[want] {
			t.Errorf("series.jsonl has no %s sample: %v", want, names)
		}
	}
}

const seriesFileNameForTest = "series.jsonl"

func TestRunKeepsMultipleCasesDistinct(t *testing.T) {
	h := newHarness(t, llamaOK(2), func(s *job.Spec) {
		s.Workload.Cases = []job.Case{
			{Name: "short", PromptText: "a", MaxTokens: 4, Repeats: 1},
			{Name: "long", PromptText: "b", MaxTokens: 8, Repeats: 1},
		}
	}, nil, nil)
	outcome := h.run(t)
	seen := map[string]bool{}
	for _, s := range outcome.Result.Series {
		if s.Name == "ttft_ms" {
			seen[s.Labels["case"]] = true
		}
	}
	if !seen["short"] || !seen["long"] {
		t.Fatalf("series cases = %v", seen)
	}
}

func TestRunReadsPromptFromTheRepository(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/completion":
			fmt.Fprint(w, `data: {"content":"x","stop":true,"timings":{"prompt_n":1,"predicted_n":1,"predicted_ms":1}}`+"\n\n")
		default:
			fmt.Fprint(w, "")
		}
	}, func(s *job.Spec) {
		s.Workload.Cases = []job.Case{{Name: "file", Prompt: "prompts/short.txt", MaxTokens: 4, Repeats: 1}}
		s.Metrics.Collectors = []job.Collector{job.CollectorHarness}
	}, nil, nil)
	if err := os.MkdirAll(filepath.Join(h.repoRoot, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.repoRoot, "prompts", "short.txt"), []byte("from a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome := h.run(t)
	if !outcome.Result.MeasurementValid {
		t.Fatalf("invalid: %v", outcome.Result.InvalidReasons)
	}
	// A missing prompt file is a case error, not a crash: the run still
	// produces evidence and says what went wrong.
	h2 := newHarness(t, llamaOK(2), func(s *job.Spec) {
		s.Workload.Cases = []job.Case{{Name: "missing", Prompt: "prompts/nope.txt", MaxTokens: 4, Repeats: 1}}
		s.Metrics.Collectors = []job.Collector{job.CollectorHarness}
	}, nil, nil)
	outcome2 := h2.run(t)
	if outcome2.Result.MeasurementValid || len(outcome2.Result.InvalidReasons) == 0 {
		t.Fatalf("a missing prompt did not invalidate the run: %+v", outcome2.Result)
	}
}

func TestRunIsInvalidWhenTokenCountsAreMissing(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/completion":
			fmt.Fprint(w, "data: {\"content\":\"a\"}\n\ndata: {\"content\":\"b\"}\n\n")
		default:
			fmt.Fprint(w, "")
		}
	}, func(s *job.Spec) { s.Metrics.Collectors = []job.Collector{job.CollectorHarness} }, nil, nil)
	outcome := h.run(t)
	r := outcome.Result
	if r.MeasurementValid {
		t.Fatal("a run without token counts was reported valid")
	}
	if !strings.Contains(strings.Join(r.InvalidReasons, " "), "no token count") {
		t.Fatalf("reasons = %v", r.InvalidReasons)
	}
	// The invalid run is still a sealed, verifiable result.
	if _, err := measurement.VerifyResultDir(outcome.Dir); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestRunIsInvalidWhenACollectorFails(t *testing.T) {
	h := newHarness(t, llamaOK(2), func(s *job.Spec) {
		s.Metrics.Collectors = []job.Collector{job.CollectorHarness, job.CollectorGPU}
	}, fakeGPU{err: errors.New("nvidia-smi: command not found")}, nil)
	outcome := h.run(t)
	r := outcome.Result
	if r.MeasurementValid {
		t.Fatal("a missing GPU collector was reported valid")
	}
	var gapped bool
	for _, c := range r.Collectors {
		if c.Name == "nvidia" && c.Gaps > 0 {
			gapped = true
		}
	}
	if !gapped {
		t.Fatalf("collectors = %+v", r.Collectors)
	}
	if !strings.Contains(strings.Join(r.InvalidReasons, " "), "nvidia collector failed") {
		t.Fatalf("reasons = %v", r.InvalidReasons)
	}
}

func TestRunFailsWhenTheRuntimeDoesNotStart(t *testing.T) {
	h := newHarness(t, llamaOK(2), nil, nil, &fakeStarter{err: errors.New("exec: not found")})
	if _, err := h.tryRun(t); !errors.Is(err, ErrNoResult) {
		t.Fatalf("error = %v, want ErrNoResult", err)
	}
}

func TestRunFailsWhenReadinessTimesOut(t *testing.T) {
	h := newHarness(t, llamaOK(2), func(s *job.Spec) { s.Runtime.Ready.TimeoutSeconds = 1 }, nil, nil)
	h.override = stubAdapter{argv: []string{"fake"},
		ready: func(context.Context) error { return fmt.Errorf("%w: loading", runtime.ErrNotReady) }}
	if _, err := h.tryRun(t); !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
}

func TestRunFailsWhenTheRuntimeCannotServe(t *testing.T) {
	h := newHarness(t, llamaOK(2), nil, nil, nil)
	h.override = stubAdapter{argv: []string{"fake"},
		ready: func(context.Context) error { return fmt.Errorf("%w: engine failed", runtime.ErrUnavailable) }}
	if _, err := h.tryRun(t); !errors.Is(err, ErrNoResult) {
		t.Fatalf("error = %v, want ErrNoResult", err)
	}
}

func TestRunCountsARequestFailureAsInvalidNotAsASuccess(t *testing.T) {
	h := newHarness(t, llamaOK(2), func(s *job.Spec) { s.Metrics.Collectors = []job.Collector{job.CollectorHarness} }, nil, nil)
	h.override = stubAdapter{argv: []string{"fake"},
		complete: func(context.Context, runtime.Request) (runtime.Completion, error) {
			return runtime.Completion{}, errors.New("connection reset")
		}}
	outcome := h.run(t)
	if outcome.Result.MeasurementValid {
		t.Fatal("a run whose requests all failed was reported valid")
	}
	if !strings.Contains(strings.Join(outcome.Result.InvalidReasons, " "), "connection reset") {
		t.Fatalf("reasons = %v", outcome.Result.InvalidReasons)
	}
}

func TestSummarizeUsesNearestRank(t *testing.T) {
	st := summarize([]float64{3, 1, 2})
	if st.Count != 3 || st.Median != 2 || st.Min != 1 || st.Max != 3 || st.Mean != 2 {
		t.Fatalf("stats = %+v", st)
	}
	if st.P90 != 3 || st.P50 != 2 {
		t.Fatalf("quantiles = %+v", st)
	}
	if st.Sum != 6 {
		t.Fatalf("sum = %v", st.Sum)
	}
	if got := summarize(nil); got.Count != 0 {
		t.Fatalf("empty stats = %+v", got)
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	sample, err := parseNvidiaSMI([]byte("0, NVIDIA GeForce RTX 5090, 4200, 32768, 87, 580.1\n1, NVIDIA GeForce RTX 3060 Ti, 100, 8192, 0, 580.1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sample.GPUs) != 2 || sample.GPUs[0].UsedMiB != 4200 || sample.GPUs[1].Model != "NVIDIA GeForce RTX 3060 Ti" {
		t.Fatalf("sample = %+v", sample)
	}
	if _, err := parseNvidiaSMI([]byte("garbage\n")); err == nil {
		t.Fatal("expected a parse error")
	}
	if _, err := parseNvidiaSMI(nil); err == nil {
		t.Fatal("expected an error when no GPU is reported")
	}
}

func TestSampleMarshalIsOneLine(t *testing.T) {
	b, err := Sample{Name: "ttft_ms", Source: measurement.SourceHarness, Unit: "ms", Value: 1.5, Repeat: 2}.marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "\n") != 1 || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("line = %q", b)
	}
}

func TestRunAbortsWithoutWritingAResultWhenCancelled(t *testing.T) {
	// A controller that stops the job (scale down, shutdown) must not publish:
	// the run ends with no result at all.
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/completion":
			time.Sleep(50 * time.Millisecond)
			fmt.Fprint(w, `data: {"content":"x","stop":true,"timings":{"prompt_n":1,"predicted_n":1,"predicted_ms":1}}`+"\n\n")
		default:
			fmt.Fprint(w, "")
		}
	}, func(s *job.Spec) { s.Metrics.Collectors = []job.Collector{job.CollectorHarness} }, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Run(ctx, Config{
		Spec: h.spec, JobID: "j", RepoRoot: h.repoRoot, OutputDir: h.dir, ModelPath: "/models/x",
		RequestTimeout: time.Second, SampleInterval: time.Millisecond,
	}, h.adapter(t), Seams{Starter: h.starter, GPU: h.gpu, Now: time.Now})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("error = %v, want ErrAborted", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "result.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an aborted run wrote a result: %v", err)
	}
}

func TestRunReportsTheResolvedInputs(t *testing.T) {
	h := newHarness(t, llamaOK(2), func(s *job.Spec) {
		s.Metrics.Collectors = []job.Collector{job.CollectorHarness}
		s.Workload.Cases = []job.Case{{Name: "inline", PromptText: "hello", MaxTokens: 4, Repeats: 1}}
	}, nil, nil)
	outcome := h.run(t)
	in := outcome.Result.Inputs
	if in.ModelID != "qwen38-27b" || in.ModelPath != "/models/x" || in.ModelDigest != strings.Repeat("d", 64) {
		t.Fatalf("inputs = %+v", in)
	}
	if in.Prompts["inline"] != measurement.Digest([]byte("hello")) {
		t.Fatalf("prompt digest = %q", in.Prompts["inline"])
	}
	if in.WorkloadDigest == "" {
		t.Fatal("the workload digest was not recorded")
	}
	// A different workload (here a different token budget) must produce a
	// different digest: the comparison relies on it.
	other, err := workloadDigest(func() job.Spec {
		o := h.spec
		o.Workload.Cases = []job.Case{{Name: "inline", PromptText: "hello", MaxTokens: 8, Repeats: 1}}
		return o
	}())
	if err != nil {
		t.Fatal(err)
	}
	if other == in.WorkloadDigest {
		t.Fatal("the workload digest ignores the token budget")
	}
}

func TestReadinessProbeIsBounded(t *testing.T) {
	// An engine that accepts the connection and then stops answering must not
	// hang the readiness loop: every probe carries its own deadline.
	h := newHarness(t, llamaOK(2), func(s *job.Spec) { s.Runtime.Ready.TimeoutSeconds = 1 }, nil, nil)
	probes := 0
	h.override = stubAdapter{argv: []string{"fake"},
		ready: func(ctx context.Context) error {
			probes++
			<-ctx.Done()
			return ctx.Err()
		}}
	start := time.Now()
	_, err := h.tryRun(t)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("readiness took %s: the probe was not bounded", elapsed)
	}
	if probes < 2 {
		t.Fatalf("probes = %d: the loop stopped after the first probe", probes)
	}
}

func TestEnvironmentIsNotInflatedBySamples(t *testing.T) {
	// One GPU sampled many times is still one GPU, and the environment comes
	// from the identity probe rather than from workload samples.
	h := newHarness(t, llamaOK(2), nil, nil, nil)
	outcome := h.run(t)
	env := outcome.Result.Environment
	if len(env.GPUs) != 1 || env.GPUs[0].Count != 1 {
		t.Fatalf("environment = %+v", env)
	}
	if env.Driver != "580.1" {
		t.Fatalf("driver = %q", env.Driver)
	}
}

func TestUnpinnedModelDigestInvalidatesTheRun(t *testing.T) {
	h := newHarness(t, llamaOK(2), func(s *job.Spec) {
		s.Metrics.Collectors = []job.Collector{job.CollectorHarness}
	}, nil, nil)
	outcome, err := Run(context.Background(), Config{
		Spec: h.spec, JobID: "j", RepoRoot: h.repoRoot, OutputDir: h.dir, ModelPath: "/models/x",
		RequestTimeout: time.Second, SampleInterval: time.Millisecond,
	}, h.adapter(t), Seams{Starter: h.starter, GPU: h.gpu, Now: time.Now, ProbeTimeout: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Result.MeasurementValid {
		t.Fatal("a run without a pinned model digest was reported valid")
	}
	if !strings.Contains(strings.Join(outcome.Result.InvalidReasons, " "), "model digest") {
		t.Fatalf("reasons = %v", outcome.Result.InvalidReasons)
	}
}

func TestPromptBytesAreFrozenBeforeMeasuring(t *testing.T) {
	// The file changes between resolving the inputs and measuring: the run
	// must send and digest the same bytes.
	var h harness
	h = newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/completion":
			if err := os.WriteFile(filepath.Join(h.repoRoot, "prompts", "p.txt"), []byte("changed"), 0o644); err != nil {
				t.Errorf("rewrite prompt: %v", err)
			}
			fmt.Fprint(w, `data: {"content":"x","stop":true,"timings":{"prompt_n":1,"predicted_n":1,"predicted_ms":1}}`+"\n\n")
		default:
			fmt.Fprint(w, "")
		}
	}, func(s *job.Spec) {
		s.Metrics.Collectors = []job.Collector{job.CollectorHarness}
		s.Workload.Cases = []job.Case{{Name: "file", Prompt: "prompts/p.txt", MaxTokens: 4, Repeats: 1}}
	}, nil, nil)
	if err := os.MkdirAll(filepath.Join(h.repoRoot, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.repoRoot, "prompts", "p.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome := h.run(t)
	if got := outcome.Result.Inputs.Prompts["file"]; got != measurement.Digest([]byte("original")) {
		t.Fatalf("prompt digest = %q, want the frozen original bytes", got)
	}
}

func TestUnrequestedCollectorsAreNotSampled(t *testing.T) {
	// A runtime-only job must not run nvidia-smi every interval, and must not
	// publish GPU metrics the operator did not ask for.
	calls := 0
	gpu := countingGPU{onSample: func() { calls++ }}
	h := newHarness(t, llamaOK(2), func(s *job.Spec) {
		s.Metrics.Collectors = []job.Collector{job.CollectorHarness, job.CollectorRuntime}
	}, gpu, nil)
	outcome := h.run(t)
	// The identity probe reads the GPU once; the periodic sampler must not.
	if calls != 1 {
		t.Fatalf("nvidia-smi was called %d times for a runtime-only job", calls)
	}
	for _, m := range outcome.Result.Metrics {
		if m.Source == measurement.SourceExternalGPU {
			t.Fatalf("unrequested GPU metric %s was recorded", m.Name)
		}
	}
}

type countingGPU struct{ onSample func() }

func (c countingGPU) Sample(context.Context) (GPUSample, []string, error) {
	c.onSample()
	return goodGPU().sample, nil, nil
}

func TestRequiredCollectorNeedsAWorkloadSample(t *testing.T) {
	// The workload finishes before the sampler's first tick: the startup
	// readings cannot certify the workload, so the run is invalid rather than
	// reporting a collector that never observed it.
	h := newHarness(t, llamaOK(1), func(s *job.Spec) {
		s.Metrics.Collectors = []job.Collector{job.CollectorHarness, job.CollectorGPU}
		s.Workload.Cases = []job.Case{{Name: "tiny", PromptText: "a", MaxTokens: 1, Repeats: 1}}
	}, nil, nil)
	h.sampleInterval = time.Hour
	outcome := h.run(t)
	if outcome.Result.MeasurementValid {
		t.Fatal("a required collector that never sampled the workload was accepted")
	}
	if !strings.Contains(strings.Join(outcome.Result.InvalidReasons, " "), "no sample while the workload ran") {
		t.Fatalf("reasons = %v", outcome.Result.InvalidReasons)
	}
}

func TestWorkloadDigestIsCanonical(t *testing.T) {
	base := func() job.Spec {
		return job.Spec{
			Kind:  job.KindBenchmark,
			Model: job.Model{ID: "m"},
			Runtime: job.Runtime{
				Engine: "llamacpp", Image: "img", Ready: job.Ready{Port: 1},
			},
			Workload: job.Workload{
				Cases: []job.Case{{Name: "c", Prompt: "a.txt", MaxTokens: 8, Repeats: 1}},
			},
			Metrics: job.Metrics{Collectors: []job.Collector{job.CollectorHarness, job.CollectorRuntime}},
			Output:  job.Output{Dir: "experiments/m"},
		}
	}
	digest := func(s job.Spec) string {
		t.Helper()
		d, err := workloadDigest(s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	want := digest(base())

	// Defaults spelled out are the same workload.
	s := base()
	s.Workload.Cases[0].Repeats = 0
	s.Workload.Concurrency = 1
	if got := digest(s); got != want {
		t.Error("repeats 0 / concurrency 1 differ from repeats 1 / concurrency 0")
	}
	// The collector list is a set.
	s = base()
	s.Metrics.Collectors = []job.Collector{job.CollectorRuntime, job.CollectorHarness}
	if got := digest(s); got != want {
		t.Error("collector order changed the workload digest")
	}
	// How the prompt was delivered is not part of the workload identity; the
	// bytes are recorded in Inputs.Prompts.
	s = base()
	s.Workload.Cases[0].Prompt = ""
	s.Workload.Cases[0].PromptText = "same bytes"
	if got := digest(s); got != want {
		t.Error("the prompt source form changed the workload digest")
	}
	// A real change does move it.
	for _, mutate := range []func(*job.Spec){
		func(s *job.Spec) { s.Workload.Cases[0].MaxTokens = 9 },
		func(s *job.Spec) { s.Workload.Cases[0].Repeats = 2 },
		func(s *job.Spec) { s.Workload.Cases[0].Name = "other" },
		func(s *job.Spec) { s.Metrics.Collectors = []job.Collector{job.CollectorHarness} },
		func(s *job.Spec) { v := 0.0; s.Workload.Sampling = &job.Sampling{Temperature: &v} },
	} {
		s := base()
		mutate(&s)
		if digest(s) == want {
			t.Errorf("a workload change did not move the digest: %+v", s.Workload)
		}
	}
}
