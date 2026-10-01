package runtime

import (
	"context"
	"fmt"
	"net/http"
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
	return chatCompletion(ctx, a.client, a.opts.baseURL(), a.opts.ModelID, req, nil)
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
