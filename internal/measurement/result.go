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

// Inputs identify what was actually measured. The job spec carries a model id
// and a prompt path; this section freezes what those resolved to, so two runs
// that read different weights or different prompt bytes cannot look like the
// same input.
type Inputs struct {
	// ModelID is the id from the job spec.
	ModelID string `json:"model_id"`
	// ModelPath is the path or repository id the operator resolved it to.
	ModelPath string `json:"model_path"`
	// ModelDigest is the operator-pinned digest of the weights, when one is
	// configured. Empty means the operator did not pin one.
	ModelDigest string `json:"model_digest,omitempty"`
	// Prompts maps a case name to the digest of the prompt bytes that case
	// actually measured.
	Prompts map[string]string `json:"prompts,omitempty"`
	// WorkloadDigest identifies the workload itself: the cases, their sampling
	// and the collector set. The job spec digest cannot serve this purpose
	// because a runtime comparison is expected to change runtime arguments.
	WorkloadDigest string `json:"workload_digest"`
}

// Result is the canonical measurement document for one MVP job.
type Result struct {
	SchemaVersion int    `json:"schema_version"`
	JobID         string `json:"job_id"`
	Kind          string `json:"kind"`

	// JobSpecDigest identifies the effective job spec (the accepted spec
	// including the values the operator resolved).
	JobSpecDigest string `json:"jobspec_digest"`
	// JobSpecFileDigest is the digest of the frozen jobspec.yaml on disk. The
	// semantic digest above survives reformatting; this one ties the result to
	// the exact bytes that sit next to it.
	JobSpecFileDigest string `json:"jobspec_file_digest"`
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
	// Inputs freeze what was measured (the resolved model and the prompt
	// bytes), so a later comparison cannot mistake two different inputs for
	// the same one.
	Inputs Inputs `json:"inputs"`

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
	for name, d := range map[string]string{
		"jobspec_digest":      r.JobSpecDigest,
		"jobspec_file_digest": r.JobSpecFileDigest,
		"series_digest":       r.SeriesDigest,
	} {
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
	if strings.TrimSpace(r.Inputs.ModelID) == "" || strings.TrimSpace(r.Inputs.ModelPath) == "" {
		return fmt.Errorf("%w: inputs.model_id and inputs.model_path are required", ValidationError)
	}
	if r.Inputs.ModelDigest != "" && !isHexDigest(r.Inputs.ModelDigest) {
		return fmt.Errorf("%w: inputs.model_digest %q is not a sha256 digest", ValidationError, r.Inputs.ModelDigest)
	}
	for name, d := range r.Inputs.Prompts {
		if strings.TrimSpace(name) == "" || !isHexDigest(d) {
			return fmt.Errorf("%w: inputs.prompts[%q] is not a case name with a sha256 digest", ValidationError, name)
		}
	}
	if !isHexDigest(r.Inputs.WorkloadDigest) {
		return fmt.Errorf("%w: inputs.workload_digest %q is not a sha256 digest", ValidationError, r.Inputs.WorkloadDigest)
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

// VerifyResultDir verifies a whole job output directory: the result document
// against its own recorded digest, and the sidecar files the result names
// (jobspec.yaml and series.jsonl) against the digests the result records.
//
// Verifying result.json alone would be useless: a tampered series.jsonl would
// leave the result "valid" even though the identity it claims no longer
// describes what is on disk. Readers that care about identity must use this
// function, not LoadResult.
func VerifyResultDir(dir string) (Result, error) {
	r, _, err := LoadResult(ResultPath(dir))
	if err != nil {
		return Result{}, err
	}
	for _, ref := range []struct {
		file string
		want string
	}{
		{JobSpecFileName, r.JobSpecFileDigest},
		{SeriesFileName, r.SeriesDigest},
	} {
		got, err := FileDigest(filepath.Join(dir, ref.file))
		if err != nil {
			return Result{}, err
		}
		if got != ref.want {
			return Result{}, fmt.Errorf("measurement: %s digest %s does not match the result's %s", ref.file, got, ref.want)
		}
	}
	return r, nil
}

// FileDigest is the digest of a file's bytes. The result records sidecar
// digests with it, and VerifyResultDir recomputes them with it.
func FileDigest(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("measurement: digest %s: %w", path, err)
	}
	return Digest(b), nil
}

// ResultPath is the result document inside a job output directory.
func ResultPath(dir string) string { return filepath.Join(dir, ResultFileName) }

// WriteResultDir writes a job output directory in the order that keeps the
// recorded digests true: the sidecar files first (fsynced), then the result
// document. A crash between the two leaves a directory without a result, which
// VerifyResultDir reports as missing rather than as valid.
//
// The caller passes the bytes it already wrote or is about to publish; the
// digests are computed here so the writer and VerifyResultDir cannot disagree
// about what is hashed.
func WriteResultDir(dir string, jobspecYAML, seriesJSONL []byte, r Result) (Sealed, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Sealed{}, fmt.Errorf("measurement: result dir: %w", err)
	}
	for name, b := range map[string][]byte{JobSpecFileName: jobspecYAML, SeriesFileName: seriesJSONL} {
		if err := writeFileSync(filepath.Join(dir, name), b); err != nil {
			return Sealed{}, err
		}
	}
	r.JobSpecFileDigest = Digest(jobspecYAML)
	r.SeriesDigest = Digest(seriesJSONL)
	final, err := r.Finalize()
	if err != nil {
		return Sealed{}, err
	}
	return SealResult(dir, final)
}

// writeFileSync writes a sidecar file and makes the bytes durable before the
// result references them.
func writeFileSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("measurement: write %s: %w", path, err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("measurement: write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("measurement: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("measurement: write %s: %w", path, err)
	}
	return syncDir(filepath.Dir(path))
}

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
