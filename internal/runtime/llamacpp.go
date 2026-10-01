package runtime

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
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
		"-lv", "4",
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

var llamaInfo = []struct {
	re   *regexp.Regexp
	keys []string
}{
	{regexp.MustCompile(`offloaded (\d+)/(\d+) layers to GPU`), []string{"layers_on_gpu", "layers_total"}},
	{regexp.MustCompile(`(\S+) model buffer size = +([\d.]+) MiB`), []string{"model_buffer_mib.", ""}},
	{regexp.MustCompile(`(\S+) KV buffer size = +([\d.]+) MiB`), []string{"kv_buffer_mib.", ""}},
	{regexp.MustCompile(`(\S+) compute buffer size = +([\d.]+) MiB`), []string{"compute_buffer_mib.", ""}},
	{regexp.MustCompile(`size = +([\d.]+) MiB \( *(\d+) cells, +(\d+) layers.*K \((\w+)\).*V \((\w+)\)`), []string{"kv_cache_mib", "kv_cells", "kv_layers", "kv_type_k", "kv_type_v"}},
	{regexp.MustCompile(`n_slots = (\d+), n_ctx_slot = (\d+)`), []string{"slots", "ctx_per_slot"}},
}

func (llamaCpp) Info(log string) map[string]string {
	info := map[string]string{}
	for _, line := range strings.Split(log, "\n") {
		for _, p := range llamaInfo {
			m := p.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if strings.HasSuffix(p.keys[0], ".") {
				info[p.keys[0]+m[1]] = m[2]
				continue
			}
			for i, key := range p.keys {
				info[key] = m[i+1]
			}
		}
	}
	return info
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
