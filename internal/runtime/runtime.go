// Package runtime holds the runtime adapters used by the in-Sandbox
// `llmbench benchmark`: how to start a target, wait for it to serve, send one
// prompt, and read the target's own metrics.
//
// An adapter owns the flags that decide *what* is being measured (the model
// path, the listen address, the metrics and log destinations). Those flags are
// reported through ReservedArgs and the job spec may not set them, so a job
// cannot point the runtime at another model or silence the collectors while
// still passing the operator allowlists. Everything else (context size, GPU
// layers, MoE cache size, attention backend, ...) is a tuning flag and is a
// legitimate target for the optimization loop.
//
// Adapters are measurement instruments: they report what they observed and
// never decide whether a candidate is better.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nomanoma121/llm-bench/internal/measurement"
)

// ErrNotReady means the runtime is reachable but still loading or rebuilding.
// The caller retries until the ready timeout expires.
var ErrNotReady = errors.New("runtime: not ready")

// ErrUnavailable means the runtime answered that it cannot serve (for example
// its engine failed to start). Retrying will not help.
var ErrUnavailable = errors.New("runtime: unavailable")

// Options are the adapter-owned inputs. The caller resolves ModelPath from
// operator configuration; a job spec never names a path.
type Options struct {
	// ModelID is the identifier reported to clients and used as the served
	// model name.
	ModelID string
	// ModelPath is the resolved path or repository id of the weights.
	ModelPath string
	// Host and Port are the loopback address the runtime binds.
	Host string
	Port int
	// Args are the tuning flags from the job spec. Reserved flags are
	// rejected by job.ValidateConstraints before they reach an adapter.
	Args []string
}

// BaseURL is the HTTP endpoint of the runtime.
func (o Options) BaseURL() string {
	host := o.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, o.Port)
}

// Request is one measurement case: one prompt, one repetition.
type Request struct {
	Prompt    string
	MaxTokens int
	// Sampling is optional; nil fields are left to the runtime's defaults.
	Temperature *float64
	TopP        *float64
	Seed        *int64
}

// Step is one observed token.
type Step struct {
	Index int
	At    time.Duration
}

// Completion is what one measured request produced.
type Completion struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	// CachedTokens is the prefix-cache hit count when the runtime reports it.
	CachedTokens int
	// TTFT is the time from sending the request to the first token.
	TTFT time.Duration
	// Total is the time from sending the request to the last token.
	Total time.Duration
	// Steps are the per-token arrival times relative to the request start.
	Steps []Step
}

// DecodeTokensPerSecond is the observed decode rate after the first token.
// It is derived rather than read from the runtime so both adapters report the
// same quantity.
func (c Completion) DecodeTokensPerSecond() float64 {
	if c.CompletionTokens < 2 {
		return 0
	}
	span := c.Total - c.TTFT
	if span <= 0 {
		return 0
	}
	return float64(c.CompletionTokens-1) / span.Seconds()
}

// Adapter measures one runtime engine. It is bound to the options of one job,
// so the callers (the benchmark command and the collectors) only have to hand
// it a request.
type Adapter interface {
	// Name is the engine name used by the job spec and the operator
	// allowlist.
	Name() string
	// ReservedArgs are the flags this adapter owns.
	ReservedArgs() []string
	// Argv builds the process command line. Tuning flags are appended after
	// the adapter-owned ones so a tuning flag can never replace an identity
	// flag, even if the reserved list were incomplete.
	Argv() []string
	// BaseURL is the loopback endpoint of the runtime.
	BaseURL() string
	// Ready reports whether the runtime finished loading. ErrNotReady means
	// "ask again"; ErrUnavailable means it will not become ready.
	Ready(ctx context.Context) error
	// Complete runs one prompt with streaming so the first-token latency is
	// observable.
	Complete(ctx context.Context, req Request) (Completion, error)
	// Metrics reads the runtime's own metrics. A runtime that exposes none
	// returns an empty slice: the harness still has its own timing.
	Metrics(ctx context.Context) ([]measurement.Metric, error)
}

// New returns an adapter for an engine name, bound to one job's options.
func New(engine string, o Options) (Adapter, error) {
	switch engine {
	case "llamacpp":
		return llamaCpp{opts: o, client: newHTTPClient()}, nil
	case "freetoken":
		return freeToken{opts: o, client: newHTTPClient()}, nil
	}
	return nil, fmt.Errorf("runtime: unknown engine %q (known: %s)", engine, Names())
}

// Names lists the engines this build supports, for the operator allowlist and
// for error messages.
func Names() string { return "llamacpp, freetoken" }

// ReservedArgs reports the flags an engine owns without binding to a job. The
// controller and the CLI turn this into job.Constraints.ReservedArgs, so a
// spec that tries to set one is rejected before a runtime is ever started.
func ReservedArgs(engine string) ([]string, error) {
	switch engine {
	case "llamacpp":
		return llamaCpp{}.ReservedArgs(), nil
	case "freetoken":
		return freeToken{}.ReservedArgs(), nil
	}
	return nil, fmt.Errorf("runtime: unknown engine %q (known: %s)", engine, Names())
}
