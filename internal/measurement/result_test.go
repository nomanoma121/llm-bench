package measurement

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validResult(t *testing.T) Result {
	t.Helper()
	r := Result{
		SchemaVersion: SchemaVersion,
		JobID:         "2026-09-27-issue42",
		Kind:          "benchmark",
		JobSpecDigest: strings.Repeat("a", 64),
		SeriesDigest:  SeriesDigest([]byte("{\"name\":\"decode\"}\n")),
		TrustLevel:    TrustUnverifiedDriver,
		Metrics: []Metric{
			{Name: "decode_tok_per_s", Value: 45.1, Unit: "tok/s", Source: SourceHarness, Samples: 3},
			{Name: "ttft_ms", Value: 120.5, Unit: "ms", Source: SourceHarness, Samples: 3},
		},
		Collectors: []CollectorStatus{{Name: "harness", IntervalMS: 1}},
		Environment: Environment{
			GPUs: []GPU{{Model: "RTX 5090", Count: 1}},
		},
		MeasurementValid: true,
	}
	final, err := r.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if final.ResultDigest == "" {
		t.Fatal("finalize did not set result_digest")
	}
	return final
}

func TestResultSealLoadVerify(t *testing.T) {
	dir := t.TempDir()
	r := validResult(t)
	sealed, err := SealResult(dir, r)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed.Path != ResultPath(dir) {
		t.Fatalf("sealed path = %s", sealed.Path)
	}
	got, digest, err := LoadResult(sealed.Path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if digest != sealed.Digest {
		t.Fatalf("load digest %s != seal digest %s", digest, sealed.Digest)
	}
	if got.ResultDigest != r.ResultDigest {
		t.Fatalf("result_digest changed: %s != %s", got.ResultDigest, r.ResultDigest)
	}
	if _, err := VerifyResult(sealed.Path, sealed.Digest); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := VerifyResult(sealed.Path, strings.Repeat("b", 64)); err == nil {
		t.Fatal("expected a digest mismatch")
	}
}

func TestResultDigestIsStableAndCoversSeries(t *testing.T) {
	a := validResult(t)
	again := validResult(t)
	if a.ResultDigest != again.ResultDigest {
		t.Fatalf("digest is not stable: %s != %s", a.ResultDigest, again.ResultDigest)
	}
	// Editing the raw series must change the result identity, which is the
	// contradiction the spec review found.
	b := validResult(t)
	b.SeriesDigest = SeriesDigest([]byte("{\"name\":\"decode\",\"x\":1}\n"))
	final, err := b.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if final.ResultDigest == a.ResultDigest {
		t.Fatal("result_digest ignores series_digest")
	}
	// The same is true for the job spec.
	c := validResult(t)
	c.JobSpecDigest = strings.Repeat("c", 64)
	final, err = c.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if final.ResultDigest == a.ResultDigest {
		t.Fatal("result_digest ignores jobspec_digest")
	}
}

func TestResultDigestIsOrderIndependent(t *testing.T) {
	a := validResult(t)
	b := validResult(t)
	b.Metrics[0], b.Metrics[1] = b.Metrics[1], b.Metrics[0]
	final, err := b.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if final.ResultDigest != a.ResultDigest {
		t.Fatalf("metric order changed the digest: %s != %s", final.ResultDigest, a.ResultDigest)
	}
}

func TestResultValidateRejects(t *testing.T) {
	base := validResult(t)
	tests := []struct {
		name string
		edit func(*Result)
		want string
	}{
		{"schema", func(r *Result) { r.SchemaVersion = 99 }, "schema_version"},
		{"kind", func(r *Result) { r.Kind = "visual" }, "kind"},
		{"job id", func(r *Result) { r.JobID = "" }, "job_id"},
		{"jobspec digest", func(r *Result) { r.JobSpecDigest = "sha256:abc" }, "jobspec_digest"},
		{"series digest", func(r *Result) { r.SeriesDigest = "" }, "series_digest"},
		{"trust level", func(r *Result) { r.TrustLevel = "trusted" }, "trust_level"},
		{"validity disagreement", func(r *Result) { r.InvalidReasons = []string{"x"} }, "disagree"},
		{"no metrics but valid", func(r *Result) { r.Metrics = nil }, "at least one metric"},
		{"collector gap while valid", func(r *Result) {
			r.Collectors = []CollectorStatus{{Name: "harness", IntervalMS: 1, Gaps: 1}}
		}, "gaps"},
		{"tampered digest", func(r *Result) { r.ResultDigest = strings.Repeat("d", 64) }, "does not match"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.edit(&r)
			err := r.Validate()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, ValidationError) {
				t.Fatalf("error %v does not wrap ValidationError", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestResultInvalidMeasurementMayBeSealed(t *testing.T) {
	// An invalid measurement is a fact worth recording: it must be sealable
	// with reasons, so the Agent can see why a round was inconclusive.
	r := validResult(t)
	r.MeasurementValid = false
	r.InvalidReasons = []string{"nvidia collector missed 3 samples"}
	final, err := r.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	dir := t.TempDir()
	if _, err := SealResult(dir, final); err != nil {
		t.Fatalf("seal: %v", err)
	}
}

func TestLoadResultRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ResultFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"extra":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadResult(path); err == nil {
		t.Fatal("expected unknown fields to be rejected")
	}
}

func TestCanonicalizeResultIsDeterministic(t *testing.T) {
	r := validResult(t)
	first, err := CanonicalizeResult(r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalizeResult(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("canonicalization is not deterministic")
	}
}
