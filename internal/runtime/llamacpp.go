package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type llamaCpp struct {
	opts   Options
	client *http.Client
}

func (a llamaCpp) Argv() []string {
	o := a.opts
	argv := []string{
		o.binary("llama-server"),
		"--model", o.ModelPath,
		"--alias", o.ModelID,
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(o.Port),
		"--metrics",
	}
	return append(argv, o.Args...)
}

func (a llamaCpp) Ready(ctx context.Context) error {
	var health struct {
		Status string `json:"status"`
	}
	if err := getJSON(ctx, a.client, a.opts.baseURL()+"/health", &health); err != nil {
		return err
	}
	if health.Status != "" && health.Status != "ok" {
		return fmt.Errorf("%w: %s", ErrNotReady, health.Status)
	}
	return nil
}

func (a llamaCpp) Complete(ctx context.Context, req Request) (Completion, error) {
	body := map[string]any{
		"prompt":       req.Prompt,
		"n_predict":    req.MaxTokens,
		"stream":       true,
		"cache_prompt": false,
	}
	setSampling(body, req)
	start := time.Now()
	resp, err := postJSON(ctx, a.client, a.opts.baseURL()+"/completion", body)
	if err != nil {
		return Completion{}, err
	}
	var out Completion
	var last time.Duration
	err = streamSSE(ctx, resp, func(data []byte) error {
		var chunk struct {
			Content string `json:"content"`
			Timings *struct {
				PromptN    int `json:"prompt_n"`
				PredictedN int `json:"predicted_n"`
			} `json:"timings"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		if chunk.Content != "" {
			last = time.Since(start)
			if out.TTFT == 0 {
				out.TTFT = last
			}
		}
		if chunk.Timings != nil {
			out.PromptTokens = chunk.Timings.PromptN
			out.CompletionTokens = chunk.Timings.PredictedN
		}
		return nil
	})
	out.Total = last
	return out, err
}

func (a llamaCpp) Metrics(ctx context.Context) ([]Metric, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.opts.baseURL()+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtime: /metrics returned %d", resp.StatusCode)
	}
	return parsePrometheus(resp.Body)
}
