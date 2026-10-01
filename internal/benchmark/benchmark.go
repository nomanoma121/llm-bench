package benchmark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

var ErrNoResult = errors.New("benchmark: no result")

type Config struct {
	Spec           job.Spec
	JobID          string
	Root           string
	OutDir         string
	ModelsDir      string
	Binary         string
	Source         string
	SampleInterval time.Duration
	Logf           func(format string, args ...any)
}

type run struct {
	cfg     Config
	adapter runtime.Adapter
	model   Model
	runtime Runtime
	start   time.Time

	mu          sync.Mutex
	currentCase string
	samples     []Sample
	invalid     []string
	gpu         gpuReading
}

func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.SampleInterval == 0 {
		cfg.SampleInterval = time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	spec := cfg.Spec
	modelPath := filepath.Join(cfg.ModelsDir, spec.Model)
	cfg.Logf("hashing %s", modelPath)
	modelDigest, err := digestPath(modelPath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: model: %w", job.ErrInvalid, err)
	}
	model := Model{ID: spec.ModelName(), Path: modelPath, Digest: modelDigest}
	adapter, err := runtime.New(spec.Runtime.Engine, runtime.Options{
		Binary:    cfg.Binary,
		ModelID:   model.ID,
		ModelPath: model.Path,
		Port:      spec.Runtime.ListenPort(),
		Args:      spec.Runtime.Args,
	})
	if err != nil {
		return Result{}, err
	}
	prompts, promptDigests, err := readPrompts(cfg.Root, spec.Workload)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.OutDir, "raw"), 0o755); err != nil {
		return Result{}, err
	}
	jobspec, err := yaml.Marshal(spec)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "jobspec.yaml"), jobspec, 0o644); err != nil {
		return Result{}, err
	}

	argv := adapter.Argv()
	r := &run{cfg: cfg, adapter: adapter, model: model, start: time.Now(), runtime: Runtime{
		Engine:  spec.Runtime.Engine,
		Binary:  argv[0],
		Version: sourceVersion(ctx, cfg.Source),
		Args:    spec.Runtime.Args,
	}}
	if gpu, err := readGPU(ctx); err == nil {
		r.gpu = gpu
	}

	cfg.Logf("starting %s: %v", r.runtime.Label(), argv)
	proc, err := startProcess(argv, filepath.Join(cfg.OutDir, "raw", "runtime.log"))
	if err != nil {
		return Result{}, fmt.Errorf("%w: start runtime: %w", ErrNoResult, err)
	}
	defer proc.stop()
	if err := r.waitReady(ctx, proc); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrNoResult, err)
	}

	stopSampling := r.sample(ctx)
	r.measure(ctx, prompts)
	stopSampling()
	proc.stop()
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrNoResult, err)
	}

	if log, err := os.ReadFile(filepath.Join(cfg.OutDir, "raw", "runtime.log")); err == nil {
		r.runtime.Info = adapter.Info(string(log))
	}
	series := r.seriesJSONL()
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "series.jsonl"), series, 0o644); err != nil {
		return Result{}, err
	}
	result := r.result(jobspec, series, promptDigests)
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "result.json"), append(b, '\n'), 0o644); err != nil {
		return Result{}, err
	}
	return result, os.WriteFile(filepath.Join(cfg.OutDir, "README.md"), []byte(result.readme()), 0o644)
}

func digestPath(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		file := sha256.New()
		if _, err := io.Copy(file, f); err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %x\n", filepath.ToSlash(rel), file.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}

func logTail(path string, lines int) string {
	b, _ := os.ReadFile(path)
	all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

func (r *run) saveOutput(caseName string, repeat int, content string) error {
	name := fmt.Sprintf("%s-%d", caseName, repeat)
	if err := os.MkdirAll(filepath.Join(r.cfg.OutDir, "raw", "outputs"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.cfg.OutDir, "raw", "outputs", name+".md"), []byte(content), 0o644); err != nil {
		return err
	}
	html := extractHTML(content)
	if html == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(r.cfg.OutDir, "output"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.cfg.OutDir, "output", name+".html"), []byte(html), 0o644)
}

func extractHTML(content string) string {
	if i := strings.Index(content, "```html"); i >= 0 {
		rest := content[i+len("```html"):]
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
		return strings.TrimSpace(rest)
	}
	lower := strings.ToLower(content)
	start := strings.Index(lower, "<!doctype html")
	if start < 0 {
		start = strings.Index(lower, "<html")
	}
	if start < 0 {
		return ""
	}
	end := strings.LastIndex(lower, "</html>")
	if end < start {
		return strings.TrimSpace(content[start:])
	}
	return content[start : end+len("</html>")]
}

func readPrompts(root string, cases []job.Case) (map[string]string, map[string]string, error) {
	prompts, digests := map[string]string{}, map[string]string{}
	for _, c := range cases {
		text := c.PromptText
		if c.Prompt != "" {
			b, err := os.ReadFile(filepath.Join(root, c.Prompt))
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", job.ErrInvalid, err)
			}
			text = string(b)
		}
		prompts[c.Name] = text
		digests[c.Name] = digest([]byte(text))
	}
	return prompts, digests, nil
}

func (r *run) waitReady(ctx context.Context, proc *process) error {
	timeout := time.Duration(r.cfg.Spec.Runtime.ReadyTimeoutSecondsOrDefault()) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		probe, cancelProbe := context.WithTimeout(ctx, 5*time.Second)
		err := r.adapter.Ready(probe)
		cancelProbe()
		switch {
		case err == nil:
			r.cfg.Logf("runtime ready after %s", time.Since(r.start).Round(time.Second))
			return nil
		case errors.Is(err, runtime.ErrUnavailable):
			return err
		}
		select {
		case <-proc.exited:
			return fmt.Errorf("runtime exited before becoming ready:\n%s", logTail(filepath.Join(r.cfg.OutDir, "raw", "runtime.log"), 20))
		case <-ctx.Done():
			return fmt.Errorf("runtime not ready after %s: %w", timeout, err)
		case <-time.After(time.Second):
		}
	}
}

func (r *run) measure(ctx context.Context, prompts map[string]string) {
	var sampling job.Sampling
	if r.cfg.Spec.Sampling != nil {
		sampling = *r.cfg.Spec.Sampling
	}
	for _, c := range r.cfg.Spec.Workload {
		r.setCase(c.Name)
		for i := 1; i <= c.RepeatCount(); i++ {
			if ctx.Err() != nil {
				return
			}
			got, err := r.adapter.Complete(ctx, runtime.Request{
				Prompt: prompts[c.Name], MaxTokens: c.MaxTokens,
				Temperature: sampling.Temperature, TopP: sampling.TopP, Seed: sampling.Seed,
			})
			if err != nil {
				r.invalidate(fmt.Sprintf("case %s repeat %d failed: %v", c.Name, i, err))
				continue
			}
			if got.CompletionTokens < 2 || got.TTFT == 0 {
				r.invalidate(fmt.Sprintf("case %s repeat %d produced %d tokens; no decode rate can be measured", c.Name, i, got.CompletionTokens))
			}
			r.recordCompletion(c.Name, i, got)
			if err := r.saveOutput(c.Name, i, got.Content); err != nil {
				r.invalidate(fmt.Sprintf("case %s repeat %d output could not be saved: %v", c.Name, i, err))
			}
		}
	}
	r.setCase("")
}

func (r *run) recordCompletion(caseName string, repeat int, got runtime.Completion) {
	values := map[string]float64{
		"ttft_ms":    ms(got.TTFT),
		"latency_ms": ms(got.Total),
		"tokens_in":  float64(got.PromptTokens),
		"tokens_out": float64(got.CompletionTokens),
	}
	if got.CompletionTokens > 1 && got.Total > got.TTFT {
		values["decode_tok_per_s"] = float64(got.CompletionTokens-1) / (got.Total - got.TTFT).Seconds()
	}
	if got.PromptTokens > 0 && got.TTFT > 0 {
		values["prefill_tok_per_s"] = float64(got.PromptTokens) / got.TTFT.Seconds()
	}
	if got.DraftTokens > 0 {
		values["draft_acceptance"] = float64(got.DraftAccepted) / float64(got.DraftTokens)
	}
	values["cached_tokens"] = float64(got.CachedTokens)
	r.mu.Lock()
	defer r.mu.Unlock()
	at := time.Since(r.start).Milliseconds()
	for name, v := range values {
		r.samples = append(r.samples, Sample{AtMS: at, Source: "harness", Name: name, Case: caseName, Repeat: repeat, Value: v})
	}
}

func (r *run) sample(ctx context.Context) func() {
	wantGPU, wantRuntime := r.cfg.Spec.Wants(job.MetricGPU), r.cfg.Spec.Wants(job.MetricRuntime)
	if !wantGPU && !wantRuntime {
		return func() {}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(r.cfg.SampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if wantGPU {
				r.sampleGPU(ctx)
			}
			if wantRuntime {
				r.sampleRuntime(ctx)
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, source := range []struct {
			want bool
			name string
		}{{wantGPU, "gpu"}, {wantRuntime, "runtime"}} {
			if source.want && !r.hasSourceLocked(source.name) {
				r.invalid = append(r.invalid, source.name+" metrics were requested but never sampled")
			}
		}
	}
}

func (r *run) sampleGPU(ctx context.Context) {
	reading, err := readGPU(ctx)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	at := time.Since(r.start).Milliseconds()
	for i, g := range reading.GPUs {
		labels := map[string]string{"gpu": strconv.Itoa(i), "model": g.Name}
		r.samples = append(r.samples,
			Sample{AtMS: at, Source: "gpu", Name: "vram_used_mib", Case: r.currentCase, Value: g.UsedMiB, Labels: labels},
			Sample{AtMS: at, Source: "gpu", Name: "gpu_util_percent", Case: r.currentCase, Value: g.UtilPercent, Labels: labels},
		)
	}
}

func (r *run) sampleRuntime(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	metrics, err := r.adapter.Metrics(ctx)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	at := time.Since(r.start).Milliseconds()
	for _, m := range metrics {
		r.samples = append(r.samples, Sample{AtMS: at, Source: "runtime", Name: m.Name, Case: r.currentCase, Value: m.Value, Labels: m.Labels})
	}
}

func (r *run) hasSourceLocked(source string) bool {
	for _, s := range r.samples {
		if s.Source == source {
			return true
		}
	}
	return false
}

func (r *run) setCase(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentCase = name
}

func (r *run) invalidate(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invalid = append(r.invalid, reason)
}

func (r *run) seriesJSONL() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, s := range r.samples {
		_ = enc.Encode(s)
	}
	return buf.Bytes()
}

var harnessMetrics = []struct {
	name, unit string
	agg        func([]float64) float64
}{
	{"ttft_ms", "ms", median},
	{"latency_ms", "ms", median},
	{"decode_tok_per_s", "tok/s", median},
	{"prefill_tok_per_s", "tok/s", median},
	{"tokens_in", "tokens", median},
	{"tokens_out", "tokens", median},
	{"draft_acceptance", "ratio", median},
	{"cached_tokens", "tokens", median},
}

func (r *run) result(jobspec, series []byte, prompts map[string]string) Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	spec := r.cfg.Spec
	values := func(source, name, caseName string) []float64 {
		var out []float64
		for _, s := range r.samples {
			if s.Source == source && s.Name == name && (caseName == "" || s.Case == caseName) {
				out = append(out, s.Value)
			}
		}
		return out
	}
	var metrics []Metric
	for _, c := range spec.Workload {
		for _, h := range harnessMetrics {
			if m, ok := summarize(h.name, c.Name, h.unit, values("harness", h.name, c.Name), h.agg); ok {
				metrics = append(metrics, m)
			}
		}
	}
	if m, ok := summarize("vram_used_mib", "", "MiB", values("gpu", "vram_used_mib", ""), maximum); ok {
		metrics = append(metrics, m)
	}
	if m, ok := summarize("gpu_util_percent", "", "%", values("gpu", "gpu_util_percent", ""), mean); ok {
		metrics = append(metrics, m)
	}

	workload, _ := json.Marshal(struct {
		Workload []job.Case
		Sampling *job.Sampling
		Prompts  map[string]string
	}{spec.Workload, spec.Sampling, prompts})

	var gpus []string
	for _, g := range r.gpu.GPUs {
		gpus = append(gpus, g.Name)
	}
	return Result{
		JobID:            r.cfg.JobID,
		Kind:             string(spec.Kind),
		StartedAt:        r.start.UTC(),
		DurationSeconds:  time.Since(r.start).Seconds(),
		Model:            r.model,
		Runtime:          r.runtime,
		GPUs:             gpus,
		Driver:           r.gpu.Driver,
		MeasurementValid: len(r.invalid) == 0,
		InvalidReasons:   r.invalid,
		Metrics:          metrics,
		Digests: Digests{
			JobSpec:  digest(jobspec),
			Series:   digest(series),
			Workload: digest(workload),
			Prompts:  prompts,
		},
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
