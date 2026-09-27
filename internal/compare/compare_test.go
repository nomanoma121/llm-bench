package compare

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/measurement"
)

// workloadDigest renders a small counter as the hex digest the schema wants,
// so a test can make two runs share or differ in their workload.
func workloadDigest(n uint64) string {
	return fmt.Sprintf("%064x", n)
}

// result builds a sealed result directory with the given overrides.
type resultSpec struct {
	jobID        string
	kind         string
	modelPath    string
	modelDigest  string
	prompt       string
	runtimeSpec  string
	runtimeBuild string
	prompts      map[string]string
	invalid      []string
	metrics      []measurement.Metric
	environment  measurement.Environment
	workload     uint64 // a small counter rendered as a hex digest
	collectors   []measurement.CollectorStatus
}

func seal(t *testing.T, s resultSpec) string {
	t.Helper()
	dir := t.TempDir()
	series := []byte("{\"name\":\"ttft_ms\",\"value\":1}\n")
	workload := s.workload
	if workload == 0 {
		workload = 1
	}
	collectors := s.collectors
	if collectors == nil {
		collectors = []measurement.CollectorStatus{{Name: "harness", IntervalMS: 1}}
	}
	r := measurement.Result{
		SchemaVersion:    measurement.SchemaVersion,
		JobID:            s.jobID,
		Kind:             s.kind,
		JobSpecDigest:    strings.Repeat("a", 64),
		SeriesDigest:     measurement.Digest(series),
		TrustLevel:       measurement.TrustUnverifiedDriver,
		MeasurementValid: len(s.invalid) == 0,
		InvalidReasons:   s.invalid,
		Inputs: measurement.Inputs{
			ModelID:        "qwen38-27b",
			ModelPath:      s.modelPath,
			ModelDigest:    s.modelDigest,
			Prompts:        s.prompts,
			WorkloadDigest: workloadDigest(workload),
		},
		Runtime:     measurement.RuntimeRef{SpecDigest: s.runtimeSpec, BuildDigest: s.runtimeBuild},
		Collectors:  collectors,
		Environment: s.environment,
		Metrics:     s.metrics,
	}
	if r.Kind == "" {
		r.Kind = "benchmark"
	}
	if r.Inputs.Prompts == nil {
		r.Inputs.Prompts = map[string]string{"short": strings.Repeat("c", 64)}
	}
	if len(r.Metrics) == 0 {
		r.Metrics = []measurement.Metric{
			{Name: "ttft_ms", Value: 100, Unit: "ms", Source: measurement.SourceHarness, Labels: map[string]string{"case": "short"}, Samples: 3},
			{Name: "decode_tok_per_s", Value: 40, Unit: "tok/s", Source: measurement.SourceHarness, Labels: map[string]string{"case": "short"}, Samples: 3},
		}
	}
	if _, err := measurement.WriteResultDir(dir, []byte("kind: benchmark\n"), series, r); err != nil {
		t.Fatalf("write result: %v", err)
	}
	return dir
}

func base() resultSpec {
	return resultSpec{
		jobID:        "2026-09-27-issue1",
		modelPath:    "/models/a",
		modelDigest:  strings.Repeat("1", 64),
		runtimeSpec:  "r1",
		runtimeBuild: "sha256:" + strings.Repeat("2", 64),
		environment:  measurement.Environment{Driver: "580.1", GPUs: []measurement.GPU{{Model: "RTX 5090", Count: 1}}},
	}
}

func TestCompareRuntimeReportsFacts(t *testing.T) {
	a := seal(t, base())
	b := base()
	b.jobID = "2026-09-27-issue2"
	b.runtimeSpec = "r2"
	b.runtimeBuild = "sha256:" + strings.Repeat("3", 64)
	b.metrics = []measurement.Metric{
		{Name: "ttft_ms", Value: 80, Unit: "ms", Source: measurement.SourceHarness, Labels: map[string]string{"case": "short"}, Samples: 3},
		{Name: "decode_tok_per_s", Value: 50, Unit: "tok/s", Source: measurement.SourceHarness, Labels: map[string]string{"case": "short"}, Samples: 3},
	}
	c := seal(t, b)

	got, err := Compare(a, c, KindRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Comparable || len(got.Reasons) != 0 {
		t.Fatalf("not comparable: %v", got.Reasons)
	}
	if !got.MeasurementValid {
		t.Fatal("both sides are valid measurements")
	}
	byName := map[string]MetricDelta{}
	for _, d := range got.Metrics {
		byName[d.Name] = d
	}
	ttft := byName["ttft_ms"]
	if ttft.Status != StatusCompared || ttft.AbsChange == nil || *ttft.AbsChange != -20 || ttft.RelChange == nil || *ttft.RelChange != -0.2 {
		t.Fatalf("ttft delta = %+v", ttft)
	}
	decode := byName["decode_tok_per_s"]
	if decode.AbsChange == nil || *decode.AbsChange != 10 || decode.RelChange == nil || *decode.RelChange != 0.25 {
		t.Fatalf("decode delta = %+v", decode)
	}
	if decode.Labels["case"] != "short" {
		t.Fatalf("delta labels = %v", decode.Labels)
	}
	if decode.Case != "short" || decode.Source != string(measurement.SourceHarness) {
		t.Fatalf("delta identity = %+v", decode)
	}
	// The comparison reports facts only: no field may carry a verdict.
	body := strings.ToLower(strings.Join([]string{ttft.Status, decode.Status}, " "))
	for _, forbidden := range []string{"accept", "reject", "better", "worse", "recommend"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("verdict %q leaked into %q", forbidden, body)
		}
	}
}

func TestCompareRejectsIncomparablePairs(t *testing.T) {
	tests := []struct {
		name   string
		kind   Kind
		mutate func(*resultSpec)
		want   string
	}{
		{"different prompt bytes", KindRuntime, func(s *resultSpec) {
			s.prompts = map[string]string{"short": strings.Repeat("d", 64)}
		}, "prompt bytes"},
		{"different cases", KindRuntime, func(s *resultSpec) {
			s.prompts = map[string]string{"short": strings.Repeat("c", 64), "long": strings.Repeat("e", 64)}
		}, `case "long" exists in the candidate only`},
		{"different model in a runtime comparison", KindRuntime, func(s *resultSpec) {
			s.modelDigest = strings.Repeat("7", 64)
		}, "model digest differs"},
		{"different workload", KindRuntime, func(s *resultSpec) { s.workload = 2 }, "workload (cases, sampling, collectors) differs"},
		{"different collector interval", KindRuntime, func(s *resultSpec) {
			s.collectors = []measurement.CollectorStatus{{Name: "harness", IntervalMS: 1}, {Name: "runtime", IntervalMS: 1000}}
		}, "collector configuration differs"},
		{"different model digest in a runtime comparison", KindRuntime, func(s *resultSpec) {
			s.modelDigest = strings.Repeat("9", 64)
		}, "model digest differs"},
		{"same runtime build", KindRuntime, func(s *resultSpec) {
			s.runtimeBuild = "sha256:" + strings.Repeat("2", 64)
		}, "same runtime build"},
		{"missing build digest", KindRuntime, func(s *resultSpec) { s.runtimeBuild = "" }, "does not record its runtime build digest"},
		{"missing model digest", KindRuntime, func(s *resultSpec) { s.modelDigest = "" }, "does not record a model digest"},
		{"different environment", KindRuntime, func(s *resultSpec) {
			s.environment = measurement.Environment{Driver: "580.1", GPUs: []measurement.GPU{{Model: "RTX 3090", Count: 1}}}
		}, "environment differs"},
		{"different runtime in a model comparison", KindModel, func(s *resultSpec) { s.runtimeSpec = "r9" }, "runtime spec differs"},
		{"same model in a model comparison", KindModel, func(s *resultSpec) {
			s.modelPath = "/models/a"
			s.runtimeSpec = "r1"
			s.runtimeBuild = "sha256:" + strings.Repeat("2", 64)
			s.modelDigest = strings.Repeat("1", 64)
		}, "needs two models"},
		{"different runtime build in a model comparison", KindModel, func(s *resultSpec) {
			s.runtimeBuild = "sha256:" + strings.Repeat("4", 64)
		}, "runtime build differs"},
		{"invalid side", KindRuntime, func(s *resultSpec) { s.invalid = []string{"collector gap"} }, "invalid"},
		{"unknown environment", KindRuntime, func(s *resultSpec) {
			s.environment = measurement.Environment{}
		}, "does not record its environment"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := seal(t, base())
			b := base()
			b.jobID = "2026-09-27-issue3"
			b.runtimeSpec = "r2"
			b.runtimeBuild = "sha256:" + strings.Repeat("3", 64)
			// A model comparison needs a different model and the same runtime.
			if tc.kind == KindModel {
				b.modelPath = "/models/b"
				b.modelDigest = strings.Repeat("2", 64)
				b.runtimeSpec = "r1"
				b.runtimeBuild = "sha256:" + strings.Repeat("2", 64)
			}
			if tc.mutate != nil {
				tc.mutate(&b)
			}
			c := seal(t, b)
			got, err := Compare(a, c, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if got.Comparable {
				t.Fatalf("compared a pair that should be rejected")
			}
			if !strings.Contains(strings.Join(got.Reasons, " "), tc.want) {
				t.Fatalf("reasons = %v, want %q", got.Reasons, tc.want)
			}
		})
	}
}

func TestCompareRejectsTheSameResultAndBadInput(t *testing.T) {
	a := seal(t, base())
	if _, err := Compare(a, a, KindRuntime); err == nil {
		t.Fatal("comparing a result with itself succeeded")
	}
	if _, err := Compare(a, filepath.Join(t.TempDir(), "missing"), KindRuntime); err == nil {
		t.Fatal("a missing directory succeeded")
	}
	if _, err := Compare(a, a, "visual"); err == nil {
		t.Fatal("an unknown kind succeeded")
	}
}

func TestCompareDetectsATamperedSidecar(t *testing.T) {
	a := seal(t, base())
	b := base()
	b.jobID = "2026-09-27-issue4"
	b.runtimeSpec = "r2"
	b.runtimeBuild = "sha256:" + strings.Repeat("3", 64)
	c := seal(t, b)
	if err := os.WriteFile(filepath.Join(c, measurement.SeriesFileName), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Compare(a, c, KindRuntime); err == nil {
		t.Fatal("a tampered series.jsonl was compared instead of rejected")
	}
}

func TestDeltasReportOneSidedMetrics(t *testing.T) {
	before := []measurement.Metric{{Name: "a", Unit: "ms", Source: measurement.SourceHarness, Value: 1}}
	after := []measurement.Metric{
		{Name: "a", Unit: "ms", Source: measurement.SourceHarness, Value: 3},
		{Name: "b", Unit: "ms", Source: measurement.SourceHarness, Value: 5},
	}
	out := deltas(before, after)
	if len(out) != 2 {
		t.Fatalf("deltas = %+v", out)
	}
	if out[0].Name != "a" || out[0].Status != StatusCompared || out[0].AbsChange == nil || *out[0].AbsChange != 2 {
		t.Fatalf("paired delta = %+v", out[0])
	}
	if out[1].Name != "b" || out[1].Status != StatusOnlyAfter || out[1].After == nil || *out[1].After != 5 {
		t.Fatalf("one-sided delta = %+v", out[1])
	}
	if out[1].Before != nil {
		t.Fatal("an absent side must not report a value")
	}
}

func TestZeroValuesSurviveTheEncoding(t *testing.T) {
	// A measured zero is a fact; an absent side and an undefined relative
	// change are different facts and must not be printed as zero.
	before := []measurement.Metric{{Name: "a", Unit: "ms", Source: measurement.SourceHarness, Value: 0}}
	after := []measurement.Metric{{Name: "a", Unit: "ms", Source: measurement.SourceHarness, Value: 10}}
	out := deltas(before, after)
	if len(out) != 1 {
		t.Fatalf("deltas = %+v", out)
	}
	d := out[0]
	if d.Before == nil || *d.Before != 0 {
		t.Fatalf("a measured zero was dropped: %+v", d)
	}
	if d.AbsChange == nil || *d.AbsChange != 10 {
		t.Fatalf("abs change = %+v", d.AbsChange)
	}
	if d.RelChange != nil {
		t.Fatalf("relative change must be undefined when the baseline is zero, got %v", *d.RelChange)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MetricDelta
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Before == nil || *decoded.Before != 0 {
		t.Fatalf("zero did not survive the round trip: %s", b)
	}
	if decoded.RelChange != nil {
		t.Fatalf("undefined rel_change was encoded: %s", b)
	}
	if strings.Contains(string(b), "rel_change") {
		t.Fatalf("rel_change is present in %s", b)
	}
}

func TestPromptDifferencesAreExhaustive(t *testing.T) {
	// Both prompts differ: a single return would hide the second reason.
	a := measurement.Inputs{Prompts: map[string]string{"short": "1", "long": "2"}}
	b := measurement.Inputs{Prompts: map[string]string{"short": "3", "long": "4", "extra": "5"}}
	got := promptDifferences(a, b)
	if len(got) != 3 {
		t.Fatalf("reasons = %v, want one per differing or extra case", got)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"short", "long", "extra"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%q missing from %v", want, got)
		}
	}
}
