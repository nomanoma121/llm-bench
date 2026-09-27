// Package job defines the MVP job spec: the request carried by a GitHub Issue.
//
// A job spec is untrusted data. It may select from operator-owned allowlists
// (runtime images, models, output roots), but it can never name a GPU lease, a
// pause target, or a manifest path: those stay in operator configuration. The
// CLI and the controller run the same validation, so a spec that a human
// validated locally cannot be rejected differently by the controller.
package job

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind selects one of the two MVP paths.
type Kind string

const (
	// KindBenchmark measures once and opens a PR with the result.
	KindBenchmark Kind = "benchmark"
	// KindOptimize hands the sandbox to an Agent, which iterates and then
	// opens a PR with its final change and the result.
	KindOptimize Kind = "optimize"
)

// Collector names a metrics source. The value is recorded verbatim in the
// result so that the Agent can tell a harness measurement from a runtime
// self-report.
type Collector string

const (
	CollectorHarness Collector = "harness"
	CollectorRuntime Collector = "runtime"
	CollectorGPU     Collector = "nvidia"
)

// OutputRoot is the only directory tree a job spec may write results to.
const OutputRoot = "experiments"

// Limits bound untrusted values. They are generous but finite so that a
// malformed spec cannot ask for an unbounded run.
const (
	MaxCases           = 8
	MaxRepeats         = 100
	MaxMaxTokens       = 8192
	MaxConcurrency     = 8
	MaxRounds          = 1000
	DefaultRepeats     = 1
	DefaultConcurrency = 1
	// DefaultReadyTimeoutSeconds applies when the spec omits the timeout.
	DefaultReadyTimeoutSeconds = 300
)

// ErrInvalid marks a malformed or unacceptable job spec. The CLI maps it to
// exit code 2; the controller turns it into a failed Issue.
var ErrInvalid = errors.New("job: invalid spec")

// Spec is a parsed job request.
type Spec struct {
	Kind     Kind     `yaml:"kind" json:"kind"`
	Model    Model    `yaml:"model" json:"model"`
	Runtime  Runtime  `yaml:"runtime" json:"runtime"`
	Workload Workload `yaml:"workload" json:"workload"`
	Metrics  Metrics  `yaml:"metrics" json:"metrics"`
	Source   *Source  `yaml:"source,omitempty" json:"source,omitempty"`
	Budget   *Budget  `yaml:"budget,omitempty" json:"budget,omitempty"`
	Output   Output   `yaml:"output" json:"output"`
}

// Model names the model to measure. The path is resolved by the operator.
type Model struct {
	ID string `yaml:"id" json:"id"`
}

// Runtime describes how to start the benchmark target. Image and engine are
// checked against the operator allowlists.
type Runtime struct {
	Engine string   `yaml:"engine" json:"engine"`
	Image  string   `yaml:"image" json:"image"`
	Args   []string `yaml:"args,omitempty" json:"args,omitempty"`
	Ready  Ready    `yaml:"ready" json:"ready"`
}

// Ready is the readiness probe awaited before measuring.
type Ready struct {
	Port           int    `yaml:"port" json:"port"`
	Path           string `yaml:"path,omitempty" json:"path,omitempty"`
	TimeoutSeconds int    `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
}

// Timeout returns the readiness timeout, applying the default.
func (r Ready) Timeout() int {
	if r.TimeoutSeconds <= 0 {
		return DefaultReadyTimeoutSeconds
	}
	return r.TimeoutSeconds
}

// Workload is the set of prompts measured against the runtime.
type Workload struct {
	Cases       []Case    `yaml:"cases" json:"cases"`
	Concurrency int       `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
	Sampling    *Sampling `yaml:"sampling,omitempty" json:"sampling,omitempty"`
}

// Case is one prompt measured Repeats times.
type Case struct {
	Name       string `yaml:"name" json:"name"`
	Prompt     string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	PromptText string `yaml:"prompt_text,omitempty" json:"prompt_text,omitempty"`
	MaxTokens  int    `yaml:"max_tokens" json:"max_tokens"`
	Repeats    int    `yaml:"repeats,omitempty" json:"repeats,omitempty"`
}

// RepeatCount returns the number of measurements for the case.
func (c Case) RepeatCount() int {
	if c.Repeats <= 0 {
		return DefaultRepeats
	}
	return c.Repeats
}

// Sampling is the generation configuration shared by every case. Pointers
// keep "unset" distinct from an explicit zero so that the digest of two specs
// differs when only an explicit default was added.
type Sampling struct {
	Temperature *float64 `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	TopP        *float64 `yaml:"top_p,omitempty" json:"top_p,omitempty"`
	Seed        *int64   `yaml:"seed,omitempty" json:"seed,omitempty"`
}

// MetricConcurrency returns the request concurrency, applying the default.
func (w Workload) MetricConcurrency() int {
	if w.Concurrency <= 0 {
		return DefaultConcurrency
	}
	return w.Concurrency
}

// Metrics selects the collectors to run alongside the workload.
type Metrics struct {
	Collectors []Collector `yaml:"collectors" json:"collectors"`
}

// Source is the runtime source tree an optimization Agent may edit.
type Source struct {
	Repo string `yaml:"repo" json:"repo"`
	Ref  string `yaml:"ref" json:"ref"`
}

// Budget bounds an optimization run. Rounds are independent measurement runs.
type Budget struct {
	MaxRounds int `yaml:"max_rounds,omitempty" json:"max_rounds,omitempty"`
}

// Output is where the result is written inside the repository.
type Output struct {
	Dir string `yaml:"dir" json:"dir"`
}

// Constraints are the operator-owned selections a job spec may choose from.
// An empty field means "not constrained", which is the local-development
// default; the controller always passes the configured allowlists.
type Constraints struct {
	Engines     []string
	Images      []string
	Models      []string
	OutputRoots []string
	MaxRounds   int
}

// Parse decodes a job spec. Unknown fields are rejected so that a typo fails
// loudly instead of silently changing the request.
func Parse(r io.Reader) (Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("%w: parse: %w", ErrInvalid, err)
	}
	return s, nil
}

// Load reads and parses the job spec at path.
func Load(path string) (Spec, error) {
	f, err := os.Open(path)
	if err != nil {
		return Spec{}, fmt.Errorf("job: load: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// Validate checks the spec on its own, without operator allowlists.
func (s Spec) Validate() error { return s.ValidateConstraints(Constraints{}) }

// ValidateConstraints checks the spec, including the operator-owned
// allowlists. Every problem is reported, so a human can fix one Issue instead
// of rediscovering the next error per attempt.
func (s Spec) ValidateConstraints(c Constraints) error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	switch s.Kind {
	case KindBenchmark, KindOptimize:
	default:
		add("kind must be %q or %q, got %q", KindBenchmark, KindOptimize, s.Kind)
	}
	if !isSafeName(s.Model.ID) {
		add("model.id %q must be a non-empty name without path separators", s.Model.ID)
	}
	if err := checkAllowed("model.id", s.Model.ID, c.Models); err != nil {
		errs = append(errs, err)
	}
	if !isSafeName(s.Runtime.Engine) {
		add("runtime.engine %q must be a non-empty name without path separators", s.Runtime.Engine)
	}
	if err := checkAllowed("runtime.engine", s.Runtime.Engine, c.Engines); err != nil {
		errs = append(errs, err)
	}
	if strings.TrimSpace(s.Runtime.Image) == "" {
		add("runtime.image is required")
	}
	if err := checkAllowed("runtime.image", s.Runtime.Image, c.Images); err != nil {
		errs = append(errs, err)
	}
	for i, a := range s.Runtime.Args {
		if a == "" {
			add("runtime.args[%d] is empty", i)
		}
	}
	switch {
	case s.Runtime.Ready.Port <= 0 || s.Runtime.Ready.Port > 65535:
		add("runtime.ready.port must be 1..65535, got %d", s.Runtime.Ready.Port)
	}
	if p := s.Runtime.Ready.Path; p != "" && !strings.HasPrefix(p, "/") {
		add("runtime.ready.path %q must start with /", p)
	}
	if t := s.Runtime.Ready.TimeoutSeconds; t < 0 || t > 86400 {
		add("runtime.ready.timeout_seconds must be 0..86400, got %d", t)
	}
	errs = append(errs, s.Workload.validate()...)
	errs = append(errs, s.Metrics.validate()...)
	errs = append(errs, s.outputErrors(c)...)

	switch s.Kind {
	case KindOptimize:
		if s.Source == nil {
			add("source is required for kind=optimize")
		}
	case KindBenchmark:
		if s.Budget != nil {
			add("budget is only valid for kind=optimize")
		}
	}
	if s.Source != nil {
		if !isRepoRef(s.Source.Repo) {
			add("source.repo %q must look like owner/name", s.Source.Repo)
		}
		if strings.TrimSpace(s.Source.Ref) == "" {
			add("source.ref is required when source is set")
		}
	}
	if s.Budget != nil {
		if s.Budget.MaxRounds <= 0 || s.Budget.MaxRounds > MaxRounds {
			add("budget.max_rounds must be 1..%d, got %d", MaxRounds, s.Budget.MaxRounds)
		}
		if c.MaxRounds > 0 && s.Budget.MaxRounds > c.MaxRounds {
			add("budget.max_rounds %d exceeds the operator limit %d", s.Budget.MaxRounds, c.MaxRounds)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
}

func (w Workload) validate() []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	switch {
	case len(w.Cases) == 0:
		add("workload.cases must not be empty")
	case len(w.Cases) > MaxCases:
		add("workload.cases has %d entries, the limit is %d", len(w.Cases), MaxCases)
	}
	seen := map[string]bool{}
	for i, c := range w.Cases {
		if !isSafeName(c.Name) {
			add("workload.cases[%d].name %q must be a non-empty name without path separators", i, c.Name)
		}
		if seen[c.Name] {
			add("workload.cases[%d].name %q is duplicated", i, c.Name)
		}
		seen[c.Name] = true
		switch {
		case c.Prompt != "" && c.PromptText != "":
			add("workload.cases[%d] sets both prompt and prompt_text", i)
		case c.Prompt == "" && c.PromptText == "":
			add("workload.cases[%d] needs prompt or prompt_text", i)
		}
		if c.Prompt != "" && !filepath.IsLocal(c.Prompt) {
			add("workload.cases[%d].prompt %q must be a repository-relative path", i, c.Prompt)
		}
		if c.MaxTokens <= 0 || c.MaxTokens > MaxMaxTokens {
			add("workload.cases[%d].max_tokens must be 1..%d, got %d", i, MaxMaxTokens, c.MaxTokens)
		}
		if c.Repeats < 0 || c.Repeats > MaxRepeats {
			add("workload.cases[%d].repeats must be 0..%d, got %d", i, MaxRepeats, c.Repeats)
		}
	}
	if w.Concurrency < 0 || w.Concurrency > MaxConcurrency {
		add("workload.concurrency must be 0..%d, got %d", MaxConcurrency, w.Concurrency)
	}
	if s := w.Sampling; s != nil {
		if s.Temperature != nil && (*s.Temperature < 0 || *s.Temperature > 2) {
			add("workload.sampling.temperature must be 0..2, got %v", *s.Temperature)
		}
		if s.TopP != nil && (*s.TopP <= 0 || *s.TopP > 1) {
			add("workload.sampling.top_p must be in (0,1], got %v", *s.TopP)
		}
	}
	return errs
}

func (m Metrics) validate() []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if len(m.Collectors) == 0 {
		add("metrics.collectors must not be empty")
		return errs
	}
	seen := map[Collector]bool{}
	for i, c := range m.Collectors {
		switch c {
		case CollectorHarness, CollectorRuntime, CollectorGPU:
		default:
			add("metrics.collectors[%d] %q is unknown (harness, runtime, nvidia)", i, c)
		}
		if seen[c] {
			add("metrics.collectors[%d] %q is duplicated", i, c)
		}
		seen[c] = true
	}
	if !seen[CollectorHarness] {
		add("metrics.collectors must include %q: identity and validity come from the harness", CollectorHarness)
	}
	return errs
}

func (s Spec) outputErrors(c Constraints) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	dir := s.Output.Dir
	switch {
	case dir == "":
		add("output.dir is required")
		return errs
	case !filepath.IsLocal(dir):
		add("output.dir %q must be a repository-relative path", dir)
		return errs
	}
	roots := c.OutputRoots
	if len(roots) == 0 {
		roots = []string{OutputRoot}
	}
	for _, root := range roots {
		if dir == root || strings.HasPrefix(dir, root+"/") {
			return errs
		}
	}
	add("output.dir %q must be under %s", dir, strings.Join(roots, " or "))
	return errs
}

// Digest is the canonical identity of the spec: SHA-256 over its JSON form.
// The controller freezes it on the run so that the spec a result belongs to
// can be proven later, and so that a re-submitted spec with the same content
// is recognisably the same job.
func (s Spec) Digest() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("job: canonical spec: %w", err)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// checkAllowed enforces an operator allowlist. An empty list means the
// operator did not constrain the field.
func checkAllowed(field, value string, allowed []string) error {
	if len(allowed) == 0 || value == "" {
		return nil
	}
	for _, a := range allowed {
		if a == value {
			return nil
		}
	}
	return fmt.Errorf("%s %q is not allowed by the operator (allowed: %s)", field, value, strings.Join(allowed, ", "))
}

// isSafeName accepts identifiers used as directory names or labels.
func isSafeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, "/\\ \t\n") {
		return false
	}
	return true
}

// isRepoRef accepts "owner/name".
func isRepoRef(s string) bool {
	parts := strings.Split(s, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}
