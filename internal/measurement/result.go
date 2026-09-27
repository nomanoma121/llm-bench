package measurement

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The MVP writes one result document per job (docs/mvp.md §4.3). It reuses the
// metric/series/collector schema of the frozen evidence document but is keyed
// by the job rather than by a run, and it closes the digest loop: the digest of
// the job spec and the digest of the raw series file are recorded *inside* the
// payload, and the result digest covers them. Editing jobspec.yaml or
// series.jsonl therefore changes the identity of the result, which is what
// makes the recorded identity trustworthy for a later comparison.
const (
	// ResultFileName is the result document inside a job's output directory.
	ResultFileName = "result.json"
	// SeriesFileName is the raw sample file whose digest the result records.
	SeriesFileName = "series.jsonl"
	// JobSpecFileName is the frozen copy of the accepted job spec.
	JobSpecFileName = "jobspec.yaml"
)

// Result trust levels. Driver isolation (executing a driver from a path the
// candidate cannot write, and verifying Driver.ContentDigest at run time) is
// deferred, so a result records how much its measurements can be trusted
// instead of implying a guarantee it does not have.
const (
	// TrustUnverifiedDriver means the driver ran inside the sandbox with the
	// same write access as the candidate.
	TrustUnverifiedDriver = "unverified-driver"
	// TrustIsolatedDriver means the driver ran from a candidate-unwritable
	// path and its content digest was verified before execution.
	TrustIsolatedDriver = "isolated-driver"
)

// Result is the canonical measurement document for one MVP job.
type Result struct {
	SchemaVersion int    `json:"schema_version"`
	JobID         string `json:"job_id"`
	Kind          string `json:"kind"`

	// JobSpecDigest identifies the effective job spec (the accepted spec
	// including the values the operator resolved).
	JobSpecDigest string `json:"jobspec_digest"`
	// SeriesDigest is the digest of the raw series file on disk.
	SeriesDigest string `json:"series_digest"`
	// TrustLevel records how the measurements were produced.
	TrustLevel string `json:"trust_level"`

	// MeasurementValid answers "can this measurement be trusted", never "did
	// the candidate perform well". InvalidReasons must be empty exactly when
	// it is true.
	MeasurementValid bool     `json:"measurement_valid"`
	InvalidReasons   []string `json:"invalid_reasons,omitempty"`

	Metrics     []Metric          `json:"metrics"`
	Series      []Series          `json:"series,omitempty"`
	Collectors  []CollectorStatus `json:"collectors,omitempty"`
	Environment Environment       `json:"environment"`
	Runtime     RuntimeRef        `json:"runtime"`

	// ResultDigest is the digest of the canonical payload above. It is always
	// the last field so the payload that is hashed is unambiguous.
	ResultDigest string `json:"result_digest"`
}

// Validate checks the schema and the invariants a sealed result must hold.
func (r Result) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version %d", ValidationError, r.SchemaVersion)
	}
	if err := checkIdentity("job_id", r.JobID); err != nil {
		return err
	}
	if err := checkIdentity("kind", r.Kind); err != nil {
		return err
	}
	switch r.Kind {
	case "benchmark", "optimize":
	default:
		return fmt.Errorf("%w: kind %q", ValidationError, r.Kind)
	}
	for name, d := range map[string]string{"jobspec_digest": r.JobSpecDigest, "series_digest": r.SeriesDigest} {
		if !isHexDigest(d) {
			return fmt.Errorf("%w: %s %q is not a sha256 digest", ValidationError, name, d)
		}
	}
	switch r.TrustLevel {
	case TrustUnverifiedDriver, TrustIsolatedDriver:
	default:
		return fmt.Errorf("%w: trust_level %q", ValidationError, r.TrustLevel)
	}
	if r.MeasurementValid != (len(r.InvalidReasons) == 0) {
		return fmt.Errorf("%w: measurement_valid and invalid_reasons disagree", ValidationError)
	}
	if err := validateMeasurements(r.Metrics, r.Series, r.Collectors, r.MeasurementValid); err != nil {
		return err
	}
	want, err := r.computeDigest()
	if err != nil {
		return err
	}
	if r.ResultDigest != want {
		return fmt.Errorf("%w: result_digest %s does not match the payload digest %s", ValidationError, r.ResultDigest, want)
	}
	return nil
}

// computeDigest hashes the canonical payload without the digest field.
func (r Result) computeDigest() (string, error) {
	r.ResultDigest = ""
	b, err := json.Marshal(r.normalized())
	if err != nil {
		return "", fmt.Errorf("measurement: canonical result: %w", err)
	}
	return Digest(b), nil
}

// Finalize fills in ResultDigest. Callers build the payload, finalize, then
// seal: the digest can only be computed once every other field is set.
func (r Result) Finalize() (Result, error) {
	d, err := r.computeDigest()
	if err != nil {
		return Result{}, err
	}
	r.ResultDigest = d
	return r, nil
}

// normalized orders the sections so the canonical bytes do not depend on how
// the caller built the slices.
func (r Result) normalized() Result {
	out := r
	out.InvalidReasons = append([]string(nil), r.InvalidReasons...)
	sort.Strings(out.InvalidReasons)
	out.Metrics = append([]Metric(nil), r.Metrics...)
	sort.Slice(out.Metrics, func(i, j int) bool { return metricKey(out.Metrics[i]) < metricKey(out.Metrics[j]) })
	out.Series = append([]Series(nil), r.Series...)
	sort.Slice(out.Series, func(i, j int) bool { return seriesKey(out.Series[i]) < seriesKey(out.Series[j]) })
	out.Collectors = append([]CollectorStatus(nil), r.Collectors...)
	sort.Slice(out.Collectors, func(i, j int) bool { return out.Collectors[i].Name < out.Collectors[j].Name })
	return out
}

// CanonicalizeResult renders the result deterministically. It validates first,
// so a malformed result is never sealed.
func CanonicalizeResult(r Result) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(r.normalized())
	if err != nil {
		return nil, fmt.Errorf("measurement: canonicalize result: %w", err)
	}
	if len(b) > MaxEvidenceBytes {
		return nil, fmt.Errorf("%w: %d bytes exceed the limit %d", ValidationError, len(b), MaxEvidenceBytes)
	}
	return b, nil
}

// SeriesDigest is the digest of a raw series file. The file is hashed as
// written (one sample per line, in measurement order), so re-ordering or
// editing a sample changes the result identity.
func SeriesDigest(b []byte) string { return Digest(b) }

// SealResult writes the result atomically and returns its digest.
func SealResult(dir string, r Result) (Sealed, error) {
	b, err := CanonicalizeResult(r)
	if err != nil {
		return Sealed{}, err
	}
	return atomicSeal(dir, ResultFileName, ".result-", b)
}

// LoadResult reads and validates a sealed result, returning it with the digest
// of the bytes on disk.
func LoadResult(path string) (Result, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, "", err
	}
	var r Result
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Result{}, "", fmt.Errorf("measurement: decode %s: %w", path, err)
	}
	if err := r.Validate(); err != nil {
		return Result{}, "", err
	}
	return r, Digest(b), nil
}

// VerifyResult checks a result file against a recorded digest. A mismatch is
// corruption, not an inconclusive measurement, so it is an error.
func VerifyResult(path, want string) (Result, error) {
	r, got, err := LoadResult(path)
	if err != nil {
		return Result{}, err
	}
	if got != want {
		return Result{}, fmt.Errorf("measurement: %s digest %s does not match the recorded %s", path, got, want)
	}
	return r, nil
}

// ResultPath is the result document inside a job output directory.
func ResultPath(dir string) string { return filepath.Join(dir, ResultFileName) }

// isHexDigest accepts a bare lowercase sha256.
func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
