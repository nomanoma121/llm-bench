package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	Args      []string
}

func (o Options) baseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", o.Port) }

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
	Ready(ctx context.Context) error
	Complete(ctx context.Context, req Request) (Completion, error)
	Metrics(ctx context.Context) ([]Metric, error)
	Info(log string) map[string]string
}

func New(engine string, o Options) (Adapter, error) {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	switch engine {
	case "llamacpp":
		return llamaCpp{o, client}, nil
	case "freetoken":
		return freeToken{o, client}, nil
	case "strata":
		return strata{o, client}, nil
	}
	return nil, fmt.Errorf("runtime: unknown engine %q (llamacpp, freetoken, strata)", engine)
}
