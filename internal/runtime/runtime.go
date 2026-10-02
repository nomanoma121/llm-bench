package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var (
	ErrNotReady    = errors.New("runtime: not ready")
	ErrUnavailable = errors.New("runtime: unavailable")
)

type Options struct {
	Binary    string
	ModelID   string
	ModelPath string
	Port      int
	Settings  Settings
	// Args go after the arguments Settings become.
	Args []string
}

// Preparer is an adapter that writes files into dir before its runtime starts.
type Preparer interface {
	Prepare(dir string) error
}

func (o Options) baseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", o.Port) }

func (a llamaCpp) BaseURL() string  { return a.opts.baseURL() }
func (a freeToken) BaseURL() string { return a.opts.baseURL() }
func (a strata) BaseURL() string    { return a.opts.baseURL() }

func (o Options) binary(fallback string) string {
	if o.Binary != "" {
		return o.Binary
	}
	return fallback
}

type Request struct {
	Prompt      string
	MaxTokens   int
	Temperature *float64
	TopP        *float64
	Seed        *int64
	// ReasoningEffort is none, low, medium or high; empty leaves the default.
	ReasoningEffort string
}

type Completion struct {
	Content          string
	Reasoning        string
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	DraftTokens      int
	DraftAccepted    int
	TTFT             time.Duration
	Total            time.Duration
	// ITL holds the gaps between consecutive streamed chunks.
	ITL []time.Duration
	// ExpertHitRate is the share of MoE expert lookups the GPU cache served,
	// or 0 when the runtime does not report it.
	ExpertHitRate float64
}

type Metric struct {
	Name   string
	Value  float64
	Labels map[string]string
}

type Adapter interface {
	Argv() []string
	// BaseURL is where the runtime serves its OpenAI-compatible API.
	BaseURL() string
	Ready(ctx context.Context) error
	Complete(ctx context.Context, req Request) (Completion, error)
	Metrics(ctx context.Context) ([]Metric, error)
	Info(log string) map[string]string
}

var Engines = []string{"llamacpp", "strata", "freetoken"}

// New fails when the engine is unknown or cannot apply o.Settings.
func New(engine string, o Options) (Adapter, error) {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	var a interface {
		Adapter
		settingArgs() ([]string, error)
	}
	switch engine {
	case "llamacpp":
		a = llamaCpp{o, client}
	case "freetoken":
		a = freeToken{o, client}
	case "strata":
		a = &strata{opts: o, client: client}
	default:
		return nil, fmt.Errorf("runtime: unknown engine %q (%s)", engine, strings.Join(Engines, ", "))
	}
	if err := o.Settings.check(); err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	if _, err := a.settingArgs(); err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	return a, nil
}

func on(b *bool) bool  { return b != nil && *b }
func off(b *bool) bool { return b != nil && !*b }
