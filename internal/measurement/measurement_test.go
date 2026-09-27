package measurement

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func base() Evidence {
	return Evidence{
		SchemaVersion:    SchemaVersion,
		RunID:            "0123456789abcdef0123456789abcdef",
		Kind:             "measurement",
		Protocol:         Ref{ID: "longctx", Digest: "pd"},
		MeasurementValid: true,
		Metrics: []Metric{
			{Name: "decode_step_ms", Value: 17.71, Unit: "ms/step", Source: SourceDriver, Labels: map[string]string{"depth": "64k"}, Samples: 385},
		},
	}
}

func TestCanonicalizeIsDeterministic(t *testing.T) {
	first, err := Canonicalize(base())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Canonicalize(base())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("canonical form is not stable")
	}
	// Order of metrics and reasons must not change the identity.
	shuffled := base()
	shuffled.Metrics = append(shuffled.Metrics, Metric{Name: "prefill_ms", Value: 20.6, Unit: "ms", Source: SourceDriver})
	shuffled.InvalidReasons = []string{}
	a, err := Canonicalize(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	reordered := shuffled
	reordered.Metrics = []Metric{shuffled.Metrics[1], shuffled.Metrics[0]}
	b, err := Canonicalize(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("metric order changed the identity:\n%s\n%s", a, b)
	}
	if Digest(a) == Digest(first) {
		t.Fatal("adding a metric must change the digest")
	}
}

func TestValidateIsFailClosed(t *testing.T) {
	cases := map[string]func(*Evidence){
		"schema version":   func(e *Evidence) { e.SchemaVersion = 2 },
		"missing run id":   func(e *Evidence) { e.RunID = "" },
		"bad kind":         func(e *Evidence) { e.Kind = "benchmark" },
		"missing protocol": func(e *Evidence) { e.Protocol = Ref{} },
		"validity flag": func(e *Evidence) {
			e.MeasurementValid = true
			e.InvalidReasons = []string{"collector gap"}
		},
		"unknown source": func(e *Evidence) { e.Metrics[0].Source = "vibes" },
		"non-finite":     func(e *Evidence) { e.Metrics[0].Value = mathNaN() },
		"no metrics but valid": func(e *Evidence) {
			e.Metrics = nil
		},
		"series over limit": func(e *Evidence) {
			points := make([][2]float64, MaxSeriesPointsPerSeries+1)
			e.Series = []Series{{Name: "s", Unit: "ms", Source: SourceHarness, Points: points, OriginalCount: len(points)}}
		},
		"downsample without method": func(e *Evidence) {
			e.Series = []Series{{Name: "s", Unit: "ms", Source: SourceHarness, Points: [][2]float64{{0, 1}}, OriginalCount: 10}}
		},
		"collector without interval": func(e *Evidence) {
			e.Collectors = []CollectorStatus{{Name: "nvidia-smi"}}
		},
		"empty invalid reason": func(e *Evidence) {
			e.MeasurementValid = false
			e.InvalidReasons = []string{" "}
		},
	}
	for name, mutate := range cases {
		e := base()
		mutate(&e)
		if err := e.Validate(); err == nil {
			t.Fatalf("%s: expected a validation error", name)
		} else if !errors.Is(err, ValidationError) {
			t.Fatalf("%s: error is not a ValidationError: %v", name, err)
		}
	}
	// An invalid measurement stays legal as long as it carries reasons.
	invalid := base()
	invalid.MeasurementValid = false
	invalid.InvalidReasons = []string{"collector gap: nvidia-smi"}
	if err := invalid.Validate(); err != nil {
		t.Fatalf("invalid measurement with a reason must validate: %v", err)
	}
}

func TestSealIsAtomicAndVerifiable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), EvidenceDir)
	sealed, err := Seal(dir, base())
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Path != filepath.Join(dir, EvidenceFileName) {
		t.Fatalf("path = %s", sealed.Path)
	}
	if sealed.Digest == "" || sealed.Bytes == 0 {
		t.Fatalf("sealed = %+v", sealed)
	}
	// No temporary files are left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != EvidenceFileName {
		t.Fatalf("leftover files: %v", entries)
	}
	e, err := Verify(sealed.Path, sealed.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if e.Metrics[0].Name != "decode_step_ms" {
		t.Fatalf("evidence round-trip failed: %+v", e)
	}
	// Tampering is corruption, not an inconclusive measurement.
	if err := os.WriteFile(sealed.Path, []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(sealed.Path, sealed.Digest); err == nil {
		t.Fatal("expected a digest mismatch to be an error")
	}
}

func TestCanonicalizeEnforcesTheByteLimit(t *testing.T) {
	// The section caps already bound the realistic size, so the byte guard is
	// exercised directly with a small limit.
	if _, err := canonicalize(base(), 16); err == nil {
		t.Fatal("expected the byte limit to reject the evidence")
	}
	if _, err := canonicalize(base(), MaxEvidenceBytes); err != nil {
		t.Fatalf("the same evidence must fit the real limit: %v", err)
	}
}

func TestSealRejectsInvalidEvidence(t *testing.T) {
	dir := t.TempDir()
	bad := base()
	bad.Metrics[0].Source = "nope"
	if _, err := Seal(dir, bad); err == nil {
		t.Fatal("expected invalid evidence to be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, EvidenceFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a rejected seal must not leave a file")
	}
	// Too many labels is a metadata-exhaustion attempt.
	many := base()
	many.Metrics[0].Labels = map[string]string{}
	for i := 0; i < MaxLabelsPerMetric+1; i++ {
		many.Metrics[0].Labels["k"+itoa(i)] = "v"
	}
	if _, err := Seal(dir, many); err == nil {
		t.Fatal("expected the label bound to reject the evidence")
	}
}

func TestWithHarnessMetricReplacesByLabels(t *testing.T) {
	e := WithHarnessMetric(base(), "wall_clock_ms", 1000, "ms", nil)
	e = WithHarnessMetric(e, "wall_clock_ms", 1200, "ms", nil)
	count := 0
	for _, m := range e.Metrics {
		if m.Name == "wall_clock_ms" {
			count++
			if m.Value != 1200 || m.Source != SourceHarness {
				t.Fatalf("metric = %+v", m)
			}
		}
	}
	if count != 1 {
		t.Fatalf("harness metric was duplicated %d times", count)
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}

func mathNaN() float64 {
	var zero float64
	return zero / zero
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestParseRawRejectsUnusableInput(t *testing.T) {
	if _, err := ParseRaw([]byte(`{"metrics":[]}`)); err == nil {
		t.Fatal("expected empty raw metrics to be rejected")
	}
	if _, err := ParseRaw([]byte(`nonsense`)); err == nil {
		t.Fatal("expected invalid JSON to be rejected")
	}
	if _, err := ParseRaw([]byte(`{"metrics":[{"name":"x","value":1,"unit":"ms","source":"runtime"}]}`)); err == nil {
		t.Fatal("raw input must not be able to set its own source")
	}
	raw, err := ParseRaw([]byte(`{"metrics":[{"name":"prefill_ms","value":20.6,"unit":"ms","labels":{"depth":"64k"},"samples":3}]}`))
	if err != nil {
		t.Fatal(err)
	}
	e := base().AppendRaw(raw, SourceDriver)
	if len(e.Metrics) != 2 {
		t.Fatalf("evidence = %+v", e.Metrics)
	}
	found := false
	for _, m := range e.Metrics {
		if m.Name == "prefill_ms" {
			found = true
			if m.Source != SourceDriver {
				t.Fatalf("source = %q", m.Source)
			}
		}
	}
	if !found {
		t.Fatal("raw metric missing")
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateIdentitiesAreRejected(t *testing.T) {
	e := base()
	e.Metrics = append(e.Metrics, e.Metrics[0])
	if err := e.Validate(); err == nil {
		t.Fatal("expected a duplicate metric to be rejected")
	}
	// Same name with different labels is legal and ordered deterministically.
	a := base()
	a.Metrics = []Metric{
		{Name: "decode_step_ms", Value: 1, Unit: "ms/step", Source: SourceDriver, Labels: map[string]string{"depth": "128k"}},
		{Name: "decode_step_ms", Value: 2, Unit: "ms/step", Source: SourceDriver, Labels: map[string]string{"depth": "64k"}},
	}
	b := a
	b.Metrics = []Metric{a.Metrics[1], a.Metrics[0]}
	ba, err := Canonicalize(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := Canonicalize(b)
	if err != nil {
		t.Fatal(err)
	}
	if Digest(ba) != Digest(bb) {
		t.Fatalf("label-distinguished metrics must canonicalize identically:\n%s\n%s", ba, bb)
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidityEnforcement(t *testing.T) {
	// Collector gaps cannot coexist with a valid measurement.
	gapped := base()
	gapped.Collectors = []CollectorStatus{{Name: "nvidia-smi", IntervalMS: 500, Gaps: 2}}
	if err := gapped.Validate(); err == nil {
		t.Fatal("expected gaps with measurement_valid to be rejected")
	}
	gapped.MeasurementValid = false
	gapped.InvalidReasons = []string{"collector nvidia-smi reported 2 gaps"}
	if err := gapped.Validate(); err != nil {
		t.Fatal(err)
	}

	// A required source that produced nothing makes the measurement invalid.
	e := measurementWithHarnessOnly()
	e = EnforceRequiredSources(e, []string{"driver"})
	if e.MeasurementValid || len(e.InvalidReasons) == 0 {
		t.Fatalf("expected invalidity: %+v", e)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("invalid evidence with reasons must validate: %v", err)
	}
	// Present sources keep it valid.
	ok := measurementWithHarnessOnly()
	ok = ok.AppendRaw(Raw{Metrics: []RawMetric{{Name: "decode_step_ms", Value: 1, Unit: "ms/step"}}}, SourceDriver)
	ok = EnforceRequiredSources(ok, []string{"driver"})
	if !ok.MeasurementValid || len(ok.InvalidReasons) != 0 {
		t.Fatalf("expected a valid measurement: %+v", ok)
	}
	// Collector gaps are also enforced by the helper.
	dirty := measurementWithHarnessOnly()
	dirty.Collectors = []CollectorStatus{{Name: "nvidia-smi", IntervalMS: 500, Gaps: 1}}
	dirty = EnforceCollectorGaps(dirty)
	if dirty.MeasurementValid || len(dirty.InvalidReasons) == 0 {
		t.Fatalf("expected gaps to invalidate: %+v", dirty)
	}
}

func measurementWithHarnessOnly() Evidence {
	e := base()
	e.Metrics = nil
	return WithHarnessMetric(e, "wall_clock_ms", 1000, "ms", nil)
}
