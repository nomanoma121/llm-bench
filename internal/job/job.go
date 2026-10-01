package job

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Kind string

const (
	Benchmark Kind = "benchmark"
	Optimize  Kind = "optimize"
)

var ErrInvalid = errors.New("invalid job spec")

type Spec struct {
	Kind     Kind      `yaml:"kind" json:"kind"`
	Model    string    `yaml:"model" json:"model"`
	Runtime  Runtime   `yaml:"runtime" json:"runtime"`
	Workload []Case    `yaml:"workload" json:"workload"`
	Sampling *Sampling `yaml:"sampling,omitempty" json:"sampling,omitempty"`
	Metrics  []string  `yaml:"metrics,omitempty" json:"metrics,omitempty"`
	Source   *Source   `yaml:"source,omitempty" json:"source,omitempty"`
	Budget   *Budget   `yaml:"budget,omitempty" json:"budget,omitempty"`
}

type Runtime struct {
	Engine              string   `yaml:"engine" json:"engine"`
	Args                []string `yaml:"args,omitempty" json:"args,omitempty"`
	Port                int      `yaml:"port,omitempty" json:"port,omitempty"`
	ReadyTimeoutSeconds int      `yaml:"ready_timeout_seconds,omitempty" json:"ready_timeout_seconds,omitempty"`
}

type Case struct {
	Name       string `yaml:"name" json:"name"`
	Prompt     string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	PromptText string `yaml:"prompt_text,omitempty" json:"prompt_text,omitempty"`
	MaxTokens  int    `yaml:"max_tokens" json:"max_tokens"`
	Repeats    int    `yaml:"repeats,omitempty" json:"repeats,omitempty"`
}

type Sampling struct {
	Temperature *float64 `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	TopP        *float64 `yaml:"top_p,omitempty" json:"top_p,omitempty"`
	Seed        *int64   `yaml:"seed,omitempty" json:"seed,omitempty"`
}

type Source struct {
	Repo string `yaml:"repo" json:"repo"`
	Ref  string `yaml:"ref" json:"ref"`
}

type Budget struct {
	MaxRounds int `yaml:"max_rounds" json:"max_rounds"`
}

const (
	MetricRuntime = "runtime"
	MetricGPU     = "gpu"
	MetricHost    = "host"
)

func (c Case) RepeatCount() int {
	if c.Repeats <= 0 {
		return 1
	}
	return c.Repeats
}

func (r Runtime) ListenPort() int {
	if r.Port == 0 {
		return 18080
	}
	return r.Port
}

func (r Runtime) ReadyTimeoutSecondsOrDefault() int {
	if r.ReadyTimeoutSeconds == 0 {
		return 600
	}
	return r.ReadyTimeoutSeconds
}

// shardSuffix is the "-00001-of-00004" a split GGUF's first file carries.
var shardSuffix = regexp.MustCompile(`-\d{5}-of-\d{5}$`)

func (s Spec) ModelName() string {
	base := filepath.Base(s.Model)
	return shardSuffix.ReplaceAllString(strings.TrimSuffix(base, filepath.Ext(base)), "")
}

func (s Spec) Wants(metric string) bool {
	for _, m := range s.Metrics {
		if m == metric {
			return true
		}
	}
	return false
}

func Parse(r io.Reader) (Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return s, s.Validate()
}

func Load(path string) (Spec, error) {
	f, err := os.Open(path)
	if err != nil {
		return Spec{}, err
	}
	defer f.Close()
	return Parse(f)
}

func FromIssueBody(body string) (Spec, error) {
	block, ok := yamlBlock(body)
	if !ok {
		return Spec{}, fmt.Errorf("%w: no ```yaml block in the issue body", ErrInvalid)
	}
	return Parse(strings.NewReader(block))
}

func (s Spec) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if s.Kind != Benchmark && s.Kind != Optimize {
		add("kind must be benchmark or optimize, got %q", s.Kind)
	}
	if s.Model == "" || !filepath.IsLocal(s.Model) {
		add("model %q must be a path under the models directory", s.Model)
	}
	if s.Runtime.Engine == "" {
		add("runtime.engine is required")
	}
	if len(s.Workload) == 0 {
		add("workload must have at least one case")
	}
	seen := map[string]bool{}
	for i, c := range s.Workload {
		if !isName(c.Name) || seen[c.Name] {
			add("workload[%d].name %q must be a unique plain name", i, c.Name)
		}
		seen[c.Name] = true
		if (c.Prompt == "") == (c.PromptText == "") {
			add("workload[%d] needs exactly one of prompt or prompt_text", i)
		}
		if c.Prompt != "" && !filepath.IsLocal(c.Prompt) {
			add("workload[%d].prompt %q must be a repository-relative path", i, c.Prompt)
		}
		if c.MaxTokens <= 0 {
			add("workload[%d].max_tokens must be positive", i)
		}
	}
	for _, m := range s.Metrics {
		if m != MetricRuntime && m != MetricGPU && m != MetricHost {
			add("metrics: unknown %q (runtime, gpu, host)", m)
		}
	}
	if s.Kind == Optimize && (s.Source == nil || s.Source.Repo == "") {
		add("source.repo is required for optimize")
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
	}
	return nil
}

func isName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\ \t\n")
}

func yamlBlock(body string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lang := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "```"))
		if !strings.HasPrefix(strings.TrimSpace(line), "```") || (lang != "yaml" && lang != "yml") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				return strings.Join(lines[i+1:j], "\n"), true
			}
		}
		return "", false
	}
	return "", false
}

func Template(kind Kind) string {
	s := `kind: ` + string(kind) + `
model: Qwen3.8-27B/Qwen3.8-27B-Q4_0.gguf
runtime:
  engine: llamacpp
  args: ["--ctx-size", "8192"]
workload:
  - name: short
    prompt: benchmarks/visual/prompt.md
    max_tokens: 256
    repeats: 3
sampling:
  temperature: 0
  seed: 1
metrics: [runtime, gpu, host]
`
	if kind == Optimize {
		s += `source:
  repo: ggml-org/llama.cpp
  ref: master
budget:
  max_rounds: 20
`
	}
	return s
}
