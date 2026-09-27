package job

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func validSpec(kind Kind) Spec {
	return Spec{
		Kind:  kind,
		Model: Model{ID: "qwen38-27b"},
		Runtime: Runtime{
			Engine: "llamacpp",
			Image:  "ghcr.io/example/llama.cpp@sha256:abc",
			Args:   []string{"-ngl", "99"},
			Ready:  Ready{Port: 8080, Path: "/health"},
		},
		Workload: Workload{
			Cases: []Case{{Name: "short", Prompt: "prompts/short.txt", MaxTokens: 256, Repeats: 3}},
		},
		Metrics: Metrics{Collectors: []Collector{CollectorHarness, CollectorRuntime, CollectorGPU}},
		Output:  Output{Dir: "experiments/qwen38-27b"},
	}
}

// mutateSpec applies a mutation to a deep copy, so a table entry cannot leak
// into the next one through the shared slices.
func mutateSpec(base Spec, f func(*Spec)) Spec {
	out := base
	out.Runtime.Args = append([]string(nil), base.Runtime.Args...)
	out.Workload.Cases = append([]Case(nil), base.Workload.Cases...)
	out.Metrics.Collectors = append([]Collector(nil), base.Metrics.Collectors...)
	f(&out)
	return out
}

func TestValidateAcceptsValidSpecs(t *testing.T) {
	bench := validSpec(KindBenchmark)
	if err := bench.Validate(); err != nil {
		t.Fatalf("benchmark spec: %v", err)
	}
	opt := validSpec(KindOptimize)
	opt.Source = &Source{Repo: "nomanoma121/llama.cpp", Ref: "main"}
	opt.Budget = &Budget{MaxRounds: 20}
	if err := opt.Validate(); err != nil {
		t.Fatalf("optimize spec: %v", err)
	}
	if _, err := opt.Digest(); err != nil {
		t.Fatalf("digest: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	mutate := func(f func(*Spec)) Spec { return mutateSpec(validSpec(KindBenchmark), f) }
	tests := []struct {
		name string
		spec Spec
		want string
	}{
		{"unknown kind", mutate(func(s *Spec) { s.Kind = "visual" }), "kind must be"},
		{"missing model", mutate(func(s *Spec) { s.Model.ID = "" }), "model.id"},
		{"model with path", mutate(func(s *Spec) { s.Model.ID = "a/b" }), "model.id"},
		{"missing engine", mutate(func(s *Spec) { s.Runtime.Engine = "" }), "runtime.engine"},
		{"missing image", mutate(func(s *Spec) { s.Runtime.Image = " " }), "runtime.image"},
		{"empty arg", mutate(func(s *Spec) { s.Runtime.Args = []string{"-ngl", ""} }), "runtime.args[1]"},
		{"bad port", mutate(func(s *Spec) { s.Runtime.Ready.Port = 0 }), "runtime.ready.port"},
		{"relative ready path", mutate(func(s *Spec) { s.Runtime.Ready.Path = "health" }), "runtime.ready.path"},
		{"no cases", mutate(func(s *Spec) { s.Workload.Cases = nil }), "workload.cases must not be empty"},
		{"duplicate case names", mutate(func(s *Spec) {
			s.Workload.Cases = append(s.Workload.Cases, Case{Name: "short", PromptText: "hi", MaxTokens: 8})
		}), "is duplicated"},
		{"case with two prompts", mutate(func(s *Spec) { s.Workload.Cases[0].PromptText = "hi" }), "both prompt and prompt_text"},
		{"case with no prompt", mutate(func(s *Spec) { s.Workload.Cases[0].Prompt = "" }), "needs prompt or prompt_text"},
		{"prompt escapes the repo", mutate(func(s *Spec) { s.Workload.Cases[0].Prompt = "../etc/passwd" }), "repository-relative"},
		{"max_tokens too large", mutate(func(s *Spec) { s.Workload.Cases[0].MaxTokens = MaxMaxTokens + 1 }), "max_tokens"},
		{"repeats too large", mutate(func(s *Spec) { s.Workload.Cases[0].Repeats = MaxRepeats + 1 }), "repeats"},
		{"concurrency too large", mutate(func(s *Spec) { s.Workload.Concurrency = MaxConcurrency + 1 }), "workload.concurrency"},
		{"temperature out of range", mutate(func(s *Spec) {
			v := 3.0
			s.Workload.Sampling = &Sampling{Temperature: &v}
		}), "temperature"},
		{"no collector", mutate(func(s *Spec) { s.Metrics.Collectors = nil }), "collectors must not be empty"},
		{"unknown collector", mutate(func(s *Spec) { s.Metrics.Collectors = []Collector{"nvml"} }), "is unknown"},
		{"duplicate collector", mutate(func(s *Spec) {
			s.Metrics.Collectors = []Collector{CollectorHarness, CollectorHarness}
		}), "is duplicated"},
		{"collector without harness", mutate(func(s *Spec) { s.Metrics.Collectors = []Collector{CollectorRuntime} }), "must include"},
		{"missing output", mutate(func(s *Spec) { s.Output.Dir = "" }), "output.dir is required"},
		{"absolute output", mutate(func(s *Spec) { s.Output.Dir = "/tmp/out" }), "repository-relative"},
		{"output outside experiments", mutate(func(s *Spec) { s.Output.Dir = "results/x" }), "must be under experiments"},
		{"budget on benchmark", mutate(func(s *Spec) { s.Budget = &Budget{MaxRounds: 3} }), "budget is only valid"},
		{"optimize without source", mutate(func(s *Spec) { s.Kind = KindOptimize }), "source is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateConstraints(t *testing.T) {
	s := validSpec(KindBenchmark)
	c := Constraints{
		Engines:     []string{"freetoken"},
		Images:      []string{"ghcr.io/other/llama.cpp@sha256:abc"},
		Models:      []string{"other-model"},
		OutputRoots: []string{"experiments/other"},
	}
	err := s.ValidateConstraints(c)
	if err == nil {
		t.Fatal("expected allowlist errors")
	}
	for _, want := range []string{"runtime.engine", "runtime.image", "model.id", "output.dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	// A permissive configuration accepts the same spec.
	if err := s.ValidateConstraints(Constraints{}); err != nil {
		t.Fatalf("unconstrained: %v", err)
	}

	// The operator round cap applies to optimize jobs.
	opt := validSpec(KindOptimize)
	opt.Source = &Source{Repo: "o/r", Ref: "main"}
	opt.Budget = &Budget{MaxRounds: 50}
	if err := opt.ValidateConstraints(Constraints{MaxRounds: 10}); err == nil {
		t.Fatal("expected the operator round cap to reject max_rounds=50")
	}
}

func TestReservedArgsCannotBeOverridden(t *testing.T) {
	reserved := Constraints{ReservedArgs: []string{"--model", "-m", "--host", "--port", "--metrics", "--log-file"}}
	tests := []struct {
		name string
		args []string
		want bool // true means the spec must be rejected
	}{
		{"model path", []string{"--model", "/other/model.gguf"}, true},
		{"short model flag", []string{"-m", "/other/model.gguf"}, true},
		{"equals form", []string{"--model=/other/model.gguf"}, true},
		{"listen address", []string{"--host", "0.0.0.0"}, true},
		{"metrics endpoint", []string{"--metrics", "/dev/null"}, true},
		{"tuning", []string{"-ngl", "99", "--ctx-size=4096", "-c", "4096"}, false},
		{"bare value that looks like a flag", []string{"-ngl", "--model"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := mutateSpec(validSpec(KindBenchmark), func(s *Spec) { s.Runtime.Args = tc.args })
			err := s.ValidateConstraints(reserved)
			if tc.want && err == nil {
				t.Fatalf("args %v were accepted", tc.args)
			}
			if !tc.want && err != nil {
				t.Fatalf("args %v were rejected: %v", tc.args, err)
			}
		})
	}
	// Without the operator list there is nothing to reserve, so the same
	// spec validates: the adapter owns the list, not this package.
	s := mutateSpec(validSpec(KindBenchmark), func(s *Spec) { s.Runtime.Args = []string{"--host", "0.0.0.0"} })
	if err := s.Validate(); err != nil {
		t.Fatalf("unconstrained validation rejected args: %v", err)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse(strings.NewReader("kind: benchmark\nnope: 1\n"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestCredentialsCannotSelectPrivilegedSettings(t *testing.T) {
	// A spec that tries to pick a GPU lease or a pause target is rejected as
	// an unknown field rather than silently ignored.
	for _, body := range []string{
		"kind: benchmark\nlease: gpu-prod\n",
		"kind: benchmark\ntarget: inference\n",
		"kind: benchmark\npause:\n  path: spec.replicas\n",
	} {
		if _, err := Parse(strings.NewReader(body)); !errors.Is(err, ErrInvalid) {
			t.Errorf("spec %q: expected ErrInvalid, got %v", strings.TrimSpace(body), err)
		}
	}
}

func TestDigestIsStable(t *testing.T) {
	a := validSpec(KindBenchmark)
	first, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("digest is not stable: %s != %s", first, second)
	}
	b := validSpec(KindBenchmark)
	b.Runtime.Args = []string{"-ngl", "98"}
	other, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first == other {
		t.Fatal("digest ignores runtime.args")
	}
}

// TestIssueFormsMatchTemplates keeps the pre-filled Issue forms and the Go
// templates in step: the form content must parse and validate through the same
// path the controller uses, and it must equal the template that
// `llmbench job init` prints.
func TestIssueFormsMatchTemplates(t *testing.T) {
	for _, tc := range []struct {
		file string
		kind Kind
	}{
		{"../../.github/ISSUE_TEMPLATE/benchmark.yml", KindBenchmark},
		{"../../.github/ISSUE_TEMPLATE/optimize.yml", KindOptimize},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.FromSlash(tc.file))
			if err != nil {
				t.Fatalf("read form: %v", err)
			}
			var form struct {
				Labels []string `yaml:"labels"`
				Body   []struct {
					ID         string `yaml:"id"`
					Attributes struct {
						Value string `yaml:"value"`
					} `yaml:"attributes"`
				} `yaml:"body"`
			}
			if err := yaml.Unmarshal(raw, &form); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			wantLabel := "llmbench:" + string(tc.kind)
			var hasLabel bool
			for _, l := range form.Labels {
				if l == wantLabel {
					hasLabel = true
				}
			}
			if !hasLabel {
				t.Errorf("form does not carry the %s label", wantLabel)
			}
			var prefilled string
			for _, b := range form.Body {
				if b.ID == "jobspec" {
					prefilled = b.Attributes.Value
				}
			}
			if prefilled == "" {
				t.Fatal("form has no jobspec textarea with a value")
			}
			printable, err := Template(tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimRight(prefilled, "\n") != strings.TrimRight(printable, "\n") {
				t.Errorf("the pre-filled spec drifted from `llmbench job init --kind %s`", tc.kind)
			}
			// GitHub renders `render: yaml` textareas inside a ```yaml fence.
			spec, err := FromIssueBody("```yaml\n" + prefilled + "```\n")
			if err != nil {
				t.Fatalf("form spec does not parse through the Issue path: %v", err)
			}
			if spec.Kind != tc.kind {
				t.Errorf("form spec kind = %q", spec.Kind)
			}
			if err := spec.Validate(); err != nil {
				t.Errorf("form spec does not validate: %v", err)
			}
		})
	}
}

func TestTemplatesAreValid(t *testing.T) {
	for _, kind := range []Kind{KindBenchmark, KindOptimize} {
		text, err := Template(kind)
		if err != nil {
			t.Fatalf("template %s: %v", kind, err)
		}
		spec, err := Parse(strings.NewReader(text))
		if err != nil {
			t.Fatalf("template %s does not parse: %v", kind, err)
		}
		if spec.Kind != kind {
			t.Fatalf("template %s has kind %s", kind, spec.Kind)
		}
		// The template is the intended starting point, so it must be
		// structurally valid: only the placeholder values are meant to change.
		if err := spec.Validate(); err != nil {
			t.Fatalf("template %s does not validate: %v", kind, err)
		}
	}
	if _, err := Template("visual"); err == nil {
		t.Fatal("expected an error for an unknown template kind")
	}
}
