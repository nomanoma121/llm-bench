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
	"sort"
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
	// prevCPU is the previous /proc/stat reading, used only by the sampler.
	prevCPU []cpuTimes
	// harnesses is set up on the first case that names a harness.
	harnesses *harnessRunner
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
	if r.harnesses != nil {
		r.harnesses.proxy.Close()
	}
	proc.stop()
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrNoResult, err)
	}

	if log, err := os.ReadFile(filepath.Join(cfg.OutDir, "raw", "runtime.log")); err == nil {
		r.runtime.Info = adapter.Info(string(log))
	}
	// A runtime run from its image has no source checkout; its log may say the version.
	if r.runtime.Version == "" {
		r.runtime.Version = r.runtime.Info["version"]
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
	// A model directory moved elsewhere can be left behind as a symlink, which
	// WalkDir would not enter.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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

func (r *run) saveOutput(caseName string, repeat int, content, reasoning string) error {
	name := fmt.Sprintf("%s-%d", caseName, repeat)
	if err := os.MkdirAll(filepath.Join(r.cfg.OutDir, "raw", "outputs"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.cfg.OutDir, "raw", "outputs", name+".md"), []byte(content), 0o644); err != nil {
		return err
	}
	if reasoning != "" {
		if err := os.WriteFile(filepath.Join(r.cfg.OutDir, "raw", "outputs", name+".reasoning.md"), []byte(reasoning), 0o644); err != nil {
			return err
		}
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
			if c.Harness != "" {
				if err := r.runHarness(ctx, c, i, prompts[c.Name], sampling); err != nil {
					r.invalidate(fmt.Sprintf("case %s repeat %d: %v", c.Name, i, err))
				}
				continue
			}
			got, err := r.adapter.Complete(ctx, runtime.Request{
				Prompt: prompts[c.Name], MaxTokens: c.MaxTokens,
				Temperature: sampling.Temperature, TopP: sampling.TopP, Seed: sampling.Seed,
				ReasoningEffort: sampling.ReasoningEffort,
			})
			if err != nil {
				r.invalidate(fmt.Sprintf("case %s repeat %d failed: %v", c.Name, i, err))
				continue
			}
			if got.CompletionTokens < 2 || got.TTFT == 0 {
				r.invalidate(fmt.Sprintf("case %s repeat %d produced %d tokens; no decode rate can be measured", c.Name, i, got.CompletionTokens))
			}
			r.record(c.Name, i, requestValues([]runtime.Completion{got}))
			if err := r.saveOutput(c.Name, i, got.Content, got.Reasoning); err != nil {
				r.invalidate(fmt.Sprintf("case %s repeat %d output could not be saved: %v", c.Name, i, err))
			}
		}
	}
	r.setCase("")
}

// requestValues summarizes the chat completions of one repeat: the single
// direct request, or every request a harness made. Times and counts add up
// over the requests, so a session's decode speed is its total decode tokens
// over its total decode time.
func requestValues(reqs []runtime.Completion) map[string]float64 {
	values := map[string]float64{"requests": float64(len(reqs))}
	var promptTokens, cachedTokens, completionTokens, decodeTokens, drafts, accepted, draftedTokens int
	var ttft, decodeTime, total time.Duration
	var ttfts, itl, hitRates []float64
	for _, got := range reqs {
		promptTokens += got.PromptTokens
		cachedTokens += got.CachedTokens
		completionTokens += got.CompletionTokens
		ttft += got.TTFT
		total += got.Total
		ttfts = append(ttfts, ms(got.TTFT))
		if got.CompletionTokens > 1 && got.Total > got.TTFT {
			decodeTokens += got.CompletionTokens - 1
			decodeTime += got.Total - got.TTFT
		}
		if got.DraftTokens > 0 {
			drafts += got.DraftTokens
			accepted += got.DraftAccepted
			draftedTokens += got.CompletionTokens
		}
		for _, d := range got.ITL {
			itl = append(itl, ms(d))
		}
		if got.ExpertHitRate > 0 {
			hitRates = append(hitRates, got.ExpertHitRate)
		}
	}
	values["tokens_in"] = float64(promptTokens)
	values["cached_tokens"] = float64(cachedTokens)
	values["tokens_out"] = float64(completionTokens)
	values["latency_ms"] = ms(total)
	if len(ttfts) > 0 {
		sort.Float64s(ttfts)
		values["ttft_ms"] = median(ttfts)
	}
	if decodeTime > 0 {
		values["decode_tok_per_s"] = float64(decodeTokens) / decodeTime.Seconds()
	}
	// Prompt caching stays on as runtimes are used; only the tokens the cache
	// did not hold were read before the first token.
	if read := promptTokens - cachedTokens; read > 0 && ttft > 0 {
		values["prefill_tok_per_s"] = float64(read) / ttft.Seconds()
	}
	if drafts > 0 {
		values["draft_acceptance"] = float64(accepted) / float64(drafts)
		// Each verification step emits one token plus the drafts it accepted.
		if steps := draftedTokens - accepted; steps > 0 {
			values["mtp_accept_len"] = float64(draftedTokens) / float64(steps)
		}
	}
	if len(itl) > 0 {
		sort.Float64s(itl)
		values["itl_ms_p50"] = percentile(itl, 0.50)
		values["itl_ms_p95"] = percentile(itl, 0.95)
		values["itl_ms_p99"] = percentile(itl, 0.99)
	}
	if len(hitRates) > 0 {
		values["expert_hit_rate"] = mean(hitRates)
	}
	return values
}

func (r *run) record(caseName string, repeat int, values map[string]float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	at := time.Since(r.start).Milliseconds()
	for name, v := range values {
		r.samples = append(r.samples, Sample{AtMS: at, Source: "harness", Name: name, Case: caseName, Repeat: repeat, Value: v})
	}
}

// sample polls each requested metric source every SampleInterval until the
// returned function is called. A requested source that never produced a sample
// makes the measurement invalid.
func (r *run) sample(ctx context.Context) func() {
	type source struct {
		name string
		poll func(context.Context)
	}
	var sources []source
	if r.cfg.Spec.Wants(job.MetricGPU) {
		sources = append(sources, source{"gpu", r.sampleGPU})
	}
	if r.cfg.Spec.Wants(job.MetricRuntime) {
		sources = append(sources, source{"runtime", r.sampleRuntime})
	}
	if r.cfg.Spec.Wants(job.MetricHost) {
		sources = append(sources, source{"host", r.sampleHost})
	}
	if len(sources) == 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(r.cfg.SampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			for _, s := range sources {
				s.poll(ctx)
			}
		}
	}()
	if r.cfg.Spec.Wants(job.MetricGPU) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// PCIe throughput is optional: older drivers or a missing dmon
			// must not invalidate the other GPU metrics.
			_ = streamPCIe(ctx, r.cfg.SampleInterval, r.recordPCIe)
		}()
	}
	return func() {
		cancel()
		wg.Wait()
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, s := range sources {
			if !r.hasSourceLocked(s.name) {
				r.invalid = append(r.invalid, s.name+" metrics were requested but never sampled")
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
		for _, f := range gpuFields {
			if v, ok := g.Values[f.name]; ok {
				r.samples = append(r.samples, Sample{AtMS: at, Source: "gpu", Name: f.name, Case: r.currentCase, Value: v, Labels: gpuLabels(i, g.Name)})
			}
		}
	}
}

func (r *run) recordPCIe(p pcieReading) {
	var name string
	if p.GPU < len(r.gpu.GPUs) {
		name = r.gpu.GPUs[p.GPU].Name
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	at := time.Since(r.start).Milliseconds()
	r.samples = append(r.samples,
		Sample{AtMS: at, Source: "gpu", Name: "pcie_rx_mbps", Case: r.currentCase, Value: p.RxMBps, Labels: gpuLabels(p.GPU, name)},
		Sample{AtMS: at, Source: "gpu", Name: "pcie_tx_mbps", Case: r.currentCase, Value: p.TxMBps, Labels: gpuLabels(p.GPU, name)},
	)
}

func gpuLabels(index int, model string) map[string]string {
	return map[string]string{"gpu": strconv.Itoa(index), "model": model}
}

// sampleHost records CPU and RAM use of the node the runtime runs on.
func (r *run) sampleHost(context.Context) {
	cpu, cpuErr := readCPU()
	mem, memErr := readMemUsedMiB()
	r.mu.Lock()
	defer r.mu.Unlock()
	at := time.Since(r.start).Milliseconds()
	add := func(name string, v float64) {
		r.samples = append(r.samples, Sample{AtMS: at, Source: "host", Name: name, Case: r.currentCase, Value: v})
	}
	if cpuErr == nil {
		if all, busiest, ok := cpuUtil(r.prevCPU, cpu); ok {
			add("cpu_util_percent", all)
			add("cpu_max_core_percent", busiest)
		}
		r.prevCPU = cpu
	}
	if memErr == nil {
		add("ram_used_mib", mem)
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
	{"mtp_accept_len", "tokens", median},
	{"requests", "requests", median},
	{"completed", "ratio", mean},
	{"expert_hit_rate", "ratio", median},
	{"cached_tokens", "tokens", median},
	{"itl_ms_p50", "ms", median},
	{"itl_ms_p95", "ms", median},
	{"itl_ms_p99", "ms", median},
}

// sampledMetrics summarize the polled series over the whole run.
var sampledMetrics = []struct {
	source, name, unit string
	agg                func([]float64) float64
}{
	{"gpu", "vram_used_mib", "MiB", maximum},
	{"gpu", "gpu_util_percent", "%", mean},
	{"gpu", "power_w", "W", mean},
	{"gpu", "temperature_c", "°C", maximum},
	{"host", "cpu_util_percent", "%", mean},
	{"host", "ram_used_mib", "MiB", maximum},
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
	for _, c := range spec.Workload {
		if m, ok := energyPerToken(r.samples, c.Name); ok {
			metrics = append(metrics, m)
		}
	}
	for _, sm := range sampledMetrics {
		if m, ok := summarize(sm.name, "", sm.unit, values(sm.source, sm.name, ""), sm.agg); ok {
			metrics = append(metrics, m)
		}
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
		Harnesses:        r.harnessVersions(),
		Digests: Digests{
			JobSpec:  digest(jobspec),
			Series:   digest(series),
			Workload: digest(workload),
			Prompts:  prompts,
		},
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// energyPerToken integrates the power of all GPUs over the samples taken while
// a case ran and divides it by the tokens the case generated. Prefill is
// included, so it is the energy cost of a generated token end to end.
func energyPerToken(samples []Sample, caseName string) (Metric, bool) {
	power := map[int64]float64{}
	var tokens float64
	var repeats int
	for _, s := range samples {
		switch {
		case s.Case != caseName:
		case s.Source == "gpu" && s.Name == "power_w":
			power[s.AtMS] += s.Value
		case s.Source == "harness" && s.Name == "tokens_out":
			tokens += s.Value
			repeats++
		}
	}
	times := make([]int64, 0, len(power))
	for t := range power {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	var joules float64
	for i := 1; i < len(times); i++ {
		joules += power[times[i]] * float64(times[i]-times[i-1]) / 1000
	}
	if joules == 0 || tokens == 0 {
		return Metric{}, false
	}
	v := joules / tokens
	return Metric{Name: "energy_j_per_token", Case: caseName, Unit: "J/token", Value: v, Min: v, Max: v, Samples: repeats}, true
}

func (r *run) harnessVersions() map[string]string {
	if r.harnesses == nil {
		return nil
	}
	return r.harnesses.installed
}
