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
type MetricDelta struct {
	Case   string  `json:"case,omitempty"`
	Name   string  `json:"name"`
	Source string  `json:"source"`
	Unit   string  `json:"unit,omitempty"`
	Before float64 `json:"before,omitempty"`
	After  float64 `json:"after,omitempty"`
	Abs    float64 `json:"abs_change,omitempty"`
	Rel    float64 `json:"rel_change,omitempty"`
	// BeforeSamples and AfterSamples are how many observations backed each
	// side, so a reader can see whether a difference is meaningful.
	BeforeSamples int `json:"before_samples,omitempty"`
	AfterSamples  int `json:"after_samples,omitempty"`
	// Present marks metrics that exist on one side only.
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
	if diff := promptDifference(before.Inputs, after.Inputs); diff != "" {
		add("%s", diff)
	}
	// The workload itself has to match, otherwise the numbers answer
	// different questions.
	if diff := workloadDifference(before, after); diff != "" {
		add("%s", diff)
	}
	switch kind {
	case KindModel:
		if before.Runtime.SpecDigest != after.Runtime.SpecDigest {
			add("the runtime spec differs but this is a model comparison")
		}
		if before.Inputs.ModelPath == after.Inputs.ModelPath {
			add("both sides measured %s: a model comparison needs two models", before.Inputs.ModelPath)
		}
	case KindRuntime:
		if before.Inputs.ModelPath != after.Inputs.ModelPath {
			add("the model path differs (%s vs %s) but this is a runtime comparison", before.Inputs.ModelPath, after.Inputs.ModelPath)
		}
		if before.Inputs.ModelDigest != after.Inputs.ModelDigest {
			add("the model digest differs but this is a runtime comparison")
		}
		if before.Runtime.BuildDigest != "" && before.Runtime.BuildDigest == after.Runtime.BuildDigest {
			add("both sides ran the same runtime build (%s): a runtime comparison needs two builds", before.Runtime.BuildDigest)
		}
	}
	// The environment must match: a GPU change is not a runtime change.
	if a, b := environmentKey(before), environmentKey(after); a != b {
		add("the measured environment differs: %s vs %s", a, b)
	}
	return reasons
}

func promptDifference(a, b measurement.Inputs) string {
	if len(a.Prompts) != len(b.Prompts) {
		return fmt.Sprintf("the workload cases differ (%d vs %d prompts)", len(a.Prompts), len(b.Prompts))
	}
	for name, digest := range a.Prompts {
		other, ok := b.Prompts[name]
		switch {
		case !ok:
			return fmt.Sprintf("case %q exists on one side only", name)
		case other != digest:
			return fmt.Sprintf("the prompt bytes of case %q differ", name)
		}
	}
	return ""
}

// workloadDifference compares the parts of the workload that decide what the
// numbers mean. The raw specs are not compared: a runtime comparison is
// expected to change runtime arguments.
func workloadDifference(before, after measurement.Result) string {
	a, b := before.Metrics, after.Metrics
	if len(a) == 0 || len(b) == 0 {
		return ""
	}
	cases := func(m []measurement.Metric) []string {
		var out []string
		seen := map[string]bool{}
		for _, metric := range m {
			c := metric.Labels["case"]
			if c != "" && !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
		sort.Strings(out)
		return out
	}
	if x, y := strings.Join(cases(a), ","), strings.Join(cases(b), ","); x != y {
		return fmt.Sprintf("the workload cases differ: %s vs %s", x, y)
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
			d.Before = e.before.Value
			d.BeforeSamples = e.before.Samples
		case e.after != nil:
			d.Name = e.after.Name
			d.Source = string(e.after.Source)
			d.Unit = e.after.Unit
			d.Case = e.after.Labels["case"]
		}
		if e.after != nil {
			d.After = e.after.Value
			d.AfterSamples = e.after.Samples
		}
		switch {
		case e.before == nil:
			d.Status = StatusOnlyAfter
		case e.after == nil:
			d.Status = StatusOnlyBefore
		default:
			d.Abs = d.After - d.Before
			if d.Before != 0 {
				d.Rel = d.Abs / d.Before
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
