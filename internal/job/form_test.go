package job

import (
	"errors"
	"os"
	"reflect"
	"strings"
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

// form is a body as GitHub writes it for a submitted form.
func form(fields ...string) string {
	values := map[string]string{
		"Model":              "Flash-Next UD-Q4_K_XL",
		"Runtime":            "llamacpp",
		"GPUs":               "GPU 1 · RTX 3060 12GB, GPU 0 · RTX 3060 12GB",
		"Context":            "64K",
		"MTP":                "on",
		"KV cache":           "q8_0",
		"Experts on CPU":     "default",
		"Extra runtime args": `-ot 'per_layer_token_embd\.weight=CPU' --flag "a b"`,
		"Benchmark":          "visual",
		"Harness":            "opencode",
		"Extra harness args": "_No response_",
		"Max tokens":         "28672",
		"Spec override":      "_No response_",
		"Notes":              "_No response_",
	}
	for i := 0; i+1 < len(fields); i += 2 {
		values[fields[i]] = fields[i+1]
	}
	var b strings.Builder
	for label, v := range values {
		b.WriteString("### " + label + "\n\n" + v + "\n\n")
	}
	return b.String()
}

func TestFromIssue(t *testing.T) {
	s, err := FromIssue(Benchmark, form())
	if err != nil {
		t.Fatal(err)
	}
	r, c := s.Runtime, s.Workload[0]
	if s.Model != "UD-Q4_K_XL/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf" || r.Engine != "llamacpp" ||
		!reflect.DeepEqual(r.GPUs, []int{0, 1}) || r.Context != 65536 || !*r.MTP || r.KVCache != "q8_0" || r.ExpertsOnCPU != nil ||
		!reflect.DeepEqual(r.Args, []string{"-ot", `per_layer_token_embd\.weight=CPU`, "--flag", "a b"}) ||
		c.Name != "visual" || c.Prompt != "benchmarks/visual/prompt.md" || c.Harness.Name != "opencode" || c.MaxTokens != 0 ||
		s.Sampling.ReasoningEffort != "" || *s.Sampling.Seed != 1 {
		t.Fatalf("unexpected spec %+v %+v", s, c)
	}
}

func TestFromIssueDirect(t *testing.T) {
	s, err := FromIssue(Benchmark, form("Harness", "direct"))
	if err != nil {
		t.Fatal(err)
	}
	if c := s.Workload[0]; c.Harness != nil || c.MaxTokens != 28672 {
		t.Fatalf("unexpected spec %+v %+v", c, s.Sampling)
	}
}

// GitHub rejects a form whose dropdown offers "None".
func TestFormsAvoidNone(t *testing.T) {
	for _, kind := range []Kind{Benchmark, Optimize} {
		form, err := Form(kind, []string{"visual"})
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(form), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok && strings.EqualFold(strings.Trim(v, `"'`), "none") {
				t.Errorf("%s: option %q", kind, v)
			}
		}
	}
}

// An empty YAML field comes as an empty fenced block (issue #127).
func TestFromIssueEmptyOverride(t *testing.T) {
	if _, err := FromIssue(Benchmark, form("Spec override", "```yaml\n\n```")); err != nil {
		t.Fatal(err)
	}
}

func TestFromIssueOverride(t *testing.T) {
	body := form("Spec override", "```yaml\nruntime:\n  engine: llamacpp\n  context: 4096\n### not a heading\nsampling:\n  seed: 7\n```") +
		"### Source repository\n\nggml-org/llama.cpp\n\n### Source ref\n\nmaster\n\n### Max rounds\n\n5"
	s, err := FromIssue(Optimize, body)
	if err != nil {
		t.Fatal(err)
	}
	if s.Runtime.Context != 4096 || len(s.Runtime.GPUs) != 2 || *s.Sampling.Seed != 7 ||
		s.Source.Repo != "ggml-org/llama.cpp" || s.Budget.MaxRounds != 5 {
		t.Fatalf("unexpected spec %+v %+v", s, s.Sampling)
	}
}

func TestFromIssueRejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown model":       form("Model", "nope"),
		"unknown gpu":         form("GPUs", "GPU 7 · H100"),
		"model not on engine": form("Model", "Flash-Next IQ2_XS"),
		"bad number":          form("Harness", "direct", "Max tokens", "many"),
		"unsupported setting": form("Model", "Flash-Next IQ2_XS", "Runtime", "strata", "MTP", "off"),
		"unterminated quote":  form("Extra runtime args", "--x 'y"),
		"unknown field":       form("Spec override", "```yaml\nfoo: 1\n```"),
	} {
		if _, err := FromIssue(Benchmark, body); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if _, err := FromIssue(Optimize, form()); !errors.Is(err, ErrInvalid) {
		t.Errorf("optimize without a source: got %v", err)
	}
}
