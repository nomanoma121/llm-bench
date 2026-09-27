// Package benchmark runs one measurement job inside the GPU sandbox: it starts
// the runtime, waits for it, measures each workload case, collects the
// configured metrics, and writes the result directory.
//
// The package is the harness. It owns identity (job id, spec digest, image
// digest, environment), validity (whether the measurement channel can be
// trusted) and the canonical output; the runtime adapter owns only the
// engine-specific commands and metrics. Nothing here decides whether a
// candidate is better than another — the Agent does that, from the facts this
// package records (docs/mvp.md §4).
package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

// Sentinel errors the CLI maps to its exit codes (docs/mvp.md §5).
var (
	// ErrNoResult means the run could not produce a result at all: the runtime
	// never started or never became ready.
	ErrNoResult = errors.New("benchmark: no result")
	// ErrTimeout means a bounded step (readiness) ran out of time.
	ErrTimeout = errors.New("benchmark: timeout")
)

// DefaultRequestTimeout bounds one measured request. It is deliberately
// generous: a long-context prefill on a consumer GPU can take minutes, and a
// timeout would otherwise be recorded as a candidate failure.
const DefaultRequestTimeout = 10 * time.Minute

// readyPollInterval is how often the harness asks the runtime whether it is up.
const readyPollInterval = 500 * time.Millisecond

// stopGrace is how long the runtime gets to exit after SIGTERM.
const stopGrace = 15 * time.Second

// Config is one measurement job.
type Config struct {
	Spec job.Spec
	// JobID names the output directory (docs/mvp.md §3.3).
	JobID string
	// RepoRoot is the checkout the result is written under and the root that
	// relative case prompts resolve against.
	RepoRoot string
	// OutputDir is the absolute directory the result is written to.
	OutputDir string
	// ModelPath is the resolved weights path or repository id. The operator
	// resolves it; a job spec never names it.
	ModelPath string
	// RequestTimeout overrides DefaultRequestTimeout.
	RequestTimeout time.Duration
	// Logf receives human-readable progress lines.
	Logf func(format string, args ...any)
}

func (c Config) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Seams are the collaborators the harness needs but does not own. Tests
// replace them; production uses DefaultSeams.
type Seams struct {
	// Starter launches the runtime process.
	Starter Starter
	// GPU reads GPU state for the nvidia collector. A nil reader makes the
	// collector report a gap, which invalidates the measurement when the
	// spec asked for it.
	GPU GPUReader
	// Now is the clock, for tests and for offsetting samples.
	Now func() time.Time
}

// DefaultSeams returns the production collaborators.
func DefaultSeams() Seams {
	return Seams{Starter: ExecStarter{}, GPU: NvidiaSMI{}, Now: time.Now}
}

// Outcome is what a completed run produced.
type Outcome struct {
	Dir      string
	Result   measurement.Result
	Sealed   measurement.Sealed
	Duration time.Duration
}

// Run measures the job and writes its output directory.
//
// A run that produced a result document returns normally even when the
// measurement is invalid: an invalid measurement is a fact worth recording, and
// the caller reports it through ExitCode. Only a run that could not produce a
// result at all (the runtime never came up, the output could not be written)
// returns an error.
func Run(ctx context.Context, cfg Config, adapter runtime.Adapter, seams Seams) (Outcome, error) {
	if seams.Starter == nil {
		seams.Starter = ExecStarter{}
	}
	if seams.GPU == nil {
		seams.GPU = NvidiaSMI{}
	}
	if seams.Now == nil {
		seams.Now = time.Now
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	specDigest, err := cfg.Spec.Digest()
	if err != nil {
		return Outcome{}, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.OutputDir, rawDir), 0o755); err != nil {
		return Outcome{}, fmt.Errorf("benchmark: output dir: %w", err)
	}
	jobspecYAML, err := yaml.Marshal(cfg.Spec)
	if err != nil {
		return Outcome{}, fmt.Errorf("benchmark: freeze spec: %w", err)
	}

	state := newRunState(cfg, adapter, seams, cfg.Spec.Metrics.Collectors)

	proc, err := state.startRuntime(ctx)
	if err != nil {
		return Outcome{}, err
	}
	defer state.stopRuntime(context.WithoutCancel(ctx), proc)

	if err := state.waitReady(ctx); err != nil {
		if errors.Is(err, runtime.ErrUnavailable) {
			return Outcome{}, fmt.Errorf("%w: %w", ErrNoResult, err)
		}
		return Outcome{}, fmt.Errorf("%w: %w", ErrTimeout, err)
	}
	state.collectGPU(ctx, "startup")
	state.measure(ctx)
	state.stopRuntime(context.WithoutCancel(ctx), proc)
	proc = nil

	// The raw samples are rendered before the result is built: a sample that
	// cannot be encoded has to appear in invalid_reasons.
	seriesJSONL := state.seriesJSONL()
	result := state.buildResult(specDigest)
	sealed, err := measurement.WriteResultDir(cfg.OutputDir, jobspecYAML, seriesJSONL, result)
	if err != nil {
		return Outcome{}, err
	}
	loaded, err := measurement.VerifyResultDir(cfg.OutputDir)
	if err != nil {
		// The directory we just wrote must verify: if it does not, the result
		// is not trustworthy and must not be reported as a success.
		return Outcome{}, fmt.Errorf("benchmark: self-check: %w", err)
	}
	state.recordDigests(loaded)
	if err := state.writeHumanOutputs(loaded.ResultDigest, sealed.Digest); err != nil {
		return Outcome{}, err
	}
	return Outcome{Dir: cfg.OutputDir, Result: loaded, Sealed: sealed, Duration: seams.Now().Sub(state.start)}, nil
}

// startRuntime launches the process and returns its handle.
func (s *runState) startRuntime(ctx context.Context) (Process, error) {
	logPath := filepath.Join(s.cfg.OutputDir, rawDir, runtimeLogName)
	argv := s.adapter.Argv()
	s.cfg.logf("starting runtime: %s", strings.Join(argv, " "))
	proc, err := s.seams.Starter.Start(ctx, argv, logPath)
	if err != nil {
		return nil, fmt.Errorf("%w: start runtime: %w", ErrNoResult, err)
	}
	return proc, nil
}

// stopRuntime stops the runtime and records why, so a failed case can be read
// back from the raw log. It is safe to call twice: Run stops the runtime right
// after measuring and the deferred call then only clears the handle.
func (s *runState) stopRuntime(ctx context.Context, proc Process) {
	if proc == nil || s.stopped {
		return
	}
	s.stopped = true
	if err := proc.Stop(ctx); err != nil && !isSignalExit(err) {
		// Stopping escalates to SIGKILL, so a signal exit is the expected
		// outcome; anything else is worth a line in the run log.
		s.cfg.logf("stopping the runtime: %v", err)
	}
	if err := proc.Wait(); err != nil && !isSignalExit(err) {
		s.cfg.logf("runtime exited with: %v", err)
	}
}

// waitReady polls the adapter until the runtime serves, the readiness timeout
// expires, or the runtime reports that it cannot serve.
func (s *runState) waitReady(ctx context.Context) error {
	deadline := s.seams.Now().Add(time.Duration(s.cfg.Spec.Runtime.Ready.Timeout()) * time.Second)
	var last error
	for {
		err := s.adapter.Ready(ctx)
		switch {
		case err == nil:
			s.cfg.logf("runtime is ready after %s", s.seams.Now().Sub(s.start).Round(time.Millisecond))
			return nil
		case errors.Is(err, runtime.ErrUnavailable):
			return err
		default:
			last = err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !s.seams.Now().Before(deadline) {
			return fmt.Errorf("readiness timeout after %s: %w", s.seams.Now().Sub(s.start).Round(time.Millisecond), last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPollInterval):
		}
	}
}

// measure runs every case the configured number of times.
func (s *runState) measure(ctx context.Context) {
	now := s.seams.Now
	for _, c := range s.cfg.Spec.Workload.Cases {
		repeats := c.RepeatCount()
		for i := 1; i <= repeats; i++ {
			reqCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
			temp, topP, seed := samplingOf(s.cfg.Spec.Workload)
			req := runtime.Request{
				Prompt:      s.prompt(c),
				MaxTokens:   c.MaxTokens,
				Temperature: temp,
				TopP:        topP,
				Seed:        seed,
			}
			completion, err := s.adapter.Complete(reqCtx, req)
			cancel()
			at := now().Sub(s.start)
			if err != nil {
				s.recordCaseError(c.Name, i, at, err)
				continue
			}
			s.recordCompletion(c, i, at, completion)
		}
		s.collectRuntime(ctx, c.Name)
		s.collectGPU(ctx, c.Name)
	}
}

// isSignalExit reports whether a process ended because it was signalled, which
// is what Stop does on purpose.
func isSignalExit(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled()
}

// samplingOf reads the optional sampling settings. A spec without them leaves
// every field unset, which tells the runtime to use its own defaults.
func samplingOf(w job.Workload) (temperature, topP *float64, seed *int64) {
	if w.Sampling == nil {
		return nil, nil, nil
	}
	return w.Sampling.Temperature, w.Sampling.TopP, w.Sampling.Seed
}

// prompt resolves the case prompt: an inline prompt_text, or a file read from
// the repository root. A missing file is a case error, not a job failure: the
// rest of the cases still produce evidence.
func (s *runState) prompt(c job.Case) string {
	if c.PromptText != "" {
		return c.PromptText
	}
	b, err := os.ReadFile(filepath.Join(s.cfg.RepoRoot, c.Prompt))
	if err != nil {
		s.recordCaseError(c.Name, 0, s.seams.Now().Sub(s.start), fmt.Errorf("prompt %s: %w", c.Prompt, err))
		return ""
	}
	return string(b)
}

// buildResult assembles the canonical document from the recorded samples.
func (s *runState) buildResult(specDigest string) measurement.Result {
	return measurement.Result{
		SchemaVersion:    measurement.SchemaVersion,
		JobID:            s.cfg.JobID,
		Kind:             string(s.cfg.Spec.Kind),
		JobSpecDigest:    specDigest,
		TrustLevel:       measurement.TrustUnverifiedDriver,
		MeasurementValid: len(s.invalidReasons) == 0,
		InvalidReasons:   s.invalidReasons,
		Metrics:          s.metrics(),
		Series:           s.series(),
		Collectors:       s.collectorStatus(),
		Environment:      s.environment(),
		Runtime:          s.runtimeRef(),
	}
}

// runtimeRef identifies what was measured. BuildDigest is filled only when the
// spec pins the image by digest, which is how the operator guarantees the
// binary that ran.
func (s *runState) runtimeRef() measurement.RuntimeRef {
	spec, err := json.Marshal(struct {
		Engine string   `json:"engine"`
		Image  string   `json:"image"`
		Args   []string `json:"args,omitempty"`
	}{s.cfg.Spec.Runtime.Engine, s.cfg.Spec.Runtime.Image, s.cfg.Spec.Runtime.Args})
	if err != nil {
		s.invalidate(fmt.Sprintf("runtime spec is not encodable: %v", err))
		return measurement.RuntimeRef{}
	}
	ref := measurement.RuntimeRef{SpecDigest: measurement.Digest(spec)}
	if i := strings.LastIndex(s.cfg.Spec.Runtime.Image, "@sha256:"); i >= 0 {
		ref.BuildDigest = s.cfg.Spec.Runtime.Image[i+len("@"):]
	}
	return ref
}
