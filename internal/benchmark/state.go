package benchmark

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

// Raw output file names inside the result's raw/ directory.
const (
	rawDir         = "raw"
	runtimeLogName = "runtime.log"
	nvidiaRawName  = "nvidia-smi.txt"
)

// Observation is one measured request.
type observation struct {
	Case     string
	Repeat   int
	At       time.Duration
	Duration time.Duration
	Result   runtime.Completion
	Err      error
}

// runState accumulates everything one run observes. The harness is the only
// writer, which is why identity and validity can be trusted: the runtime and
// the candidate contribute samples, never identity or validity.
type runState struct {
	cfg      Config
	adapter  runtime.Adapter
	seams    Seams
	start    time.Time
	required []job.Collector

	mu           sync.Mutex
	observations []observation
	samples      []Sample
	gpu          []GPUSample
	gpuRaw       []string
	// collectorSeen counts samples taken while a case was running: a reading
	// from before the workload says nothing about it, so it cannot satisfy a
	// required collector.
	collectorSeen  map[job.Collector]int
	collectorGaps  map[job.Collector]int
	invalidReasons []string
	currentCase    string
	inputs         measurement.Inputs
	prompts        map[string]string
	// env is the machine identity, captured by a dedicated probe so workload
	// samples cannot inflate it.
	env measurement.Environment

	// digests the result recorded, copied back after sealing so the generated
	// README prints the same values the result carries.
	specDigest     string
	specFileDigest string
	seriesDigest   string

	stopped bool
}

func newRunState(cfg Config, adapter runtime.Adapter, seams Seams, required []job.Collector) *runState {
	return &runState{
		cfg:           cfg,
		adapter:       adapter,
		seams:         seams,
		start:         seams.Now(),
		required:      required,
		collectorSeen: map[job.Collector]int{},
		collectorGaps: map[job.Collector]int{},
	}
}

// invalidate records why the measurement cannot be trusted.
func (s *runState) invalidate(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addReasonLocked(reason)
}

func (s *runState) addReasonLocked(reason string) {
	for _, r := range s.invalidReasons {
		if r == reason {
			return
		}
	}
	s.invalidReasons = append(s.invalidReasons, reason)
}

// setCase records which case the samplers should label their readings with.
func (s *runState) setCase(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentCase = name
}

// caseLabelsLocked adds the case label only while a case is running. A
// reading taken before or between cases belongs to the run, not to a case, and
// labelling it "startup" would invent a case that the job never declared.
func (s *runState) caseLabelsLocked(extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range extra {
		out[k] = v
	}
	if s.currentCase != "" {
		out["case"] = s.currentCase
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// setInputs freezes what was measured: the resolved model and the exact prompt
// bytes. The job spec only carries the model id and a prompt path, so without
// this two runs that read different weights or different prompt files would
// look like the same input.
func (s *runState) setInputs(in measurement.Inputs) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs = in
}

// recordCompletion turns one completed request into samples and an
// observation.
func (s *runState) recordCompletion(c job.Case, repeat int, at time.Duration, got runtime.Completion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, observation{
		Case: c.Name, Repeat: repeat, At: at, Duration: got.Total, Result: got,
	})
	s.collectorSeen[job.CollectorHarness]++

	labels := map[string]string{"case": c.Name}
	s.addSample("ttft_ms", measurement.SourceHarness, "ms", got.TTFT.Seconds()*1000, labels, repeat, 0, at)
	s.addSample("latency_ms", measurement.SourceHarness, "ms", got.Total.Seconds()*1000, labels, repeat, 0, at)
	if rate := decodeTokensPerSecond(got); rate > 0 {
		s.addSample("decode_tok_per_s", measurement.SourceHarness, "tok/s", rate, labels, repeat, 0, at)
	}
	if rate := prefillTokensPerSecond(got); rate > 0 {
		s.addSample("prefill_tok_per_s", measurement.SourceHarness, "tok/s", rate, labels, repeat, 0, at)
	}
	s.addSample("tokens_in", measurement.SourceHarness, "count", float64(got.PromptTokens), labels, repeat, 0, at)
	s.addSample("tokens_out", measurement.SourceHarness, "count", float64(got.CompletionTokens), labels, repeat, 0, at)
	s.addSample("requests", measurement.SourceHarness, "count", 1, labels, repeat, 0, at)

	// Inter-chunk latency: the shape of a decode step over the run. Steps are
	// streamed chunks, not tokens, so this series is descriptive only.
	prev := time.Duration(0)
	for i, step := range got.Steps {
		if i > 0 {
			s.addSample("decode_step_ms", measurement.SourceHarness, "ms", (step.At-prev).Seconds()*1000, labels, repeat, i, at+step.At)
		}
		prev = step.At
	}

	if got.CompletionTokens <= 0 {
		s.addReasonLocked(fmt.Sprintf("case %q repeat %d produced no token count, so no decode rate can be derived", c.Name, repeat))
	}
}

// recordCaseError records a request that could not be measured. A case without
// a measurement makes the measurement incomplete, so it invalidates the run
// rather than being silently skipped.
func (s *runState) recordCaseError(name string, repeat int, at time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, observation{Case: name, Repeat: repeat, At: at, Err: err})
	s.addReasonLocked(fmt.Sprintf("case %q repeat %d failed: %v", name, repeat, err))
}

// probeEnvironment records the machine identity once, independently of the
// collectors: the environment is part of the result's identity, so it must not
// depend on whether the job asked for the nvidia collector, and it must not be
// derived from workload samples (one GPU sampled ten times is still one GPU).
func (s *runState) probeEnvironment(ctx context.Context) {
	if s.seams.GPU == nil {
		return
	}
	sample, raw, err := s.sampleGPU(ctx)
	// The probe is best-effort: a machine without nvidia-smi produces a result
	// with no environment, which is a fact worth recording rather than a
	// failure. GPU driver/model identity does not change during a run.
	_ = err
	_ = raw
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		return
	}
	counts := map[string]int{}
	var order []string
	env := measurement.Environment{Driver: sample.Driver}
	for _, dev := range sample.GPUs {
		if _, seen := counts[dev.Model]; !seen {
			order = append(order, dev.Model)
		}
		counts[dev.Model]++
	}
	sort.Strings(order)
	for _, model := range order {
		env.GPUs = append(env.GPUs, measurement.GPU{Model: model, Count: counts[model]})
	}
	s.env = env
}

// sampleGPU calls the GPU reader with its own deadline, so a stuck nvidia-smi
// cannot block the sampler (and therefore the run) forever.
func (s *runState) sampleGPU(ctx context.Context) (GPUSample, []string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, s.seams.ProbeTimeout)
	defer cancel()
	return s.seams.GPU.Sample(probeCtx)
}

// collectRuntime samples the runtime's own metrics. They are kept as samples
// only, never as a summary metric: llama.cpp exposes process-lifetime counters
// and FreeToken exposes sliding-window rates, so a single number read after a
// case would be mislabelled as that case's value (docs/mvp.md §4.2). The
// trajectory stays available for the Agent through series.jsonl.
func (s *runState) collectRuntime(ctx context.Context) {
	if !s.wants(job.CollectorRuntime) {
		return
	}
	metrics, err := s.sampleRuntime(ctx)
	at := s.seams.Now().Sub(s.start)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.collectorGaps[job.CollectorRuntime]++
		s.addReasonLocked(fmt.Sprintf("runtime collector failed: %v", err))
		return
	}
	if len(metrics) == 0 {
		s.collectorGaps[job.CollectorRuntime]++
		s.addReasonLocked("runtime collector returned no metric; the job requires it")
		return
	}
	for _, m := range metrics {
		source := m.Source
		if source == "" {
			source = measurement.SourceRuntime
		}
		s.addSample(m.Name, source, m.Unit, m.Value, s.caseLabelsLocked(m.Labels), 0, 0, at)
	}
	s.noteWorkloadSampleLocked(job.CollectorRuntime)
}

// sampleRuntime calls the adapter's metrics endpoint with its own deadline: a
// runtime that stops answering a scrape must not hang the sampler.
func (s *runState) sampleRuntime(ctx context.Context) ([]measurement.Metric, error) {
	probeCtx, cancel := context.WithTimeout(ctx, s.seams.ProbeTimeout)
	defer cancel()
	return s.adapter.Metrics(probeCtx)
}

// collectGPU samples the GPU once. Sampling runs periodically while the cases
// run, which is what makes a peak and a mean meaningful: one reading taken
// after a case would report an idle utilization and miss the peak.
func (s *runState) collectGPU(ctx context.Context) {
	if s.seams.GPU == nil {
		return
	}
	sample, raw, err := s.sampleGPU(ctx)
	at := s.seams.Now().Sub(s.start)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if s.wants(job.CollectorGPU) {
			s.collectorGaps[job.CollectorGPU]++
			s.addReasonLocked(fmt.Sprintf("nvidia collector failed: %v", err))
		}
		// GPU identity is also used by the environment section, so a failure
		// is only invalid when the job asked for the collector.
		return
	}
	s.gpu = append(s.gpu, sample)
	s.gpuRaw = append(s.gpuRaw, raw...)
	if !s.wants(job.CollectorGPU) {
		// The reading still feeds the environment identity, but an
		// unrequested collector records no samples.
		return
	}
	s.noteWorkloadSampleLocked(job.CollectorGPU)
	for _, g := range sample.GPUs {
		labels := s.caseLabelsLocked(map[string]string{"gpu": g.Model})
		s.addSample("vram_used_mib", measurement.SourceExternalGPU, "MiB", float64(g.UsedMiB), labels, 0, 0, at)
		s.addSample("gpu_util_percent", measurement.SourceExternalGPU, "percent", float64(g.UtilPercent), labels, 0, 0, at)
	}
}

// startSampling runs the GPU and runtime collectors periodically until stop is
// called.
func (s *runState) startSampling(ctx context.Context) (stop func()) {
	if !s.wants(job.CollectorGPU) && !s.wants(job.CollectorRuntime) {
		return func() {}
	}
	interval := s.cfg.SampleInterval
	if interval <= 0 {
		interval = DefaultSampleInterval
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Only sample what the job asked for: an unrequested
				// collector must not add load to the measurement (and must
				// not add metrics the operator did not want).
				if s.wants(job.CollectorGPU) {
					s.collectGPU(ctx)
				}
				if s.wants(job.CollectorRuntime) {
					s.collectRuntime(ctx)
				}
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
	}
}

// noteWorkloadSampleLocked counts a sample towards the collector requirement
// only when a case is running. Callers hold the lock.
func (s *runState) noteWorkloadSampleLocked(c job.Collector) {
	if s.currentCase != "" {
		s.collectorSeen[c]++
	}
}

func (s *runState) wants(c job.Collector) bool {
	for _, want := range s.required {
		if want == c {
			return true
		}
	}
	return false
}

// addSample appends one raw observation. Callers hold the lock.
func (s *runState) addSample(name string, source measurement.Source, unit string, value float64, labels map[string]string, repeat, step int, at time.Duration) {
	s.samples = append(s.samples, Sample{
		Name:   name,
		Source: source,
		Unit:   unit,
		Labels: labels,
		Value:  value,
		AtMS:   at.Milliseconds(),
		Repeat: repeat,
		Step:   step,
	})
}

// decodeTokensPerSecond is the harness-side decode rate: it uses the runtime's
// reported token count over the span from the first to the last produced
// token, so it never invents a rate from stream chunk counts.
func decodeTokensPerSecond(got runtime.Completion) float64 {
	if got.CompletionTokens < 2 || got.Total <= got.TTFT {
		return 0
	}
	return float64(got.CompletionTokens-1) / (got.Total - got.TTFT).Seconds()
}

// prefillTokensPerSecond attributes the prompt tokens to the time to the first
// token. The first token is decoded as well, so the value is a lower bound on
// the prefill rate; it is reported as such and used the same way for baseline
// and candidate.
func prefillTokensPerSecond(got runtime.Completion) float64 {
	if got.PromptTokens <= 0 || got.TTFT <= 0 {
		return 0
	}
	return float64(got.PromptTokens) / got.TTFT.Seconds()
}

// metrics assembles the summary metrics. Harness metrics are summarized per
// case over the repeats; GPU metrics are aggregated over the periodic samples
// (VRAM by peak, utilization by mean). Runtime metrics are not summarized at
// all: see collectRuntime.
func (s *runState) metrics() []measurement.Metric {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.metricsLocked()
}

// metricsLocked assembles the summary metrics. Callers hold the lock.
func (s *runState) metricsLocked() []measurement.Metric {
	var metrics []measurement.Metric
	for _, name := range s.caseOrderLocked() {
		var ttft, latency, decode, prefill []float64
		var tokensIn, tokensOut, requests, totalMS float64
		for _, o := range s.observations {
			if o.Case != name || o.Err != nil {
				continue
			}
			ttft = append(ttft, o.Result.TTFT.Seconds()*1000)
			latency = append(latency, o.Result.Total.Seconds()*1000)
			tokensIn += float64(o.Result.PromptTokens)
			tokensOut += float64(o.Result.CompletionTokens)
			requests++
			totalMS += o.Result.Total.Seconds() * 1000
			if rate := decodeTokensPerSecond(o.Result); rate > 0 {
				decode = append(decode, rate)
			}
			if rate := prefillTokensPerSecond(o.Result); rate > 0 {
				prefill = append(prefill, rate)
			}
		}
		labels := map[string]string{"case": name}
		metrics = add(metrics, "ttft_ms", "ms", measurement.SourceHarness, labels, ttft, medianOf)
		metrics = add(metrics, "latency_ms", "ms", measurement.SourceHarness, labels, latency, medianOf)
		metrics = add(metrics, "decode_tok_per_s", "tok/s", measurement.SourceHarness, labels, decode, medianOf)
		metrics = add(metrics, "prefill_tok_per_s", "tok/s", measurement.SourceHarness, labels, prefill, medianOf)
		metrics = append(metrics,
			scalarMetric("requests", "count", measurement.SourceHarness, labels, requests),
			scalarMetric("tokens_in", "count", measurement.SourceHarness, labels, tokensIn),
			scalarMetric("tokens_out", "count", measurement.SourceHarness, labels, tokensOut),
			scalarMetric("total_ms", "ms", measurement.SourceHarness, labels, totalMS),
		)
	}

	var used, total, util []float64
	for _, g := range s.gpu {
		for _, dev := range g.GPUs {
			used = append(used, float64(dev.UsedMiB))
			total = append(total, float64(dev.TotalMiB))
			util = append(util, float64(dev.UtilPercent))
		}
	}
	// The peak decides whether a candidate fits, so VRAM is aggregated by
	// maximum over the periodic samples; utilization is a duty-cycle-like
	// quantity and is averaged.
	metrics = add(metrics, "vram_used_mib", "MiB", measurement.SourceExternalGPU, nil, used, maxOf)
	metrics = add(metrics, "vram_total_mib", "MiB", measurement.SourceExternalGPU, nil, total, maxOf)
	metrics = add(metrics, "gpu_util_percent", "percent", measurement.SourceExternalGPU, nil, util, meanOf)
	return metrics
}

// add appends a summary metric when the collector produced any value. A metric
// with nothing behind it is left out entirely rather than recorded as zero,
// which a reader could mistake for a measurement.
func add(out []measurement.Metric, name, unit string, source measurement.Source, labels map[string]string, values []float64, value func([]float64) float64) []measurement.Metric {
	if len(values) == 0 {
		return out
	}
	st := summarize(values)
	return append(out, measurement.Metric{
		Name:    name,
		Value:   value(values),
		Unit:    unit,
		Source:  source,
		Labels:  labels,
		Samples: len(values),
		Stats:   &st,
	})
}

func scalarMetric(name, unit string, source measurement.Source, labels map[string]string, value float64) measurement.Metric {
	return measurement.Metric{
		Name: name, Value: value, Unit: unit, Source: source, Labels: labels, Samples: 1,
	}
}

// caseOrderLocked is the order cases appear in, so the document lists them the
// way the spec does.
func (s *runState) caseOrderLocked() []string {
	var order []string
	seen := map[string]bool{}
	for _, o := range s.observations {
		if !seen[o.Case] {
			seen[o.Case] = true
			order = append(order, o.Case)
		}
	}
	return order
}

// series is the graph-shaped view of every per-observation sample: harness
// timings, the decode step shape, the GPU trajectory, and whatever the runtime
// reported.
func (s *runState) series() []measurement.Series {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The key must be the same identity measurement uses (name, source, unit
	// and labels): keying on name and case alone would merge the harness
	// decode rate with a runtime metric of the same name and keep whichever
	// source arrived first.
	type key struct{ name, source, unit, labels string }
	points := map[key][][2]float64{}
	labelMaps := map[string]map[string]string{}
	var order []key
	for _, sample := range s.samples {
		if !isSeriesSample(sample) {
			continue
		}
		k := key{sample.Name, string(sample.Source), sample.Unit, labelKey(sample.Labels)}
		if _, seen := points[k]; !seen {
			order = append(order, k)
			labelMaps[k.labels] = sample.Labels
		}
		x := float64(sample.Repeat)
		switch sample.Name {
		case "decode_step_ms":
			x = float64(sample.Step)
		case "vram_used_mib", "gpu_util_percent":
			x = float64(sample.AtMS)
		default:
			if sample.Source == measurement.SourceRuntime {
				x = float64(sample.AtMS)
			}
		}
		points[k] = append(points[k], [2]float64{x, sample.Value})
	}
	var out []measurement.Series
	for _, k := range order {
		out = append(out, measurement.Series{
			Name:          k.name,
			Unit:          k.unit,
			Source:        measurement.Source(k.source),
			Labels:        labelMaps[k.labels],
			Points:        points[k],
			OriginalCount: len(points[k]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return labelKey(out[i].Labels) < labelKey(out[j].Labels)
	})
	return out
}

// isSeriesSample decides which raw samples become a series. Harness timings,
// the decode shape and the GPU trajectory are always interesting; everything
// from the runtime is kept too, because the Agent may want to see how a cache
// or a KV gauge moved over the run.
func isSeriesSample(s Sample) bool {
	if s.Source == measurement.SourceRuntime {
		return true
	}
	switch s.Name {
	case "ttft_ms", "decode_tok_per_s", "decode_step_ms", "vram_used_mib", "gpu_util_percent":
		return true
	}
	return false
}

// finalizeValidity closes the accounting before the result is built: a
// collector the job asked for that never produced a single sample has to
// appear as an invalid reason, not only as a gap, because the two must agree.
func (s *runState) finalizeValidity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.required {
		if s.collectorSeen[c] == 0 {
			s.addReasonLocked(fmt.Sprintf("%s collector produced no sample while the workload ran; the job requires it", c))
		}
	}
}

// collectorStatus reports what each requested collector produced. A collector
// that saw nothing or failed records a gap, and a gap makes the measurement
// invalid: the operator asked for that source, so its absence has to be
// visible instead of being averaged away.
func (s *runState) collectorStatus() []measurement.CollectorStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	intervals := map[job.Collector]int{
		job.CollectorHarness: 1,
		job.CollectorRuntime: int(s.sampleIntervalMS()),
		job.CollectorGPU:     int(s.sampleIntervalMS()),
	}
	var out []measurement.CollectorStatus
	for _, c := range s.required {
		status := measurement.CollectorStatus{Name: string(c), IntervalMS: intervals[c], Gaps: s.collectorGaps[c]}
		if s.collectorSeen[c] == 0 {
			status.Gaps++
		}
		out = append(out, status)
	}
	return out
}

// sampleIntervalMS is the nominal collector period reported in the result.
func (s *runState) sampleIntervalMS() int64 {
	interval := s.cfg.SampleInterval
	if interval <= 0 {
		interval = DefaultSampleInterval
	}
	return interval.Milliseconds()
}

// environment records the stable machine identity, never the momentary state,
// and is derived from the GPU identity probe rather than from the optional
// collector.
func (s *runState) environment() measurement.Environment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.environmentLocked()
}

func (s *runState) environmentLocked() measurement.Environment {
	return s.env
}

// seriesJSONL renders the raw samples, one JSON object per line, in the order
// they were observed. The file is hashed as written, so the line order is part
// of the result identity. It must be called before buildResult: a sample that
// cannot be encoded has to land in invalid_reasons.
func (s *runState) seriesJSONL() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b []byte
	for _, sample := range s.samples {
		line, err := sample.marshal()
		if err != nil {
			s.addReasonLocked(fmt.Sprintf("sample %s could not be encoded: %v", sample.Name, err))
			continue
		}
		b = append(b, line...)
	}
	return b
}

// labelKey renders labels as a comparable string, matching the ordering rule
// the measurement package uses.
func labelKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b []byte
	for _, k := range keys {
		b = append(b, k...)
		b = append(b, 0x1f)
		b = append(b, labels[k]...)
		b = append(b, 0x1e)
	}
	return string(b)
}
