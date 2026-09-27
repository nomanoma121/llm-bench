package operator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// This file holds the measurement and optimization configuration added in
// design v1.7 (docs/optimization.md §6). All of it is operator-owned: an
// experiment recipe cannot name a protocol, a policy or a sampling plan, and
// the controller never reads promotion thresholds from a run.
//
// Snapshots (the JSON below) and their digests are both persisted, so a run's
// measurement conditions stay reconstructible after the operator edits the
// configuration.

// KindVisual and KindMeasurement mirror run.RunKind without importing the run
// package (run imports operator, so the reverse would be an import cycle).
const (
	KindVisual      = "visual"
	KindMeasurement = "measurement"
)

// ExecutionSpec describes how a trusted measurement component is executed and
// how its output reaches the harness. Candidate runtime code must not be able
// to replace either the component or its output channel (docs/optimization.md
// §5.4).
type ExecutionSpec struct {
	// ContentDigest is the digest of the component's file tree. A version
	// string is not enough: it would let a swapped binary keep its identity.
	ContentDigest string `yaml:"content_digest" json:"content_digest"`
	// ExecMode: "sandbox-exec" | "separate-container".
	ExecMode string `yaml:"exec_mode" json:"exec_mode"`
	// OutputMode: "stdout-transport" (recommended) | "private-dir".
	OutputMode string `yaml:"output_mode" json:"output_mode"`
}

// WorkloadCase is one element of the measurement workload matrix. The matrix
// digest covers the whole executed matrix; selecting a subset is a future
// extension and deliberately not part of SubmitOptions.
type WorkloadCase struct {
	Name         string `yaml:"name" json:"name"`
	ContextDepth int    `yaml:"context_depth" json:"context_depth"`
	PromptTokens int    `yaml:"prompt_tokens" json:"prompt_tokens"`
	DecodeSteps  int    `yaml:"decode_steps" json:"decode_steps"`
	Prefill      bool   `yaml:"prefill,omitempty" json:"prefill,omitempty"`
}

// MeasurementProtocol describes how one run is measured. Multi-run arrangement
// lives in SamplingPolicy: one attempt is one run.
type MeasurementProtocol struct {
	SchemaVersion int                 `yaml:"schema_version" json:"schema_version"`
	Driver        ExecutionSpec       `yaml:"driver" json:"driver"`
	DriverArgv    []string            `yaml:"driver_argv" json:"driver_argv"`
	Workload      MeasurementWorkload `yaml:"workload" json:"workload"`
	Warmup        MeasurementWarmup   `yaml:"warmup,omitempty" json:"warmup,omitempty"`
	// KVFill: "none" | "to:<tokens>".
	KVFill string `yaml:"kv_fill,omitempty" json:"kv_fill,omitempty"`
	// WithinRunSamples repeats the measurement inside a single run.
	WithinRunSamples int `yaml:"within_run_samples,omitempty" json:"within_run_samples,omitempty"`
	// RequiredSources lists metric sources that must be present for the
	// evidence to count (for example "driver", "external_gpu").
	RequiredSources []string               `yaml:"required_sources" json:"required_sources"`
	Collectors      []MeasurementCollector `yaml:"collectors,omitempty" json:"collectors,omitempty"`
	// Validity holds the measurement_valid rules as an allowlisted set of
	// names; the implementation owns their meaning.
	Validity MeasurementValidity `yaml:"validity,omitempty" json:"validity,omitempty"`
}

// MeasurementWorkload is the workload matrix of one protocol.
type MeasurementWorkload struct {
	Matrix []WorkloadCase `yaml:"matrix" json:"matrix"`
}

// MeasurementWarmup describes the discarded warmup before sampling.
type MeasurementWarmup struct {
	Steps          int `yaml:"steps,omitempty" json:"steps,omitempty"`
	DiscardSeconds int `yaml:"discard_seconds,omitempty" json:"discard_seconds,omitempty"`
}

// MeasurementCollector configures one external metric collector.
type MeasurementCollector struct {
	Name       string        `yaml:"name" json:"name"`
	IntervalMS int           `yaml:"interval_ms" json:"interval_ms"`
	Spec       ExecutionSpec `yaml:"spec" json:"spec"`
}

// MeasurementValidity names the validity rules applied to the evidence.
type MeasurementValidity struct {
	Rules []string `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// PromotionPolicy decides acceptance from sealed evidence: objective,
// thresholds and guards. It never contains measurement instructions.
type PromotionPolicy struct {
	SchemaVersion int    `yaml:"schema_version" json:"schema_version"`
	PrimaryMetric string `yaml:"primary_metric" json:"primary_metric"`
	// Direction: "min" | "max".
	Direction string  `yaml:"direction" json:"direction"`
	AbsFloor  float64 `yaml:"abs_floor,omitempty" json:"abs_floor,omitempty"`
	RelFloor  float64 `yaml:"rel_floor,omitempty" json:"rel_floor,omitempty"`
	// NoiseMultiple scales a paired-MAD estimate; diagnostic until enough
	// history exists, so the MVP relies on the floors above.
	NoiseMultiple float64         `yaml:"noise_multiple,omitempty" json:"noise_multiple,omitempty"`
	Guards        PromotionGuards `yaml:"guards,omitempty" json:"guards,omitempty"`
	// AllowedSources restricts which metric sources may drive promotion
	// (for example "harness", "driver", "external_gpu").
	AllowedSources []string `yaml:"allowed_sources" json:"allowed_sources"`
	// GrayZone.Action is "inconclusive" in the MVP. Adaptive sampling, which
	// would add "needs-more-samples", is a later addition.
	GrayZone PromotionGrayZone `yaml:"gray_zone,omitempty" json:"gray_zone,omitempty"`
}

// PromotionGuards are the non-negotiable bounds checked before the objective.
type PromotionGuards struct {
	PrefillRegressionLimit float64  `yaml:"prefill_regression_limit,omitempty" json:"prefill_regression_limit,omitempty"`
	MinVRAMHeadroomMiB     int      `yaml:"min_vram_headroom_mib,omitempty" json:"min_vram_headroom_mib,omitempty"`
	CorrectnessPredicates  []string `yaml:"correctness_predicates,omitempty" json:"correctness_predicates,omitempty"`
}

// PromotionGrayZone describes what to do when the measurement cannot decide.
type PromotionGrayZone struct {
	Action string `yaml:"action,omitempty" json:"action,omitempty"`
}

// SamplingPolicy arranges multiple runs (pairs, order, seed). The repetition
// count lives here and nowhere else.
type SamplingPolicy struct {
	SchemaVersion int `yaml:"schema_version" json:"schema_version"`
	// InitialPairs is the number of A/B pairs in the MVP (3 pairs = 6 runs).
	InitialPairs int `yaml:"initial_pairs" json:"initial_pairs"`
	// OrderRule: "balanced-randomized-pairs".
	OrderRule string `yaml:"order_rule" json:"order_rule"`
	// MaxPairs equals InitialPairs until adaptive sampling exists.
	MaxPairs int `yaml:"max_pairs" json:"max_pairs"`
	// SeedPolicy: "per-session-random" | "fixed:<n>".
	SeedPolicy string `yaml:"seed_policy,omitempty" json:"seed_policy,omitempty"`
}

// OptimizationProfile binds the kind, protocol, policy and sampling a session
// uses. The caller cannot override what a profile fixes.
type OptimizationProfile struct {
	Kind     string `yaml:"kind" json:"kind"`
	Protocol string `yaml:"protocol" json:"protocol"`
	Policy   string `yaml:"policy" json:"policy"`
	Sampling string `yaml:"sampling" json:"sampling"`
	// Budgets for the session (docs/optimization.md §8). Best effort until
	// the session implementation lands; validated here so misconfiguration
	// fails at load time.
	MaxRounds       int `yaml:"max_rounds,omitempty" json:"max_rounds,omitempty"`
	MaxRuns         int `yaml:"max_runs,omitempty" json:"max_runs,omitempty"`
	MaxInfraRetries int `yaml:"max_infra_retries,omitempty" json:"max_infra_retries,omitempty"`
}

// Submission describes the measurement identity frozen at submit time.
type Submission struct {
	Kind string `json:"kind"` // operator.KindVisual | operator.KindMeasurement

	ProtocolID     string `json:"protocol_id,omitempty"`
	ProtocolJSON   string `json:"protocol_json,omitempty"`
	ProtocolDigest string `json:"protocol_digest,omitempty"`

	PolicyID     string `json:"policy_id,omitempty"`
	PolicyJSON   string `json:"policy_json,omitempty"`
	PolicyDigest string `json:"policy_digest,omitempty"`

	SamplingID     string `json:"sampling_id,omitempty"`
	SamplingJSON   string `json:"sampling_json,omitempty"`
	SamplingDigest string `json:"sampling_digest,omitempty"`
}

// ResolveSubmission validates a submission request against the operator
// allowlists and freezes the protocol snapshot. kind may be empty (visual),
// protocol may be empty for visual runs, and profile (when given) fixes both.
func (c Config) ResolveSubmission(target, profile, kind, protocol string) (Submission, error) {
	t, ok := c.Targets[target]
	if !ok {
		return Submission{}, fmt.Errorf("operator: target %q is not allowlisted", target)
	}
	sub := Submission{Kind: KindVisual}
	if profile != "" {
		p, ok := c.OptimizationProfiles[profile]
		if !ok {
			return Submission{}, fmt.Errorf("operator: optimization profile %q is not configured", profile)
		}
		// A profile fixes the measurement identity: the request cannot
		// override it.
		kind, protocol = p.Kind, p.Protocol
		// Policy and sampling are part of the frozen identity whenever a
		// profile is used, even for visual runs that only carry a protocol.
		if p.Policy != "" {
			if err := sub.freezePolicy(c, p.Policy); err != nil {
				return Submission{}, err
			}
		}
		if p.Sampling != "" {
			if err := sub.freezeSampling(c, p.Sampling); err != nil {
				return Submission{}, err
			}
		}
	}
	switch kind {
	case "", KindVisual:
		sub.Kind = KindVisual
	case KindMeasurement:
		sub.Kind = KindMeasurement
	default:
		return Submission{}, fmt.Errorf("operator: kind %q must be %q or %q", kind, KindVisual, KindMeasurement)
	}
	if sub.Kind == KindMeasurement && protocol == "" {
		return Submission{}, errors.New("operator: kind measurement requires a measurement protocol")
	}
	if protocol != "" {
		if err := sub.freezeProtocol(c, t, protocol); err != nil {
			return Submission{}, err
		}
	}
	return sub, nil
}

// freezeProtocol snapshots a protocol, enforcing the per-target allowlist.
func (s *Submission) freezeProtocol(c Config, t Target, name string) error {
	p, ok := c.MeasurementProtocols[name]
	if !ok {
		return fmt.Errorf("operator: measurement protocol %q is not configured", name)
	}
	allowed := false
	for _, a := range t.MeasurementProtocols {
		if a == name {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("operator: target allowlist does not include measurement protocol %q", name)
	}
	raw, digest, err := canonicalSnapshot(p)
	if err != nil {
		return err
	}
	s.ProtocolID, s.ProtocolJSON, s.ProtocolDigest = name, raw, digest
	return nil
}

func (s *Submission) freezePolicy(c Config, name string) error {
	p, ok := c.PromotionPolicies[name]
	if !ok {
		return fmt.Errorf("operator: promotion policy %q is not configured", name)
	}
	raw, digest, err := canonicalSnapshot(p)
	if err != nil {
		return err
	}
	s.PolicyID, s.PolicyJSON, s.PolicyDigest = name, raw, digest
	return nil
}

func (s *Submission) freezeSampling(c Config, name string) error {
	p, ok := c.SamplingPolicies[name]
	if !ok {
		return fmt.Errorf("operator: sampling policy %q is not configured", name)
	}
	raw, digest, err := canonicalSnapshot(p)
	if err != nil {
		return err
	}
	s.SamplingID, s.SamplingJSON, s.SamplingDigest = name, raw, digest
	return nil
}

// canonicalSnapshot serializes a configuration value deterministically and
// returns both the snapshot and its digest. Digesting the whole snapshot is
// deliberate: enumerating fields in a digest function lets new fields be
// added without entering the identity.
func canonicalSnapshot(v any) (string, string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", "", fmt.Errorf("operator: canonical snapshot: %w", err)
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

// WorkloadDigest is the canonical digest of the workload matrix a run
// executes. The MVP runs the whole matrix, so no selector exists.
func WorkloadDigest(cases []WorkloadCase) (string, error) {
	_, digest, err := canonicalSnapshot(cases)
	return digest, err
}

// validateMeasurementConfig checks the v1.7 configuration and the MVP
// constraints. It fails closed: a profile that references a missing protocol,
// policy or sampling policy is a configuration error, not a runtime surprise.
func (c Config) validateMeasurementConfig() error {
	for name, p := range c.MeasurementProtocols {
		if err := validateProtocol(p); err != nil {
			return fmt.Errorf("operator: measurement protocol %q: %w", name, err)
		}
	}
	for name, p := range c.PromotionPolicies {
		if err := validatePromotionPolicy(p); err != nil {
			return fmt.Errorf("operator: promotion policy %q: %w", name, err)
		}
	}
	for name, p := range c.SamplingPolicies {
		if err := validateSamplingPolicy(p); err != nil {
			return fmt.Errorf("operator: sampling policy %q: %w", name, err)
		}
	}
	for name, p := range c.OptimizationProfiles {
		if p.Kind != KindVisual && p.Kind != KindMeasurement {
			return fmt.Errorf("operator: optimization profile %q: kind %q must be %q or %q", name, p.Kind, KindVisual, KindMeasurement)
		}
		if _, ok := c.MeasurementProtocols[p.Protocol]; !ok {
			return fmt.Errorf("operator: optimization profile %q references unknown protocol %q", name, p.Protocol)
		}
		if _, ok := c.PromotionPolicies[p.Policy]; !ok {
			return fmt.Errorf("operator: optimization profile %q references unknown promotion policy %q", name, p.Policy)
		}
		if _, ok := c.SamplingPolicies[p.Sampling]; !ok {
			return fmt.Errorf("operator: optimization profile %q references unknown sampling policy %q", name, p.Sampling)
		}
	}
	for id, t := range c.Targets {
		for _, p := range t.MeasurementProtocols {
			if _, ok := c.MeasurementProtocols[p]; !ok {
				return fmt.Errorf("operator: target %q allowlists unknown measurement protocol %q", id, p)
			}
		}
	}
	return nil
}

func validateProtocol(p MeasurementProtocol) error {
	if p.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1")
	}
	if p.Driver.ContentDigest == "" {
		return errors.New("driver.content_digest is required")
	}
	if p.Driver.ExecMode != "sandbox-exec" && p.Driver.ExecMode != "separate-container" {
		return fmt.Errorf("driver.exec_mode %q must be sandbox-exec or separate-container", p.Driver.ExecMode)
	}
	if p.Driver.OutputMode != "stdout-transport" && p.Driver.OutputMode != "private-dir" {
		return fmt.Errorf("driver.output_mode %q must be stdout-transport or private-dir", p.Driver.OutputMode)
	}
	if p.Driver.OutputMode == "private-dir" {
		// A file channel is only trustworthy once the image provides a
		// candidate-unwritable directory; until then the harness captures the
		// driver's stdout over the transport (docs/optimization.md §5.4).
		return errors.New(`driver.output_mode "private-dir" is not supported yet: only stdout-transport is safe without a candidate-unwritable image path`)
	}
	if len(p.DriverArgv) == 0 {
		return errors.New("driver_argv is required")
	}
	if len(p.Workload.Matrix) == 0 {
		return errors.New("workload.matrix must not be empty")
	}
	for i, w := range p.Workload.Matrix {
		if w.Name == "" {
			return fmt.Errorf("workload.matrix[%d].name is required", i)
		}
		if w.ContextDepth < 0 || w.PromptTokens < 0 || w.DecodeSteps < 0 {
			return fmt.Errorf("workload.matrix[%d] has negative values", i)
		}
	}
	for i, col := range p.Collectors {
		if col.Name == "" {
			return fmt.Errorf("collectors[%d].name is required", i)
		}
		if col.IntervalMS <= 0 {
			return fmt.Errorf("collectors[%d].interval_ms must be positive", i)
		}
		if col.Spec.ContentDigest == "" {
			return fmt.Errorf("collectors[%d].spec.content_digest is required", i)
		}
	}
	if len(p.RequiredSources) == 0 {
		return errors.New("required_sources must not be empty")
	}
	for i, s := range p.RequiredSources {
		if !validMetricSource(s) {
			return fmt.Errorf("required_sources[%d] %q is not a known source", i, s)
		}
	}
	return nil
}

func validatePromotionPolicy(p PromotionPolicy) error {
	if p.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1")
	}
	if p.PrimaryMetric == "" {
		return errors.New("primary_metric is required")
	}
	if p.Direction != "min" && p.Direction != "max" {
		return fmt.Errorf("direction %q must be min or max", p.Direction)
	}
	if p.AbsFloor < 0 || p.RelFloor < 0 {
		return errors.New("floors must not be negative")
	}
	if p.Guards.MinVRAMHeadroomMiB < 0 {
		return errors.New("guards.min_vram_headroom_mib must not be negative")
	}
	if len(p.AllowedSources) == 0 {
		return errors.New("allowed_sources must not be empty")
	}
	for i, s := range p.AllowedSources {
		if !validMetricSource(s) {
			return fmt.Errorf("allowed_sources[%d] %q is not a known source", i, s)
		}
	}
	// The MVP has no adaptive sampling, so the gray zone is always
	// inconclusive and the value is required: an unset action would leave the
	// behaviour of an undecided measurement implicit (docs/optimization.md
	// §6.1).
	if p.GrayZone.Action != "inconclusive" {
		return fmt.Errorf("gray_zone.action %q is not supported in the MVP (must be \"inconclusive\")", p.GrayZone.Action)
	}
	return nil
}

func validateSamplingPolicy(p SamplingPolicy) error {
	if p.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1")
	}
	if p.InitialPairs <= 0 {
		return errors.New("initial_pairs must be positive")
	}
	if p.MaxPairs != p.InitialPairs {
		// Adaptive sampling is not implemented; a larger cap would silently
		// change how many runs a decision consumes.
		return fmt.Errorf("max_pairs (%d) must equal initial_pairs (%d) until adaptive sampling exists", p.MaxPairs, p.InitialPairs)
	}
	switch {
	case p.OrderRule == "":
		return errors.New("order_rule is required")
	case p.OrderRule != "balanced-randomized-pairs":
		return fmt.Errorf("order_rule %q is not supported", p.OrderRule)
	}
	if p.SeedPolicy != "" && !strings.HasPrefix(p.SeedPolicy, "fixed:") && p.SeedPolicy != "per-session-random" {
		return fmt.Errorf("seed_policy %q must be per-session-random or fixed:<n>", p.SeedPolicy)
	}
	return nil
}

// validMetricSource is the allowlist of metric provenance tags. A metric
// without a known source cannot be promoted (docs/optimization.md §5.2).
func validMetricSource(s string) bool {
	switch s {
	case "harness", "driver", "external_gpu", "runtime", "profiler":
		return true
	}
	return false
}
