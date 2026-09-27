package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type freeToken struct {
	opts   Options
	client *http.Client
}

func (a freeToken) Argv() []string {
	o := a.opts
	argv := []string{
		o.binary("ft"), "serve",
		"--model", o.ModelPath,
		"--served-model-name", o.ModelID,
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(o.Port),
	}
	return append(argv, o.Args...)
}

func (a freeToken) Ready(ctx context.Context) error {
	var health struct {
		Maintenance string `json:"maintenance"`
	}
	if err := getJSON(ctx, a.client, a.opts.baseURL()+"/health", &health); err != nil {
		return err
	}
	switch health.Maintenance {
	case "serving":
		return nil
	case "failed":
		return fmt.Errorf("%w: engine failed to load", ErrUnavailable)
	default:
		return fmt.Errorf("%w: %s", ErrNotReady, health.Maintenance)
	}
}

func (a freeToken) Complete(ctx context.Context, req Request) (Completion, error) {
	body := map[string]any{
		"model":          a.opts.ModelID,
		"messages":       []map[string]string{{"role": "user", "content": req.Prompt}},
		"max_tokens":     req.MaxTokens,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	setSampling(body, req)
	start := time.Now()
	resp, err := postJSON(ctx, a.client, a.opts.baseURL()+"/v1/chat/completions", body)
	if err != nil {
		return Completion{}, err
	}
	var out Completion
	var last time.Duration
	err = streamSSE(ctx, resp, func(data []byte) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content == "" && c.Delta.ReasoningContent == "" {
				continue
			}
			last = time.Since(start)
			if out.TTFT == 0 {
				out.TTFT = last
			}
		}
		if chunk.Usage != nil {
			out.PromptTokens = chunk.Usage.PromptTokens
			out.CompletionTokens = chunk.Usage.CompletionTokens
		}
		return nil
	})
	out.Total = last
	return out, err
}

func (a freeToken) Metrics(ctx context.Context) ([]Metric, error) {
	var stats struct {
		VramBytes  float64 `json:"vram_bytes"`
		Throughput struct {
			DecodeTps  float64 `json:"decode_tps"`
			PrefillTps float64 `json:"prefill_tps"`
		} `json:"throughput"`
		Kv *struct {
			UsedPages  float64 `json:"used_pages"`
			TotalPages float64 `json:"total_pages"`
		} `json:"kv"`
	}
	if err := getJSON(ctx, a.client, a.opts.baseURL()+"/v1/stats", &stats); err != nil {
		return nil, err
	}
	out := []Metric{
		{Name: "freetoken:decode_tps", Value: stats.Throughput.DecodeTps},
		{Name: "freetoken:prefill_tps", Value: stats.Throughput.PrefillTps},
		{Name: "freetoken:vram_bytes", Value: stats.VramBytes},
	}
	if stats.Kv != nil {
		out = append(out,
			Metric{Name: "freetoken:kv_used_pages", Value: stats.Kv.UsedPages},
			Metric{Name: "freetoken:kv_total_pages", Value: stats.Kv.TotalPages},
		)
	}
	return out, nil
}
