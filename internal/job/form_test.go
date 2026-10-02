package job

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

// The committed issue forms are what Form generates.
func TestFormsAreGenerated(t *testing.T) {
	benchmarks, err := Benchmarks("../../benchmarks")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{Benchmark, Optimize} {
		want, err := Form(kind, benchmarks)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile("../../.github/ISSUE_TEMPLATE/" + string(kind) + ".yml")
		if err != nil || string(got) != string(want) {
			t.Errorf("%s.yml is stale; run `go run ./cmd/llmbench job form %s > .github/ISSUE_TEMPLATE/%s.yml`", kind, kind, kind)
		}
	}
}

// A body as GitHub writes it for a submitted form.
const formBody = "### Preset\n\nstrata · Flash-Next IQ2_XS · 2GPU\n\n" +
	"### Benchmark\n\nvisual\n\n" +
	"### Harness\n\nopencode\n\n" +
	"### Reasoning effort\n\nlow\n\n" +
	"### Max tokens\n\n28672\n\n" +
	"### Repeats\n\n2\n\n" +
	"### Leave out metrics\n\n- [ ] runtime\n- [X] gpu\n- [ ] host\n\n" +
	"### Extra runtime args\n\n--port 9000\n\n" +
	"### Spec override\n\n_No response_\n\n" +
	"### Notes\n\n_No response_"

func TestFromIssue(t *testing.T) {
	s, err := FromIssue(Benchmark, formBody)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Workload[0]
	if s.Model != "strata/config/strata-iq2_xs.json" || s.Runtime.Engine != "strata" ||
		!reflect.DeepEqual(s.Runtime.Args, []string{"--gpu", "0,1", "--port", "9000"}) ||
		c.Name != "visual" || c.Prompt != "benchmarks/visual/prompt.md" || c.Harness.Name != "opencode" || c.MaxTokens != 0 || c.Repeats != 2 ||
		s.Sampling.ReasoningEffort != "low" || *s.Sampling.Seed != 1 || !reflect.DeepEqual(s.Metrics, []string{"runtime", "host"}) {
		t.Fatalf("unexpected spec %+v %+v", s, c)
	}
	if !reflect.DeepEqual(presetNamedArgs("strata · Flash-Next IQ2_XS · 2GPU"), []string{"--gpu", "0,1"}) {
		t.Fatal("the preset's arguments were changed")
	}
}

func TestFromIssueOverride(t *testing.T) {
	body := formBody[:len(formBody)-len("### Spec override\n\n_No response_\n\n### Notes\n\n_No response_")] +
		"### Spec override\n\n```yaml\nruntime:\n  engine: llamacpp\n### not a heading\nsampling:\n  seed: 7\n```\n\n### Notes\n\nhi"
	s, err := FromIssue(Optimize, body+"\n\n### Source repository\n\nggml-org/llama.cpp\n\n### Source ref\n\nmaster\n\n### Max rounds\n\n5")
	if err != nil {
		t.Fatal(err)
	}
	if s.Runtime.Engine != "llamacpp" || *s.Sampling.Seed != 7 || s.Sampling.ReasoningEffort != "low" ||
		s.Model != "strata/config/strata-iq2_xs.json" || s.Source.Repo != "ggml-org/llama.cpp" || s.Budget.MaxRounds != 5 {
		t.Fatalf("unexpected spec %+v %+v", s, s.Sampling)
	}
}

func TestFromIssueRejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown preset": "### Preset\n\nnope\n\n### Benchmark\n\nvisual\n\n### Max tokens\n\n10",
		"bad number":     "### Preset\n\nllama.cpp · Qwen3.8-27B Q4_0\n\n### Benchmark\n\nvisual\n\n### Max tokens\n\nmany",
		"unknown field":  "### Preset\n\nllama.cpp · Qwen3.8-27B Q4_0\n\n### Benchmark\n\nvisual\n\n### Max tokens\n\n10\n\n### Spec override\n\n```yaml\nfoo: 1\n```",
		"no source":      "### Preset\n\nllama.cpp · Qwen3.8-27B Q4_0\n\n### Benchmark\n\nvisual\n\n### Max tokens\n\n10",
	} {
		kind := Benchmark
		if name == "no source" {
			kind = Optimize
		}
		if _, err := FromIssue(kind, body); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func presetNamedArgs(name string) []string {
	p, _ := presetNamed(name)
	return p.Args
}
