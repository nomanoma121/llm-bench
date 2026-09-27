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

	mu             sync.Mutex
	observations   []observation
	samples        []Sample
	gpu            []GPUSample
	gpuRaw         []string
	runtimeMetrics []measurement.Metric
	collectorSeen  map[job.Collector]int
	collectorGaps  map[job.Collector]int
	invalidReasons []string

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

// collectRuntime samples the runtime's own metrics after one case. The values
// are kept with the runtime source so a reader can tell them from the harness
// measurements, which is the whole point of the source field.
func (s *runState) collectRuntime(ctx context.Context, caseName string) {
	if !s.wants(job.CollectorRuntime) {
		return
	}
	metrics, err := s.adapter.Metrics(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.collectorGaps[job.CollectorRuntime]++
		s.addReasonLocked(fmt.Sprintf("runtime collector failed after case %q: %v", caseName, err))
		return
	}
	if len(metrics) == 0 {
		s.collectorGaps[job.CollectorRuntime]++
		s.addReasonLocked(fmt.Sprintf("runtime collector returned no metric after case %q; the job requires it", caseName))
		return
	}
	for _, m := range metrics {
		m.Labels = withCase(m.Labels, caseName)
		if m.Source == "" {
			m.Source = measurement.SourceRuntime
		}
		s.runtimeMetrics = append(s.runtimeMetrics, m)
		s.addSample(m.Name, m.Source, m.Unit, m.Value, withCase(nil, caseName), 0, 0, 0)
	}
	s.collectorSeen[job.CollectorRuntime]++
}

// collectGPU samples the GPU state once per case. The aggregation is
// documented in metrics(); the raw lines are kept so a human can re-read the
// run.
func (s *runState) collectGPU(ctx context.Context, caseName string) {
	if !s.wants(job.CollectorGPU) {
		return
	}
	sample, raw, err := s.seams.GPU.Sample(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.collectorGaps[job.CollectorGPU]++
		s.addReasonLocked(fmt.Sprintf("nvidia collector failed at %s: %v", caseName, err))
		return
	}
	s.gpuRaw = append(s.gpuRaw, fmt.Sprintf("# after case %s", caseName))
	s.gpuRaw = append(s.gpuRaw, raw...)
	s.gpu = append(s.gpu, sample)
	s.collectorSeen[job.CollectorGPU]++
	for _, g := range sample.GPUs {
		labels := map[string]string{"case": caseName, "gpu": g.Model}
		s.addSample("vram_used_mib", measurement.SourceExternalGPU, "MiB", float64(g.UsedMiB), labels, 0, 0, 0)
		s.addSample("gpu_util_percent", measurement.SourceExternalGPU, "percent", float64(g.UtilPercent), labels, 0, 0, 0)
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

func withCase(labels map[string]string, caseName string) map[string]string {
	out := map[string]string{}
	for k, v := range labels {
		out[k] = v
	}
	out["case"] = caseName
	return out
}

// metrics assembles the summary metrics. Harness metrics are summarized per
// case over the repeats; GPU metrics are aggregated over the per-case samples
// (VRAM by peak, utilization by mean); runtime metrics are kept per case
// because a gauge read after a case describes that case.
func (s *runState) metrics() []measurement.Metric {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.metricsLocked()
}

// metricsLocked assembles the summary metrics. Callers hold the lock.
func (s *runState) metricsLocked() []measurement.Metric {
	var metrics []measurement.Metric
	for _, name := range s.caseOrder() {
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

	if len(s.gpu) > 0 {
		var used, total, util []float64
		for _, g := range s.gpu {
			for _, dev := range g.GPUs {
				used = append(used, float64(dev.UsedMiB))
				total = append(total, float64(dev.TotalMiB))
				util = append(util, float64(dev.UtilPercent))
			}
		}
		// The peak is what decides whether a candidate fits, so VRAM is
		// aggregated by maximum; utilization is averaged because it is a
		// duty-cycle-like quantity.
		metrics = add(metrics, "vram_used_mib", "MiB", measurement.SourceExternalGPU, nil, used, maxOf)
		metrics = add(metrics, "vram_total_mib", "MiB", measurement.SourceExternalGPU, nil, total, maxOf)
		metrics = add(metrics, "gpu_util_percent", "percent", measurement.SourceExternalGPU, nil, util, meanOf)
	}
	return append(metrics, s.runtimeMetrics...)
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

// caseOrder is the order cases appear in, so the document lists them the way
// the spec does.
func (s *runState) caseOrder() []string {
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

// series builds the graph-shaped series: per-repeat harness values, the decode
// step shape, and the GPU samples.
func (s *runState) series() []measurement.Series {
	s.mu.Lock()
	defer s.mu.Unlock()
	type key struct{ name, c string }
	points := map[key][][2]float64{}
	units := map[key]string{}
	sources := map[key]measurement.Source{}
	var order []key
	addPoint := func(name string, source measurement.Source, unit, caseName string, x, y float64) {
		k := key{name, caseName}
		if _, seen := points[k]; !seen {
			order = append(order, k)
			units[k] = unit
			sources[k] = source
		}
		points[k] = append(points[k], [2]float64{x, y})
	}
	for _, sample := range s.samples {
		switch sample.Name {
		case "ttft_ms", "decode_tok_per_s", "decode_step_ms", "vram_used_mib":
		default:
			continue
		}
		x := float64(sample.Repeat)
		if sample.Name == "decode_step_ms" {
			x = float64(sample.Step)
		}
		addPoint(sample.Name, sample.Source, sample.Unit, sample.Labels["case"], x, sample.Value)
	}
	var out []measurement.Series
	for _, k := range order {
		out = append(out, measurement.Series{
			Name:          k.name,
			Unit:          units[k],
			Source:        sources[k],
			Labels:        map[string]string{"case": k.c},
			Points:        points[k],
			OriginalCount: len(points[k]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Labels["case"] < out[j].Labels["case"]
	})
	return out
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
		job.CollectorRuntime: 1000,
		job.CollectorGPU:     1000,
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

// environment records the stable machine identity, never the momentary state.
func (s *runState) environment() measurement.Environment {
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := map[string]int{}
	var order []string
	driver := ""
	for _, g := range s.gpu {
		if driver == "" {
			driver = g.Driver
		}
		for _, dev := range g.GPUs {
			if _, seen := counts[dev.Model]; !seen {
				order = append(order, dev.Model)
			}
			counts[dev.Model]++
		}
	}
	env := measurement.Environment{Driver: driver}
	sort.Strings(order)
	for _, model := range order {
		env.GPUs = append(env.GPUs, measurement.GPU{Model: model, Count: counts[model]})
	}
	return env
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
