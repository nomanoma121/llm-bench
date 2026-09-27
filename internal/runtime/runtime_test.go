package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/measurement"
)

// newAdapter starts a fake runtime and returns an adapter bound to it, so the
// tests exercise the real HTTP paths without a GPU.
func newAdapter(t *testing.T, engine string, handler http.Handler) Adapter {
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
	a, err := New(engine, Options{
		ModelID:   "qwen38-27b",
		ModelPath: "/models/qwen38-27b",
		Host:      u.Hostname(),
		Port:      port,
		Args:      []string{"-ngl", "99"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// sse writes server-sent events, flushing after each one so the client can
// observe the arrival time of every chunk.
func sse(w http.ResponseWriter, events ...string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		fmt.Fprintf(w, "data: %s\n\n", e)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(3 * time.Millisecond)
	}
}

func TestNewUnknownEngine(t *testing.T) {
	if _, err := New("vllm", Options{}); err == nil {
		t.Fatal("expected an unknown engine to be rejected")
	}
}

func TestArgvPinsIdentityFlags(t *testing.T) {
	cases := []struct {
		engine string
		want   []string
	}{
		{"llamacpp", []string{"llama-server", "--model /p", "--host h", "--port 1234", "--alias m", "--metrics"}},
		{"freetoken", []string{"ft serve", "--model /p", "--host h", "--port 1234", "--served-model-name m"}},
	}
	for _, tc := range cases {
		a, err := New(tc.engine, Options{ModelID: "m", ModelPath: "/p", Host: "h", Port: 1234, Args: []string{"--tuning"}})
		if err != nil {
			t.Fatal(err)
		}
		argv := strings.Join(a.Argv(), " ")
		for _, want := range tc.want {
			if !strings.Contains(argv, want) {
				t.Errorf("%s argv %q does not contain %q", tc.engine, argv, want)
			}
		}
		// Tuning flags come last, so they can never replace an identity flag.
		if i, j := strings.Index(argv, "--tuning"), strings.Index(argv, "--model"); i < j {
			t.Errorf("%s: tuning args must come after the adapter-owned ones: %q", tc.engine, argv)
		}
		// The flags that decide what is measured must be reserved, otherwise a
		// job could point the runtime somewhere else.
		for _, mustReserve := range []string{"--model", "--host", "--port"} {
			var found bool
			for _, r := range a.ReservedArgs() {
				if r == mustReserve {
					found = true
				}
			}
			if !found {
				t.Errorf("%s does not reserve %s", tc.engine, mustReserve)
			}
		}
	}
}

func TestLlamaCppReady(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusServiceUnavailable, "", ErrNotReady},
		{http.StatusOK, `{"status":"ok"}`, nil},
		{http.StatusOK, `{"status":"loading model"}`, ErrNotReady},
		{http.StatusInternalServerError, "", nil}, // any error, not ErrNotReady
	}
	for _, tc := range cases {
		a := newAdapter(t, "llamacpp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			w.WriteHeader(tc.status)
			fmt.Fprintln(w, tc.body)
		}))
		err := a.Ready(context.Background())
		switch {
		case tc.want == nil && tc.status == http.StatusInternalServerError:
			if err == nil {
				t.Fatal("expected an error for a 500")
			}
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Fatalf("status %d body %q: got %v, want %v", tc.status, tc.body, err, tc.want)
		case tc.want == nil && err != nil:
			t.Fatalf("status %d: %v", tc.status, err)
		}
	}
}

func TestLlamaCppComplete(t *testing.T) {
	a := newAdapter(t, "llamacpp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/completion" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		sse(w,
			`{"content":"Hello","stop":false}`,
			`{"content":" world","stop":false}`,
			`{"content":"!","stop":true,"timings":{"prompt_n":12,"predicted_n":3,"predicted_ms":60}}`,
		)
	}))
	got, err := a.Complete(context.Background(), Request{Prompt: "hi", MaxTokens: 8})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Hello world!" {
		t.Fatalf("content = %q", got.Content)
	}
	if got.PromptTokens != 12 || got.CompletionTokens != 3 {
		t.Fatalf("tokens = %d/%d", got.PromptTokens, got.CompletionTokens)
	}
	if len(got.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(got.Steps))
	}
	if got.TTFT <= 0 || got.Total < got.TTFT {
		t.Fatalf("timings = ttft %s total %s", got.TTFT, got.Total)
	}
	if got.DecodeTokensPerSecond() <= 0 {
		t.Fatal("decode rate was not derived")
	}
}

func TestLlamaCppMetrics(t *testing.T) {
	a := newAdapter(t, "llamacpp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `# HELP llamacpp:prompt_tokens_total Number of prompt tokens
# TYPE llamacpp:prompt_tokens_total counter
llamacpp:prompt_tokens_total 120
llamacpp:kv_cache_usage_ratio{device="0"} 0.42
llamacpp:requests_processing 2
llamacpp:predicted_tokens_seconds 44.5
llamacpp:prompt_seconds_total 12.5
llamacpp:some_new_gauge 7
`)
	}))
	metrics, err := a.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 6 {
		t.Fatalf("metrics = %d, want 6", len(metrics))
	}
	byName := map[string]measurement.Metric{}
	for _, m := range metrics {
		byName[m.Name] = m
		if m.Source != measurement.SourceRuntime {
			t.Errorf("%s source = %s", m.Name, m.Source)
		}
	}
	if byName["llamacpp:prompt_tokens_total"].Value != 120 {
		t.Fatalf("counter value = %v", byName["llamacpp:prompt_tokens_total"].Value)
	}
	if got := byName["llamacpp:kv_cache_usage_ratio"].Labels["device"]; got != "0" {
		t.Fatalf("label device = %q", got)
	}
	// A metric this build does not know is recorded without a unit rather
	// than with a guessed one, and the schema has to accept that.
	if got := byName["llamacpp:some_new_gauge"]; got.Unit != "" {
		t.Fatalf("unknown gauge unit = %q, want empty", got.Unit)
	}
	// A rate named ..._tokens_seconds must not be labelled "seconds".
	if got := byName["llamacpp:predicted_tokens_seconds"]; got.Unit != "tok/s" {
		t.Fatalf("predicted_tokens_seconds unit = %q, want tok/s", got.Unit)
	}
	if got := byName["llamacpp:prompt_seconds_total"]; got.Unit != "seconds" {
		t.Fatalf("prompt_seconds_total unit = %q, want seconds", got.Unit)
	}
}

func TestLlamaCppDoesNotInventTokenCounts(t *testing.T) {
	// No timings block: the token count is unknown, so it must stay 0 rather
	// than fall back to the number of streamed chunks.
	a := newAdapter(t, "llamacpp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"content":"a"}`, `{"content":"b"}`, `{"content":"c"}`)
	}))
	got, err := a.Complete(context.Background(), Request{Prompt: "hi", MaxTokens: 8})
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletionTokens != 0 {
		t.Fatalf("CompletionTokens = %d, want 0 (chunks are not tokens)", got.CompletionTokens)
	}
	if got.DecodeTokensPerSecond() != 0 {
		t.Fatal("a decode rate was derived without a token count")
	}
}

func TestFreeTokenReady(t *testing.T) {
	cases := []struct {
		body string
		want error
	}{
		{`{"maintenance":"loading"}`, ErrNotReady},
		{`{"maintenance":"rebuilding"}`, ErrNotReady},
		{`{"maintenance":"serving"}`, nil},
		{`{"maintenance":"failed"}`, ErrUnavailable},
	}
	for _, tc := range cases {
		a := newAdapter(t, "freetoken", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, tc.body)
		}))
		err := a.Ready(context.Background())
		if tc.want == nil {
			if err != nil {
				t.Fatalf("body %s: %v", tc.body, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("body %s: got %v, want %v", tc.body, err, tc.want)
		}
	}
}

func TestFreeTokenComplete(t *testing.T) {
	a := newAdapter(t, "freetoken", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		sse(w,
			`{"choices":[{"delta":{"content":"A"}}]}`,
			`{"choices":[{"delta":{"content":"B"}}]}`,
			`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":20,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":8}}}`,
		)
	}))
	got, err := a.Complete(context.Background(), Request{Prompt: "hi", MaxTokens: 8})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "AB" || got.PromptTokens != 20 || got.CompletionTokens != 2 {
		t.Fatalf("completion = %+v", got)
	}
	if got.CachedTokens != 8 {
		t.Fatalf("cached tokens = %d, want 8", got.CachedTokens)
	}
	if got.TTFT <= 0 || got.DecodeTokensPerSecond() <= 0 {
		t.Fatalf("timings = ttft %s total %s", got.TTFT, got.Total)
	}
}

func TestFreeTokenReasoningCountsAsFirstOutput(t *testing.T) {
	a := newAdapter(t, "freetoken", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`,
			`{"choices":[{"delta":{"reasoning_content":" more"}}]}`,
			`{"choices":[{"delta":{"content":"answer"}}]}`,
			`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":4}}`,
		)
	}))
	got, err := a.Complete(context.Background(), Request{Prompt: "hi", MaxTokens: 8})
	if err != nil {
		t.Fatal(err)
	}
	// The visible answer is the content only; reasoning stays out of it.
	if got.Content != "answer" {
		t.Fatalf("content = %q", got.Content)
	}
	// TTFT is the first reasoning token, so it is earlier than the answer.
	if len(got.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(got.Steps))
	}
	if got.Steps[0].At >= got.Steps[2].At {
		t.Fatalf("reasoning deltas were not observed: %+v", got.Steps)
	}
	if got.CompletionTokens != 4 {
		t.Fatalf("CompletionTokens = %d, want 4 from usage", got.CompletionTokens)
	}
	if got.DecodeTokensPerSecond() <= 0 {
		t.Fatal("decode rate was not derived from the reported token count")
	}
}

func TestFreeTokenDoesNotInventTokenCounts(t *testing.T) {
	a := newAdapter(t, "freetoken", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"a"}}]}`, `{"choices":[{"delta":{"content":"b"}}]}`)
	}))
	got, err := a.Complete(context.Background(), Request{Prompt: "hi", MaxTokens: 8})
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletionTokens != 0 {
		t.Fatalf("CompletionTokens = %d, want 0 without a usage block", got.CompletionTokens)
	}
	if got.DecodeTokensPerSecond() != 0 {
		t.Fatal("a decode rate was derived without usage")
	}
}

func TestFreeTokenMetrics(t *testing.T) {
	a := newAdapter(t, "freetoken", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stats" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		fmt.Fprint(w, `{
  "kv": {"used_pages": 3, "total_pages": 12, "page_size": 128},
  "vram_bytes": 8589934592,
  "throughput": {"decode_tps": 44.5, "prefill_tps": 900.2},
  "requests": {"active": 0, "completed": 7, "p95_ms": 310, "ttft_mean_ms": 120},
  "unknown_future_field": {"ignored": true}
}`)
	}))
	metrics, err := a.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]measurement.Metric{}
	for _, m := range metrics {
		byName[m.Name] = m
	}
	if byName["decode_tok_per_s"].Value != 44.5 || byName["decode_tok_per_s"].Unit != "tok/s" {
		t.Fatalf("decode metric = %+v", byName["decode_tok_per_s"])
	}
	if byName["kv_cache_usage_ratio"].Value != 0.25 {
		t.Fatalf("kv ratio = %v", byName["kv_cache_usage_ratio"].Value)
	}
	if byName["vram_used_bytes"].Value != 8589934592 {
		t.Fatalf("vram = %v", byName["vram_used_bytes"].Value)
	}
}

func TestParsePrometheus(t *testing.T) {
	in := `# HELP x help
# TYPE x gauge
plain_metric 1.5
labeled{a="1",b="two words"} 2.5 1700000000000
escaped{path="a\\b\"c"} 3
`
	samples, err := parsePrometheus(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 3 {
		t.Fatalf("samples = %d, want 3", len(samples))
	}
	if samples[0].Name != "plain_metric" || samples[0].Value != 1.5 {
		t.Fatalf("first sample = %+v", samples[0])
	}
	if samples[1].Labels["b"] != "two words" || samples[1].Value != 2.5 {
		t.Fatalf("labeled sample = %+v", samples[1])
	}
	if samples[2].Labels["path"] != `a\b"c` {
		t.Fatalf("escaped label = %q", samples[2].Labels["path"])
	}
}
