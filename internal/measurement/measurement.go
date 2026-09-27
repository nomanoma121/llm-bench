// Package measurement defines the sealed measurement evidence of a run: its
// versioned schema, canonicalization, digest, size bounds and the rules that
// decide whether a measurement may be used for promotion.
//
// The harness is the authoritative writer: a driver inside the Sandbox only
// produces raw numbers, and the harness validates, normalizes and seals them
// here (docs/optimization.md §5). Runtime-reported values are allowed but are
// tagged with their source so a policy can refuse to promote on them.
package measurement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SchemaVersion is the evidence format written by this package.
const SchemaVersion = 1

// Evidence bounds. The operator ceiling may lower them; a run cannot raise
// them (docs/optimization.md §5.3).
const (
	MaxEvidenceBytes         = 16 << 20 // 16 MiB
	MaxSeriesPointsPerSeries = 4096
	MaxTotalSeriesPoints     = 65536
	// MaxMetricCount, MaxLabelsPerMetric and MaxLabelBytes bound the summary
	// section so a driver cannot exhaust the harness with metadata alone.
	MaxMetricCount     = 4096
	MaxLabelsPerMetric = 64
	MaxLabelBytes      = 256
)

// EvidenceFileName is the sealed evidence file inside the run's evidence
// directory. RawMeasurementFileName is what a driver may write before the
// harness seals it.
const (
	EvidenceFileName       = "metrics.json"
	RawMeasurementFileName = "raw-measurement.json"
	EvidenceDir            = "evidence"
)

// Source tags where a metric came from. Promotion policies allowlist sources,
// so a runtime's self-reported value can never masquerade as a driver
// measurement (docs/optimization.md §5.2).
type Source string

const (
	SourceHarness     Source = "harness"
	SourceDriver      Source = "driver"
	SourceExternalGPU Source = "external_gpu"
	SourceRuntime     Source = "runtime"
	SourceProfiler    Source = "profiler"
)

func (s Source) valid() bool {
	switch s {
	case SourceHarness, SourceDriver, SourceExternalGPU, SourceRuntime, SourceProfiler:
		return true
	}
	return false
}

// Ref identifies a frozen configuration snapshot by id and digest.
type Ref struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Metric is one scalar measurement. Labels distinguish cases of the same
// metric (for example a context depth).
type Metric struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Unit   string            `json:"unit"`
	Source Source            `json:"source"`
	Labels map[string]string `json:"labels,omitempty"`
	// Samples is the number of raw observations behind Value.
	Samples int    `json:"samples,omitempty"`
	Stats   *Stats `json:"stats,omitempty"`
}

// Stats summarizes raw observations. Promotion uses these values; the raw
// series is for graphs and diagnostics.
type Stats struct {
	Count  int     `json:"count"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	P50    float64 `json:"p50"`
	P90    float64 `json:"p90"`
	P99    float64 `json:"p99"`
	MAD    float64 `json:"mad"`
	Sum    float64 `json:"sum,omitempty"`
}

// Series is a time or depth ordered series kept for graphs. A downsample must
// be deterministic and must keep OriginalCount and SampleMethod.
type Series struct {
	Name string `json:"name"`
	// Labels distinguish series of the same name, exactly as they do for a
	// metric: with several workload cases, "ttft_ms" exists once per case and
	// the two must not share an identity.
	Labels        map[string]string `json:"labels,omitempty"`
	Unit          string            `json:"unit"`
	Source        Source            `json:"source"`
	Points        [][2]float64      `json:"points"`
	OriginalCount int               `json:"original_count,omitempty"`
	SampleMethod  string            `json:"sample_method,omitempty"`
}

// CollectorStatus records one collector's health. Gaps make a measurement
// invalid because the harness cannot prove the environment was clean.
type CollectorStatus struct {
	Name       string `json:"name"`
	IntervalMS int    `json:"interval_ms"`
	Gaps       int    `json:"gaps,omitempty"`
}

// Environment is the stable part of the machine identity. Dynamic values
// (temperature, momentary clocks, other processes) belong in the validity
// signals, never in this digest.
type Environment struct {
	Digest string `json:"digest,omitempty"`
	GPUs   []GPU  `json:"gpus,omitempty"`
	Driver string `json:"driver,omitempty"`
}

// GPU is one GPU model present during the measurement.
type GPU struct {
	Model string `json:"model"`
	Count int    `json:"count"`
}

// RuntimeRef identifies the runtime identity that was measured. It is a may-
// differ value for runtime optimization, which is why it lives here and not
// in the measurement protocol.
type RuntimeRef struct {
	SpecDigest  string `json:"spec_digest,omitempty"`
	BuildDigest string `json:"build_digest,omitempty"`
}

// Evidence is the sealed measurement record of one run.
type Evidence struct {
	SchemaVersion int         `json:"schema_version"`
	RunID         string      `json:"run_id"`
	Kind          string      `json:"kind"`
	Protocol      Ref         `json:"protocol"`
	Environment   Environment `json:"environment"`
	Runtime       RuntimeRef  `json:"runtime"`

	// MeasurementValid answers "can this measurement be trusted", never "did
	// the candidate perform well". InvalidReasons must be empty exactly when
	// it is true (docs/optimization.md §5.5).
	MeasurementValid bool     `json:"measurement_valid"`
	InvalidReasons   []string `json:"invalid_reasons,omitempty"`

	Metrics    []Metric          `json:"metrics"`
	Series     []Series          `json:"series,omitempty"`
	Collectors []CollectorStatus `json:"collectors,omitempty"`
}

// ValidationError marks evidence that must never be sealed or served.
var ValidationError = errors.New("measurement: invalid evidence")

// Validation bounds for identity fields, so a driver cannot smuggle oversized
// strings into the evidence.
const MaxIdentityLen = 256

// Validate checks the schema and the invariants a sealed evidence must hold.
// It is fail-closed: unknown sources, non-finite numbers, inconsistent
// validity flags and oversized series are all rejected.
func (e Evidence) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version %d", ValidationError, e.SchemaVersion)
	}
	if err := checkIdentity("run_id", e.RunID); err != nil {
		return err
	}
	if err := checkIdentity("kind", e.Kind); err != nil {
		return err
	}
	switch e.Kind {
	case "visual", "measurement":
	default:
		return fmt.Errorf("%w: kind %q", ValidationError, e.Kind)
	}
	if e.Protocol.ID == "" || e.Protocol.Digest == "" {
		return fmt.Errorf("%w: protocol id and digest are required", ValidationError)
	}
	if err := checkIdentity("protocol.id", e.Protocol.ID); err != nil {
		return err
	}
	if e.MeasurementValid != (len(e.InvalidReasons) == 0) {
		return fmt.Errorf("%w: measurement_valid and invalid_reasons disagree", ValidationError)
	}
	for _, r := range e.InvalidReasons {
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("%w: empty invalid reason", ValidationError)
		}
	}
	return validateMeasurements(e.Metrics, e.Series, e.Collectors, e.MeasurementValid)
}

// validateMeasurements holds the metric, series and collector rules shared by
// the frozen run evidence and the MVP job result. It is fail-closed: a valid
// document with no metrics, a duplicate identity, an oversized series or a
// collector gap next to measurement_valid=true are all rejected.
func validateMeasurements(metrics []Metric, series []Series, collectors []CollectorStatus, valid bool) error {
	if len(metrics) == 0 && valid {
		// A valid measurement with no metrics would let a policy "succeed" on
		// an empty evidence.
		return fmt.Errorf("%w: valid evidence must contain at least one metric", ValidationError)
	}
	if len(metrics) > MaxMetricCount {
		return fmt.Errorf("%w: %d metrics exceeds the limit %d", ValidationError, len(metrics), MaxMetricCount)
	}
	seenMetrics := map[string]bool{}
	for i, m := range metrics {
		if err := m.validate(); err != nil {
			return fmt.Errorf("%w: metrics[%d]: %v", ValidationError, i, err)
		}
		// A duplicate identity would be counted twice by a policy.
		if key := metricKey(m); seenMetrics[key] {
			return fmt.Errorf("%w: duplicate metric %q", ValidationError, m.Name)
		} else {
			seenMetrics[key] = true
		}
	}
	total := 0
	seenSeries := map[string]bool{}
	for i, s := range series {
		if err := s.validate(); err != nil {
			return fmt.Errorf("%w: series[%d]: %v", ValidationError, i, err)
		}
		if key := seriesKey(s); seenSeries[key] {
			return fmt.Errorf("%w: duplicate series %q", ValidationError, s.Name)
		} else {
			seenSeries[key] = true
		}
		total += len(s.Points)
	}
	if total > MaxTotalSeriesPoints {
		return fmt.Errorf("%w: %d series points exceed the limit %d", ValidationError, total, MaxTotalSeriesPoints)
	}
	for i, c := range collectors {
		if c.Name == "" {
			return fmt.Errorf("%w: collectors[%d].name is required", ValidationError, i)
		}
		if c.IntervalMS <= 0 {
			return fmt.Errorf("%w: collectors[%d].interval_ms must be positive", ValidationError, i)
		}
		if c.Gaps < 0 {
			return fmt.Errorf("%w: collectors[%d].gaps must not be negative", ValidationError, i)
		}
		// A collector that missed samples cannot certify a clean environment
		// (docs/optimization.md §5.5).
		if c.Gaps > 0 && valid {
			return fmt.Errorf("%w: collector %q reported %d gaps but the measurement is marked valid", ValidationError, c.Name, c.Gaps)
		}
	}
	return nil
}

func (m Metric) validate() error {
	if err := checkIdentity("name", m.Name); err != nil {
		return err
	}
	// An empty unit is allowed: a runtime gauge such as requests_processing
	// or kv_cache_used_cells is dimensionless, and inventing a unit for it
	// would be worse than recording none (docs/mvp.md §4.2).
	if m.Unit != "" {
		if err := checkIdentity("unit", m.Unit); err != nil {
			return err
		}
	}
	if !m.Source.valid() {
		return fmt.Errorf("source %q is not a known source", m.Source)
	}
	if !finite(m.Value) {
		return errors.New("value must be finite")
	}
	if m.Samples < 0 {
		return errors.New("samples must not be negative")
	}
	if err := checkLabels(m.Labels); err != nil {
		return err
	}
	if m.Stats != nil {
		if m.Stats.Count < 0 {
			return errors.New("stats.count must not be negative")
		}
		for _, v := range []float64{m.Stats.Mean, m.Stats.Median, m.Stats.Min, m.Stats.Max, m.Stats.P50, m.Stats.P90, m.Stats.P99, m.Stats.MAD, m.Stats.Sum} {
			if !finite(v) {
				return errors.New("stats must be finite")
			}
		}
	}
	return nil
}

func (s Series) validate() error {
	if err := checkIdentity("name", s.Name); err != nil {
		return err
	}
	if !s.Source.valid() {
		return fmt.Errorf("source %q is not a known source", s.Source)
	}
	if err := checkLabels(s.Labels); err != nil {
		return err
	}
	if len(s.Points) > MaxSeriesPointsPerSeries {
		return fmt.Errorf("%d points exceed the per-series limit %d", len(s.Points), MaxSeriesPointsPerSeries)
	}
	if s.OriginalCount < len(s.Points) {
		return fmt.Errorf("original_count %d is smaller than the stored %d points", s.OriginalCount, len(s.Points))
	}
	if s.OriginalCount > len(s.Points) && s.SampleMethod == "" {
		return errors.New("a downsampled series must record sample_method")
	}
	for _, p := range s.Points {
		if !finite(p[0]) || !finite(p[1]) {
			return errors.New("points must be finite")
		}
	}
	return nil
}

// checkLabels applies the label bounds shared by metrics and series.
func checkLabels(labels map[string]string) error {
	if len(labels) > MaxLabelsPerMetric {
		return fmt.Errorf("%d labels exceed the limit %d", len(labels), MaxLabelsPerMetric)
	}
	for k, v := range labels {
		if k == "" || len(k) > MaxLabelBytes || len(v) > MaxLabelBytes {
			return fmt.Errorf("label %q is empty or too long", k)
		}
	}
	return nil
}

func checkIdentity(field, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%w: %s is required", ValidationError, field)
	}
	if len(v) > MaxIdentityLen {
		return fmt.Errorf("%w: %s is too long", ValidationError, field)
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Canonicalize renders the evidence deterministically. Go's encoder sorts map
// keys, so repeated calls produce identical bytes for the same value; the
// digest therefore identifies the measurement, not the writer.
func Canonicalize(e Evidence) ([]byte, error) {
	return canonicalize(e, MaxEvidenceBytes)
}

// canonicalize applies the byte guard with an explicit limit so the guard
// itself is testable independently of the section caps.
func canonicalize(e Evidence, limit int) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	normalized := e.normalized()
	b, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("measurement: canonicalize: %w", err)
	}
	if len(b) > limit {
		return nil, fmt.Errorf("%w: %d bytes exceed the limit %d", ValidationError, len(b), limit)
	}
	return b, nil
}

// normalized returns a copy with sections ordered and slices non-nil so the
// canonical form is stable regardless of how the caller built it.
//
// Metrics are identified by name *and* labels (the schema distinguishes
// decode_step_ms at different depths), so the sort key must include every
// field that makes two metrics distinct; sorting by name alone would leave the
// byte order, and therefore the digest, dependent on the input order.
func (e Evidence) normalized() Evidence {
	out := e
	out.InvalidReasons = append([]string(nil), e.InvalidReasons...)
	sort.Strings(out.InvalidReasons)
	out.Metrics = append([]Metric(nil), e.Metrics...)
	sort.Slice(out.Metrics, func(i, j int) bool { return metricKey(out.Metrics[i]) < metricKey(out.Metrics[j]) })
	out.Series = append([]Series(nil), e.Series...)
	sort.Slice(out.Series, func(i, j int) bool { return seriesKey(out.Series[i]) < seriesKey(out.Series[j]) })
	out.Collectors = append([]CollectorStatus(nil), e.Collectors...)
	sort.Slice(out.Collectors, func(i, j int) bool { return out.Collectors[i].Name < out.Collectors[j].Name })
	return out
}

// metricKey is the total order key of a metric: name, source, unit and the
// canonical label rendering.
func metricKey(m Metric) string {
	return m.Name + "\x00" + string(m.Source) + "\x00" + m.Unit + "\x00" + labelKey(m.Labels)
}

// seriesKey is the total order key of a series. Labels are part of it for the
// same reason they are part of a metric's key: two cases of one workload
// produce two series with the same name, and treating them as one identity
// would hide one of them.
func seriesKey(s Series) string {
	return s.Name + "\x00" + string(s.Source) + "\x00" + s.Unit + "\x00" + labelKey(s.Labels)
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

// Digest is the sha256 of the canonical bytes.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Sealed is one sealed evidence file.
type Sealed struct {
	Path   string
	Digest string
	Bytes  int
}

// Seal validates the evidence, writes it atomically and returns its digest.
// Durability order: temp file, fsync, rename, parent fsync. The digest is only
// meaningful once the rename is durable, so callers must not record it before
// Seal returns.
func Seal(dir string, e Evidence) (Sealed, error) {
	b, err := Canonicalize(e)
	if err != nil {
		return Sealed{}, err
	}
	return atomicSeal(dir, EvidenceFileName, ".metrics-", b)
}

// atomicSeal is the shared write-ahead write: temp file, fsync, rename,
// parent fsync. Both the frozen run evidence and the MVP job result use it so
// the durability argument only has to hold once.
func atomicSeal(dir, name, tmpPrefix string, b []byte) (Sealed, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Sealed{}, fmt.Errorf("measurement: seal: %w", err)
	}
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*.tmp")
	if err != nil {
		return Sealed{}, fmt.Errorf("measurement: seal: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return Sealed{}, fmt.Errorf("measurement: seal: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Sealed{}, fmt.Errorf("measurement: seal: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Sealed{}, fmt.Errorf("measurement: seal: %w", err)
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(tmpName, final); err != nil {
		return Sealed{}, fmt.Errorf("measurement: seal: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return Sealed{}, fmt.Errorf("measurement: seal: %w", err)
	}
	return Sealed{Path: final, Digest: Digest(b), Bytes: len(b)}, nil
}

// Load reads and validates a sealed evidence file and returns it with the
// digest of the bytes on disk.
func Load(path string) (Evidence, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Evidence{}, "", err
	}
	var e Evidence
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return Evidence{}, "", fmt.Errorf("measurement: decode %s: %w", path, err)
	}
	if err := e.Validate(); err != nil {
		return Evidence{}, "", err
	}
	return e, Digest(b), nil
}

// Verify loads a sealed evidence and checks it against a recorded digest. A
// mismatch is corruption, not an inconclusive measurement, so it is an error.
func Verify(path, want string) (Evidence, error) {
	e, got, err := Load(path)
	if err != nil {
		return Evidence{}, err
	}
	if got != want {
		return Evidence{}, fmt.Errorf("measurement: %s digest %s does not match the recorded %s", path, got, want)
	}
	return e, nil
}

// WithHarnessMetric adds or replaces a harness-measured metric. Harness timing
// always exists, which is what makes a minimal measurement run carry valid
// evidence even before external collectors exist.
func WithHarnessMetric(e Evidence, name string, value float64, unit string, labels map[string]string) Evidence {
	if labels == nil {
		labels = map[string]string{}
	}
	metric := Metric{Name: name, Value: value, Unit: unit, Source: SourceHarness, Labels: labels, Samples: 1}
	for i, m := range e.Metrics {
		if m.Name == name && sameLabels(m.Labels, labels) {
			e.Metrics[i] = metric
			return e
		}
	}
	e.Metrics = append(e.Metrics, metric)
	return e
}

func sameLabels(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Raw is what a driver or collector inside the Sandbox may write before the
// harness seals it. It deliberately carries no identity: protocol, run id,
// environment and validity are supplied by the harness, so the untrusted side
// cannot forge them.
type Raw struct {
	Metrics []RawMetric `json:"metrics"`
	Series  []Series    `json:"series,omitempty"`
}

// RawMetric is one measurement produced by a trusted driver.
type RawMetric struct {
	Name    string            `json:"name"`
	Value   float64           `json:"value"`
	Unit    string            `json:"unit"`
	Labels  map[string]string `json:"labels,omitempty"`
	Samples int               `json:"samples,omitempty"`
	Stats   *Stats            `json:"stats,omitempty"`
}

// ParseRaw decodes and bounds raw measurement input. The caller must have
// limited the byte size already (a sandbox transfer is bounded by
// PullLimited); this function rejects anything structurally unusable.
func ParseRaw(b []byte) (Raw, error) {
	if len(b) > MaxEvidenceBytes {
		return Raw{}, fmt.Errorf("%w: raw measurement is too large (%d bytes)", ValidationError, len(b))
	}
	var r Raw
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Raw{}, fmt.Errorf("%w: decode raw measurement: %v", ValidationError, err)
	}
	if len(r.Metrics) == 0 {
		return Raw{}, fmt.Errorf("%w: raw measurement has no metrics", ValidationError)
	}
	if len(r.Metrics) > MaxMetricCount {
		return Raw{}, fmt.Errorf("%w: raw measurement has too many metrics", ValidationError)
	}
	return r, nil
}

// AppendRaw converts raw metrics into evidence metrics tagged with the given
// source. The values are validated as part of the evidence, so a driver
// cannot smuggle a NaN or an unknown label shape into the sealed file.
func (e Evidence) AppendRaw(r Raw, source Source) Evidence {
	for _, m := range r.Metrics {
		e.Metrics = append(e.Metrics, Metric{
			Name: m.Name, Value: m.Value, Unit: m.Unit, Source: source,
			Labels: m.Labels, Samples: m.Samples, Stats: m.Stats,
		})
	}
	e.Series = append(e.Series, r.Series...)
	return e
}

// EnforceRequiredSources fails a measurement closed when a source the protocol
// requires is absent. The harness calls it before sealing, because "evidence
// exists" must not be mistaken for "the measurement the protocol asked for
// exists" (docs/optimization.md §5.5).
func EnforceRequiredSources(e Evidence, required []string) Evidence {
	if len(required) == 0 {
		return e
	}
	present := map[Source]bool{}
	for _, m := range e.Metrics {
		present[m.Source] = true
	}
	for _, s := range e.Series {
		present[s.Source] = true
	}
	var missing []string
	for _, want := range required {
		if !present[Source(want)] {
			missing = append(missing, want)
		}
	}
	if len(missing) == 0 {
		return e
	}
	sort.Strings(missing)
	e.MeasurementValid = false
	e.InvalidReasons = append(e.InvalidReasons, "missing required metric sources: "+strings.Join(missing, ", "))
	return e
}

// EnforceCollectorGaps marks a measurement invalid when any collector missed
// samples.
func EnforceCollectorGaps(e Evidence) Evidence {
	for _, c := range e.Collectors {
		if c.Gaps > 0 {
			e.MeasurementValid = false
			e.InvalidReasons = append(e.InvalidReasons, fmt.Sprintf("collector %s reported %d gaps", c.Name, c.Gaps))
		}
	}
	return e
}
