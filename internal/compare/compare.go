// Package compare answers a question, not a verdict: given two result
// directories, it reports what changed and whether the two are comparable at
// all. The Agent decides whether a candidate is worth keeping (docs/mvp.md
// §5); this package must never say "better", "accept" or "recommended".
package compare

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nomanoma121/llm-bench/internal/measurement"
)

// MetricDelta is one metric's movement between two results.
//
// Before, After, AbsChange and RelChange are pointers because a measured zero,
// an absent side and an undefined relative change are three different facts: a
// plain float would report "0" for all of them.
type MetricDelta struct {
	// Case is the case label, kept for display; Labels is the full identity.
	Case   string            `json:"case,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	Name   string            `json:"name"`
	Source string            `json:"source"`
	Unit   string            `json:"unit,omitempty"`

	Before    *float64 `json:"before,omitempty"`
	After     *float64 `json:"after,omitempty"`
	AbsChange *float64 `json:"abs_change,omitempty"`
	// RelChange is absent when the baseline is zero: relative change is
	// undefined there, not zero.
	RelChange *float64 `json:"rel_change,omitempty"`

	// BeforeSamples and AfterSamples are how many observations backed each
	// side, so a reader can see whether a difference is meaningful.
	BeforeSamples int `json:"before_samples,omitempty"`
	AfterSamples  int `json:"after_samples,omitempty"`
	// Status marks metrics that exist on one side only.
	Status string `json:"status"`
}

// Metric statuses.
const (
	StatusCompared   = "compared"
	StatusOnlyBefore = "only_before"
	StatusOnlyAfter  = "only_after"
)

// Result is the factual comparison of two runs.
type Result struct {
	Baseline   Identity `json:"baseline"`
	Candidate  Identity `json:"candidate"`
	Comparable bool     `json:"comparable"`
	// Reasons explains why the two are not comparable. It is empty exactly
	// when Comparable is true.
	Reasons []string      `json:"reasons,omitempty"`
	Metrics []MetricDelta `json:"metrics"`
	// MeasurementValid reports whether both sides are trustworthy
	// measurements; an invalid side is a reason to treat the comparison as
	// inconclusive.
	MeasurementValid bool `json:"measurement_valid"`
}

// Identity is what a comparison needs to know about one side.
type Identity struct {
	Dir          string `json:"dir"`
	JobID        string `json:"job_id"`
	Kind         string `json:"kind"`
	ResultDigest string `json:"result_digest"`
	// Model and prompts identify the inputs; they must match for two runs to
	// be comparable.
	ModelPath   string            `json:"model_path"`
	ModelDigest string            `json:"model_digest,omitempty"`
	Prompts     map[string]string `json:"prompts,omitempty"`
	// Runtime identifies the runtime build; runtime optimization expects this
	// to differ, model comparison expects it to match.
	RuntimeSpecDigest  string `json:"runtime_spec_digest,omitempty"`
	RuntimeBuildDigest string `json:"runtime_build_digest,omitempty"`
}

// Kind selects which differences are allowed.
type Kind string

const (
	// KindModel compares two models on the same runtime: the model may differ,
	// the runtime spec may not.
	KindModel Kind = "model"
	// KindRuntime compares two runtime builds on the same model: the runtime
	// may differ, the model and prompts may not.
	KindRuntime Kind = "runtime"
)

// Load reads and verifies a result directory.
func Load(dir string) (measurement.Result, error) {
	r, err := measurement.VerifyResultDir(dir)
	if err != nil {
		return measurement.Result{}, fmt.Errorf("compare: %w", err)
	}
	return r, nil
}

// Compare reports the factual difference between two verified results.
//
// Both sides are read through VerifyResultDir, so a sidecar file that does not
// match the recorded digests is an error rather than a silent difference.
func Compare(baselineDir, candidateDir string, kind Kind) (Result, error) {
	if kind != KindModel && kind != KindRuntime {
		return Result{}, fmt.Errorf("compare: kind must be %q or %q, got %q", KindModel, KindRuntime, kind)
	}
	before, err := Load(baselineDir)
	if err != nil {
		return Result{}, err
	}
	after, err := Load(candidateDir)
	if err != nil {
		return Result{}, err
	}
	if before.JobID == after.JobID && before.ResultDigest == after.ResultDigest {
		return Result{}, fmt.Errorf("compare: both sides are the same result (%s)", before.ResultDigest)
	}

	out := Result{
		Baseline:         identityOf(baselineDir, before),
		Candidate:        identityOf(candidateDir, after),
		MeasurementValid: before.MeasurementValid && after.MeasurementValid,
	}
	out.Reasons = comparabilityReasons(before, after, kind)
	out.Comparable = len(out.Reasons) == 0
	out.Metrics = deltas(before.Metrics, after.Metrics)
	return out, nil
}

// comparabilityReasons lists every difference that makes the two runs
// incomparable for the requested kind. The list is exhaustive rather than
// first-error: a reader needs to see all of the reasons at once.
func comparabilityReasons(before, after measurement.Result, kind Kind) []string {
	var reasons []string
	add := func(format string, args ...any) { reasons = append(reasons, fmt.Sprintf(format, args...)) }

	if before.Kind != after.Kind {
		add("kind differs: %s vs %s", before.Kind, after.Kind)
	}
	if before.MeasurementValid != after.MeasurementValid {
		add("one side is an invalid measurement (%t vs %t)", before.MeasurementValid, after.MeasurementValid)
	}
	if !before.MeasurementValid {
		add("the baseline measurement is invalid: %s", strings.Join(before.InvalidReasons, "; "))
	}
	if !after.MeasurementValid {
		add("the candidate measurement is invalid: %s", strings.Join(after.InvalidReasons, "; "))
	}

	// Prompts must always match: a different prompt is a different question.
	// Every difference is reported, not just the first one.
	for _, diff := range promptDifferences(before.Inputs, after.Inputs) {
		add("%s", diff)
	}
	// The workload has to match as well: the same prompt with a different
	// token budget, repeat count or sampling is a different measurement, and a
	// different collector set puts a different load on the runtime.
	if before.Inputs.WorkloadDigest == "" || after.Inputs.WorkloadDigest == "" {
		add("a side does not record its workload digest, so the workload cannot be compared")
	} else if before.Inputs.WorkloadDigest != after.Inputs.WorkloadDigest {
		add("the workload (cases, sampling, collectors) differs")
	}
	if diff := collectorDifference(before.Collectors, after.Collectors); diff != "" {
		add("%s", diff)
	}
	switch kind {
	case KindModel:
		if before.Runtime.SpecDigest != after.Runtime.SpecDigest {
			add("the runtime spec differs but this is a model comparison")
		}
		// A model comparison is only meaningful on the same runtime *build*:
		// two image tags are not two identical runtimes.
		if before.Runtime.BuildDigest == "" || after.Runtime.BuildDigest == "" {
			add("a side does not record its runtime build digest, so a model comparison cannot prove the runtime was the same")
		} else if before.Runtime.BuildDigest != after.Runtime.BuildDigest {
			add("the runtime build differs (%s vs %s) but this is a model comparison", before.Runtime.BuildDigest, after.Runtime.BuildDigest)
		}
		// The weights are identified by digest: the same digest read from two
		// paths is one model, and one path whose contents changed is two.
		if before.Inputs.ModelDigest == "" || after.Inputs.ModelDigest == "" {
			add("a side does not record a model digest, so the two models cannot be compared")
		} else if before.Inputs.ModelDigest == after.Inputs.ModelDigest {
			add("both sides measured the same model digest: a model comparison needs two models")
		}
	case KindRuntime:
		if before.Inputs.ModelDigest == "" || after.Inputs.ModelDigest == "" {
			add("a side does not record a model digest, so a runtime comparison cannot prove the model was the same")
		} else if before.Inputs.ModelDigest != after.Inputs.ModelDigest {
			add("the model digest differs but this is a runtime comparison")
		}
		if before.Runtime.BuildDigest == "" || after.Runtime.BuildDigest == "" {
			add("a side does not record its runtime build digest, so the two builds cannot be compared")
		} else if before.Runtime.BuildDigest == after.Runtime.BuildDigest {
			add("both sides ran the same runtime build (%s): a runtime comparison needs two builds", before.Runtime.BuildDigest)
		}
	}
	// The environment must match *and* be known: two runs that both failed to
	// record a GPU have not agreed on anything, so an empty environment is a
	// reason rather than a match.
	if !environmentKnown(before) {
		add("the baseline does not record its environment (driver and GPU identity), so the two cannot be compared")
	}
	if !environmentKnown(after) {
		add("the candidate does not record its environment (driver and GPU identity), so the two cannot be compared")
	}
	if a, b := environmentKey(before), environmentKey(after); a != b {
		add("the measured environment differs: %s vs %s", a, b)
	}
	return reasons
}

func promptDifferences(a, b measurement.Inputs) []string {
	var reasons []string
	names := map[string]bool{}
	for name := range a.Prompts {
		names[name] = true
	}
	for name := range b.Prompts {
		names[name] = true
	}
	for _, name := range sortedKeys(names) {
		before, inBefore := a.Prompts[name]
		after, inAfter := b.Prompts[name]
		switch {
		case !inAfter:
			reasons = append(reasons, fmt.Sprintf("case %q exists in the baseline only", name))
		case !inBefore:
			reasons = append(reasons, fmt.Sprintf("case %q exists in the candidate only", name))
		case before != after:
			reasons = append(reasons, fmt.Sprintf("the prompt bytes of case %q differ", name))
		}
	}
	return reasons
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func environmentKnown(r measurement.Result) bool {
	return r.Environment.Driver != "" && len(r.Environment.GPUs) > 0
}

// collectorDifference compares the collectors the two runs asked for, with the
// interval they sampled at. Gaps are left out: a collector that failed to
// sample is a validity question, not a comparability one.
func collectorDifference(before, after []measurement.CollectorStatus) string {
	key := func(cs []measurement.CollectorStatus) string {
		out := make([]string, 0, len(cs))
		for _, c := range cs {
			out = append(out, fmt.Sprintf("%s@%dms", c.Name, c.IntervalMS))
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	if x, y := key(before), key(after); x != y {
		return fmt.Sprintf("the collector configuration differs: %s vs %s", x, y)
	}
	return ""
}

func environmentKey(r measurement.Result) string {
	var b strings.Builder
	b.WriteString(r.Environment.Driver)
	for _, g := range r.Environment.GPUs {
		fmt.Fprintf(&b, "|%s x%d", g.Model, g.Count)
	}
	return b.String()
}

func identityOf(dir string, r measurement.Result) Identity {
	return Identity{
		Dir:                dir,
		JobID:              r.JobID,
		Kind:               r.Kind,
		ResultDigest:       r.ResultDigest,
		ModelPath:          r.Inputs.ModelPath,
		ModelDigest:        r.Inputs.ModelDigest,
		Prompts:            r.Inputs.Prompts,
		RuntimeSpecDigest:  r.Runtime.SpecDigest,
		RuntimeBuildDigest: r.Runtime.BuildDigest,
	}
}

// deltas pairs up metrics with the same identity (name, source, unit, labels)
// and reports the movement of each. A metric present on one side only is
// reported rather than treated as zero.
func deltas(before, after []measurement.Metric) []MetricDelta {
	type entry struct{ before, after *measurement.Metric }
	byKey := map[string]*entry{}
	var order []string
	get := func(m measurement.Metric) *entry {
		k := metricKey(m)
		e, ok := byKey[k]
		if !ok {
			e = &entry{}
			byKey[k] = e
			order = append(order, k)
		}
		return e
	}
	for i := range before {
		get(before[i]).before = &before[i]
	}
	for i := range after {
		get(after[i]).after = &after[i]
	}

	var out []MetricDelta
	for _, k := range order {
		e := byKey[k]
		d := MetricDelta{Status: StatusCompared}
		switch {
		case e.before != nil:
			d.Name = e.before.Name
			d.Source = string(e.before.Source)
			d.Unit = e.before.Unit
			d.Case = e.before.Labels["case"]
			d.Labels = e.before.Labels
			d.Before = ptr(e.before.Value)
			d.BeforeSamples = e.before.Samples
		case e.after != nil:
			d.Name = e.after.Name
			d.Source = string(e.after.Source)
			d.Unit = e.after.Unit
			d.Case = e.after.Labels["case"]
			d.Labels = e.after.Labels
		}
		if e.after != nil {
			d.After = ptr(e.after.Value)
			d.AfterSamples = e.after.Samples
		}
		switch {
		case e.before == nil:
			d.Status = StatusOnlyAfter
		case e.after == nil:
			d.Status = StatusOnlyBefore
		default:
			abs := *d.After - *d.Before
			d.AbsChange = ptr(abs)
			if *d.Before != 0 {
				d.RelChange = ptr(abs / *d.Before)
			}
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Case != out[j].Case {
			return out[i].Case < out[j].Case
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// metricKey is the identity a delta is keyed by, matching the measurement
// package's rule: name, source, unit and labels.
func metricKey(m measurement.Metric) string {
	return m.Name + "\x00" + string(m.Source) + "\x00" + m.Unit + "\x00" + labelKey(m.Labels)
}

// ptr returns a pointer to a copy, so a measured zero survives encoding.
func ptr(v float64) *float64 { return &v }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func labelKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0x1f)
		b.WriteString(labels[k])
		b.WriteByte(0x1e)
	}
	return b.String()
}

// EnsureDir reports whether a path is a readable directory, so the CLI can
// fail with a clear message before it reads anything.
func EnsureDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("compare: %s is not a directory", path)
	}
	return nil
}
