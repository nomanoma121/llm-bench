package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nomanoma121/llm-bench/internal/measurement"
)

// llamaCpp measures llama.cpp's llama-server.
//
// Endpoints (llama.cpp b4xxx):
//
//	GET  /health      {"status":"ok"}; 503 while the model loads
//	POST /completion  streaming chunks ending with "timings"
//	GET  /metrics     Prometheus text (requires --metrics)
type llamaCpp struct {
	opts   Options
	client *http.Client
}

// llamaCppBinary is the server binary inside the runtime image.
const llamaCppBinary = "llama-server"

func (llamaCpp) Name() string { return "llamacpp" }

// ReservedArgs are the llama.cpp flags that decide what is measured: the
// model sources, the listen address, the metrics and log destinations, and
// anything else that changes which weights are served. Upstream adds new
// model-selection flags over time, so this list is part of the adapter's
// contract and must be extended when a flag that changes the measured target
// is added (docs/mvp.md §3.4).
func (llamaCpp) ReservedArgs() []string {
	return []string{
		// Model sources: any of these replaces the weights we meant to measure.
		"-m", "--model",
		"-mu", "--model-url",
		"-dr", "--docker-repo",
		"-hf", "-hfr", "--hf-repo",
		"-hff", "--hf-file",
		"--lora", "--lora-scaled",
		"--control-vector", "--control-vector-scaled",
		"--mmproj",
		// Identity and transport of the measurement itself.
		"--host", "--port",
		"--metrics",
		"--log-file", "--log-verbosity",
		"--alias",
		"--api-key",
	}
}

func (a llamaCpp) Argv() []string {
	o := a.opts
	argv := []string{
		llamaCppBinary,
		"--model", o.ModelPath,
		"--host", hostOrLoopback(o.Host),
		"--port", fmt.Sprint(o.Port),
		// The harness reads these; --metrics makes /metrics available and the
		// alias pins the name clients ask for.
		"--metrics",
		"--alias", o.ModelID,
		"--log-file", "-", // stdout/stderr only: the harness captures them
	}
	return append(argv, o.Args...)
}

func (a llamaCpp) BaseURL() string { return a.opts.BaseURL() }

func (a llamaCpp) Ready(ctx context.Context) error {
	base := a.opts.BaseURL()
	var health struct {
		Status string `json:"status"`
	}
	if err := getJSON(ctx, a.client, base+"/health", &health); err != nil {
		return err
	}
	if health.Status != "" && health.Status != "ok" {
		return fmt.Errorf("%w: health status %q", ErrNotReady, health.Status)
	}
	return nil
}

// llamaChunk is one /completion stream chunk. The final chunk carries the
// timings; intermediate chunks carry the generated text.
type llamaChunk struct {
	Content string `json:"content"`
	Stop    bool   `json:"stop"`
	Timings *struct {
		PromptN            int     `json:"prompt_n"`
		PromptMS           float64 `json:"prompt_ms"`
		PredictedN         int     `json:"predicted_n"`
		PredictedMS        float64 `json:"predicted_ms"`
		PromptPerSecond    float64 `json:"prompt_per_second"`
		PredictedPerSecond float64 `json:"predicted_per_second"`
	} `json:"timings"`
}

func (a llamaCpp) Complete(ctx context.Context, req Request) (Completion, error) {
	base := a.opts.BaseURL()
	body := map[string]any{
		"prompt":       req.Prompt,
		"n_predict":    req.MaxTokens,
		"stream":       true,
		"cache_prompt": true,
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	start := time.Now()
	resp, err := postJSON(ctx, a.client, base+"/completion", body)
	if err != nil {
		return Completion{}, err
	}
	var out Completion
	var sb strings.Builder
	first := time.Duration(0)
	err = streamSSE(ctx, resp, func(data []byte) error {
		var chunk llamaChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("runtime: llamacpp stream: %w", err)
		}
		if chunk.Content != "" {
			if first == 0 {
				first = time.Since(start)
			}
			// Steps are observed stream chunks, not tokens: they drive the
			// shape of the decode series, never a token count.
			out.Steps = append(out.Steps, Step{Index: len(out.Steps), At: time.Since(start)})
			sb.WriteString(chunk.Content)
		}
		if chunk.Timings != nil {
			out.PromptTokens = chunk.Timings.PromptN
			out.CompletionTokens = chunk.Timings.PredictedN
		}
		return nil
	})
	if err != nil {
		return Completion{}, err
	}
	out.Content = sb.String()
	out.TTFT = first
	out.Total = time.Since(start)
	// CompletionTokens comes from the runtime's `timings` only. A stream chunk
	// is not guaranteed to be one token, so counting chunks would silently
	// invent a decode rate; a missing count stays 0 and the caller treats the
	// measurement as invalid.
	return out, nil
}

// Metrics returns the llama.cpp server metrics. Prometheus names are passed
// through unchanged (with the engine prefix kept) so a later comparison does
// not depend on a mapping table this package would have to keep in step with
// upstream. Units come from an explicit table: llama.cpp names a *rate* with a
// "_tokens_seconds" suffix and a *duration counter* with "_seconds_total", so
// guessing from the suffix records the wrong unit (docs/mvp.md §4.2). A name
// this build does not know is recorded without a unit rather than with a
// guessed one.
func (a llamaCpp) Metrics(ctx context.Context) ([]measurement.Metric, error) {
	base := a.opts.BaseURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/metrics", nil)
	if err != nil {
		return nil, fmt.Errorf("runtime: request: %w", err)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("runtime: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("runtime: %s/metrics returned %d", base, resp.StatusCode)
	}
	samples, err := parsePrometheus(resp.Body)
	if err != nil {
		return nil, err
	}
	var metrics []measurement.Metric
	for _, s := range samples {
		metrics = append(metrics, measurement.Metric{
			Name:   s.Name,
			Value:  s.Value,
			Unit:   llamaCppUnits[s.Name],
			Source: measurement.SourceRuntime,
			Labels: s.Labels,
			// Counters and gauges are instantaneous readings: one sample.
			Samples: 1,
		})
	}
	return metrics, nil
}

// llamaCppUnits are the units of the metrics llama.cpp exposes. The names are
// from the server's metrics registry; an entry is added when upstream adds one.
var llamaCppUnits = map[string]string{
	"llamacpp:prompt_tokens_total":            "count",
	"llamacpp:tokens_predicted_total":         "count",
	"llamacpp:prompt_tokens_seconds":          "tok/s",
	"llamacpp:tokens_predicted_seconds":       "tok/s",
	"llamacpp:predicted_tokens_seconds":       "tok/s",
	"llamacpp:prompt_seconds_total":           "seconds",
	"llamacpp:tokens_predicted_seconds_total": "seconds",
	"llamacpp:n_decode_total":                 "count",
	"llamacpp:n_busy_slots_per_decode":        "slots",
	"llamacpp:requests_processing":            "count",
	"llamacpp:requests_deferred":              "count",
	"llamacpp:kv_cache_usage_ratio":           "ratio",
	"llamacpp:kv_cache_tokens":                "count",
	"llamacpp:kv_cache_used_cells":            "cells",
}

func hostOrLoopback(host string) string {
	if host == "" {
		return "127.0.0.1"
	}
	return host
}
