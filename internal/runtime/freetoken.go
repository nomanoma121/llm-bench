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

// freeToken measures FlashML's FreeToken engine (https://github.com/FlashML-org/FreeToken).
//
// Endpoints:
//
//	GET  /health                  {"status","maintenance"}; maintenance is
//	                              "loading" until the engine serves
//	GET  /v1/models               OpenAI model list, used to learn the served id
//	POST /v1/chat/completions     OpenAI protocol; streaming + include_usage
//	GET  /v1/stats                decode/prefill rates, KV and VRAM usage
//
// FreeToken serves the OpenAI and Anthropic protocols and an edge MoE engine
// whose tunables (MoE strategy, expert cache size, attention backend) are
// exactly what the optimization loop is meant to explore, so only the flags
// that decide what is measured are reserved.
type freeToken struct {
	opts   Options
	client *http.Client
}

// freeTokenBinary is the CLI inside the runtime image.
const freeTokenBinary = "ft"

func (freeToken) Name() string { return "freetoken" }

// ReservedArgs are the FreeToken serve flags that decide what is measured.
// Everything else (--moe-strategy, --moe-cache-size, --attention-backend,
// --memory-ratio, ...) is tuning and belongs to the job spec.
func (freeToken) ReservedArgs() []string {
	return []string{
		"--model", "--model-path",
		"--host", "--port",
		"--served-model-name",
	}
}

func (a freeToken) Argv() []string {
	o := a.opts
	argv := []string{
		freeTokenBinary, "serve",
		"--model", o.ModelPath,
		"--host", hostOrLoopback(o.Host),
		"--port", fmt.Sprint(o.Port),
		"--served-model-name", o.ModelID,
		// Makes usage.prompt_tokens_details.cached_tokens available, which is
		// how the harness observes prefix-cache reuse.
		"--enable-cache-report",
	}
	return append(argv, o.Args...)
}

func (a freeToken) BaseURL() string { return a.opts.BaseURL() }

func (a freeToken) Ready(ctx context.Context) error {
	base := a.opts.BaseURL()
	var health struct {
		Status      string `json:"status"`
		Maintenance string `json:"maintenance"`
	}
	if err := getJSON(ctx, a.client, base+"/health", &health); err != nil {
		return err
	}
	switch health.Maintenance {
	case "serving":
		return nil
	case "failed":
		return fmt.Errorf("%w: engine reported maintenance %q", ErrUnavailable, health.Maintenance)
	case "", "loading", "rebuilding":
		return fmt.Errorf("%w: engine is %q", ErrNotReady, health.Maintenance)
	default:
		return fmt.Errorf("%w: engine is %q", ErrNotReady, health.Maintenance)
	}
}

// openAIDelta is one streaming chunk of the OpenAI chat protocol.
type openAIDelta struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func (a freeToken) Complete(ctx context.Context, req Request) (Completion, error) {
	base := a.opts.BaseURL()
	body := map[string]any{
		"model":      a.opts.ModelID,
		"messages":   []map[string]string{{"role": "user", "content": req.Prompt}},
		"max_tokens": req.MaxTokens,
		"stream":     true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
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
	resp, err := postJSON(ctx, a.client, base+"/v1/chat/completions", body)
	if err != nil {
		return Completion{}, err
	}
	var out Completion
	var sb strings.Builder
	var first time.Duration
	err = streamSSE(ctx, resp, func(data []byte) error {
		var chunk openAIDelta
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("runtime: freetoken stream: %w", err)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			if first == 0 {
				first = time.Since(start)
			}
			out.Steps = append(out.Steps, Step{Index: len(out.Steps), At: time.Since(start)})
			sb.WriteString(choice.Delta.Content)
		}
		if chunk.Usage != nil {
			out.PromptTokens = chunk.Usage.PromptTokens
			out.CompletionTokens = chunk.Usage.CompletionTokens
			if chunk.Usage.PromptTokensDetails != nil {
				out.CachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			}
		}
		return nil
	})
	if err != nil {
		return Completion{}, err
	}
	out.Content = sb.String()
	out.TTFT = first
	out.Total = time.Since(start)
	if out.CompletionTokens == 0 {
		out.CompletionTokens = len(out.Steps)
	}
	return out, nil
}

// freeTokenStats is the /v1/stats document. Only the fields the harness
// records are decoded; FreeToken adds more over time and an unknown field must
// not fail a measurement.
type freeTokenStats struct {
	Kv *struct {
		UsedPages  int `json:"used_pages"`
		TotalPages int `json:"total_pages"`
		PageSize   int `json:"page_size"`
	} `json:"kv"`
	Mamba *struct {
		UsedSlots  int `json:"used_slots"`
		TotalSlots int `json:"total_slots"`
	} `json:"mamba"`
	VramBytes  int64 `json:"vram_bytes"`
	Throughput struct {
		DecodeTps  float64 `json:"decode_tps"`
		PrefillTps float64 `json:"prefill_tps"`
	} `json:"throughput"`
	Requests struct {
		Active       int `json:"active"`
		Completed    int `json:"completed"`
		P95MS        int `json:"p95_ms"`
		TTFTMeanMS   int `json:"ttft_mean_ms"`
		PromptTokens int `json:"prompt_tokens_total"`
		OutputTokens int `json:"completion_tokens_total"`
	} `json:"requests"`
}

func (a freeToken) Metrics(ctx context.Context) ([]measurement.Metric, error) {
	var stats freeTokenStats
	if err := getJSON(ctx, a.client, a.opts.BaseURL()+"/v1/stats", &stats); err != nil {
		return nil, err
	}
	var metrics []measurement.Metric
	add := func(name string, value float64, unit string) {
		metrics = append(metrics, measurement.Metric{
			Name: name, Value: value, Unit: unit, Source: measurement.SourceRuntime, Samples: 1,
		})
	}
	for _, m := range []struct {
		name  string
		value float64
		unit  string
	}{
		{"decode_tok_per_s", stats.Throughput.DecodeTps, "tok/s"},
		{"prefill_tok_per_s", stats.Throughput.PrefillTps, "tok/s"},
		{"vram_used_bytes", float64(stats.VramBytes), "bytes"},
		{"requests_completed", float64(stats.Requests.Completed), "count"},
		{"requests_p95_ms", float64(stats.Requests.P95MS), "ms"},
		{"requests_ttft_mean_ms", float64(stats.Requests.TTFTMeanMS), "ms"},
	} {
		add(m.name, m.value, m.unit)
	}
	if stats.Kv != nil {
		add("kv_cache_used_pages", float64(stats.Kv.UsedPages), "pages")
		add("kv_cache_total_pages", float64(stats.Kv.TotalPages), "pages")
		if stats.Kv.TotalPages > 0 {
			add("kv_cache_usage_ratio", float64(stats.Kv.UsedPages)/float64(stats.Kv.TotalPages), "ratio")
		}
	}
	if stats.Mamba != nil && stats.Mamba.TotalSlots > 0 {
		add("mamba_used_slots", float64(stats.Mamba.UsedSlots), "slots")
		add("mamba_usage_ratio", float64(stats.Mamba.UsedSlots)/float64(stats.Mamba.TotalSlots), "ratio")
	}
	return metrics, nil
}
