package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// strata runs Strata's server, which starts its engine from the install config
// that Strata's setup.py writes. ModelPath is that config; Binary, when set, is
// the server script of another Strata checkout.
type strata struct {
	opts   Options
	client *http.Client
	// config is the install config with Settings applied, once prepared.
	config string
}

func (a strata) Argv() []string {
	o := a.opts
	server := o.binary("/opt/strata/serve/server.py")
	python := filepath.Join(filepath.Dir(filepath.Dir(server)), ".venv", "bin", "python")
	config := a.config
	if config == "" {
		config = o.ModelPath
	}
	argv := []string{
		python, server,
		"--engine", "strata",
		"--config", config,
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(o.Port),
	}
	settings, _ := a.settingArgs()
	return append(append(argv, settings...), o.Args...)
}

// strataKV maps the common KV cache types to the engine's --kv.
var strataKV = map[string]string{"f16": "fp16", "q8_0": "int8"}

// settingArgs are the server's own; context and KV cache go into the engine
// arguments of the install config in Prepare.
func (a strata) settingArgs() ([]string, error) {
	s := a.opts.Settings
	if off(s.MTP) {
		return nil, fmt.Errorf("strata always decodes with mtp; its server needs it")
	}
	if off(s.ExpertsOnCPU) {
		return nil, fmt.Errorf("strata always keeps the experts in host memory")
	}
	if _, ok := strataKV[s.KVCache]; s.KVCache != "" && !ok {
		return nil, unsupported("strata", "kv_cache "+s.KVCache)
	}
	if s.GPUs > 0 {
		return []string{"--gpu", devices(s.GPUs, "")}, nil
	}
	return nil, nil
}

func (a *strata) Prepare(dir string) error {
	s := a.opts.Settings
	if s.Context == 0 && s.KVCache == "" {
		return nil
	}
	raw, err := os.ReadFile(a.opts.ModelPath)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("strata config %s: %w", a.opts.ModelPath, err)
	}
	args, _ := cfg["args"].([]any)
	if s.Context > 0 {
		args = setFlag(args, "--max-context", strconv.Itoa(s.Context))
	}
	if s.KVCache != "" {
		args = setFlag(args, "--kv", strataKV[s.KVCache])
	}
	cfg["args"] = args
	out, err := json.MarshalIndent(cfg, "", " ")
	if err != nil {
		return err
	}
	a.config = filepath.Join(dir, "strata-config.json")
	return os.WriteFile(a.config, out, 0o644)
}

// setFlag sets the value after flag, adding the pair when it is missing.
func setFlag(args []any, flag, value string) []any {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			args[i+1] = value
			return args
		}
	}
	return append(args, flag, value)
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
	{regexp.MustCompile(`session is up \(engine ([\d.]+)\)`), []string{"version"}},
	{regexp.MustCompile(`strata serve: layer split: (.+)`), []string{"layer_split"}},
	{regexp.MustCompile(`token graph hit path: (\d+) resident experts`), []string{"gpu_resident_experts"}},
	{regexp.MustCompile(`pre-filled (\d+) of (\d+) slots from the profile`), []string{"prefilled_expert_slots", "expert_slots"}},
	{regexp.MustCompile(`(\d+) MiB of VRAM free with everything loaded`), []string{"vram_free_mib"}},
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
