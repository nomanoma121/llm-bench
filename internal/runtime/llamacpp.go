package runtime

import (
	"context"
	"fmt"
	"net/http"
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
	return chatCompletion(ctx, a.client, a.opts.baseURL(), a.opts.ModelID, req, map[string]any{"cache_prompt": false})
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
	metrics, err := parsePrometheus(resp.Body)
	if err != nil {
		return nil, err
	}
	var slots []struct {
		NCtx          int  `json:"n_ctx"`
		IsProcessing  bool `json:"is_processing"`
		NPromptTokens int  `json:"n_prompt_tokens"`
		NextToken     []struct {
			NDecoded int `json:"n_decoded"`
		} `json:"next_token"`
	}
	if err := getJSON(ctx, a.client, a.opts.baseURL()+"/slots", &slots); err != nil {
		return metrics, nil
	}
	for _, s := range slots {
		if !s.IsProcessing || len(s.NextToken) == 0 {
			continue
		}
		decoded := float64(s.NextToken[0].NDecoded)
		metrics = append(metrics,
			Metric{Name: "llamacpp:slot_decoded_tokens", Value: decoded},
			Metric{Name: "llamacpp:context_tokens", Value: float64(s.NPromptTokens) + decoded},
			Metric{Name: "llamacpp:context_size", Value: float64(s.NCtx)},
		)
	}
	return metrics, nil
}
