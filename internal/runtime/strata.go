package runtime

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

// strata runs Strata's server, which starts its engine from the install config
// that Strata's setup.py writes. ModelPath is that config; Binary, when set, is
// the server script of another Strata checkout.
type strata struct {
	opts   Options
	client *http.Client
}

func (a strata) Argv() []string {
	o := a.opts
	server := o.binary("/opt/strata/serve/server.py")
	python := filepath.Join(filepath.Dir(filepath.Dir(server)), ".venv", "bin", "python")
	argv := []string{
		python, server,
		"--engine", "strata",
		"--config", o.ModelPath,
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(o.Port),
	}
	return append(argv, o.Args...)
}

func (a strata) Ready(ctx context.Context) error {
	var health struct {
		Status string `json:"status"`
		Loaded bool   `json:"loaded"`
	}
	if err := getJSON(ctx, a.client, a.opts.baseURL()+"/health", &health); err != nil {
		return err
	}
	if health.Status != "ok" || !health.Loaded {
		return fmt.Errorf("%w: status %q, loaded %t", ErrNotReady, health.Status, health.Loaded)
	}
	return nil
}

// strataMetrics is the part of GET /metrics the benchmark reads. requests is
// newest first, and a request is listed before its response ends.
type strataMetrics struct {
	Engine struct {
		MaxContext float64 `json:"max_context"`
	} `json:"engine"`
	Live struct {
		State        string   `json:"state"`
		PromptTokens *float64 `json:"prompt_tokens"`
		Generated    *float64 `json:"generated"`
		TokS         *float64 `json:"tok_s"`
	} `json:"live"`
	Requests []struct {
		HitRate *float64 `json:"hit_rate"`
	} `json:"requests"`
}

func (a strata) Complete(ctx context.Context, req Request) (Completion, error) {
	out, err := chatCompletion(ctx, a.client, a.opts.baseURL(), a.opts.ModelID, req, nil)
	if err != nil {
		return out, err
	}
	var m strataMetrics
	if getJSON(ctx, a.client, a.opts.baseURL()+"/metrics", &m) == nil && len(m.Requests) > 0 && m.Requests[0].HitRate != nil {
		out.ExpertHitRate = *m.Requests[0].HitRate
	}
	return out, nil
}

func (a strata) Metrics(ctx context.Context) ([]Metric, error) {
	var m strataMetrics
	if err := getJSON(ctx, a.client, a.opts.baseURL()+"/metrics", &m); err != nil {
		return nil, err
	}
	out := []Metric{{Name: "context_size", Value: m.Engine.MaxContext}}
	if l := m.Live; l.PromptTokens != nil && l.Generated != nil {
		out = append(out,
			Metric{Name: "decoded_tokens", Value: *l.Generated},
			Metric{Name: "context_tokens", Value: *l.PromptTokens + *l.Generated},
		)
	}
	if m.Live.TokS != nil {
		out = append(out, Metric{Name: "strata:tok_s", Value: *m.Live.TokS})
	}
	return out, nil
}

var strataInfo = []struct {
	re   *regexp.Regexp
	keys []string
}{
	{regexp.MustCompile(`layer split auto: K=(\d+) - the caches hold (\d+) of (\d+) profiled pairs`), []string{"split_layer", "cached_expert_pairs", "profiled_expert_pairs"}},
	{regexp.MustCompile(`strata serve: layer split: (.+)`), []string{"layer_split"}},
}

func (strata) Info(log string) map[string]string {
	info := map[string]string{}
	for _, line := range strings.Split(log, "\n") {
		for _, p := range strataInfo {
			if m := p.re.FindStringSubmatch(line); m != nil {
				for i, key := range p.keys {
					info[key] = strings.TrimSpace(m[i+1])
				}
			}
		}
	}
	return info
}
