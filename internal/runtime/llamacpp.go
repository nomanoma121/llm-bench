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
	return chatCompletion(ctx, a.client, a.opts.baseURL(), a.opts.ModelID, req)
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
