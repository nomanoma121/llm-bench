package job

import (
	"errors"
	"testing"
)

func TestTemplatesAreValid(t *testing.T) {
	for _, kind := range []Kind{Benchmark, Optimize} {
		body := "notes\n\n```yaml\n" + Template(kind) + "```\n"
		spec, err := FromIssueBody(body)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if spec.Kind != kind || spec.Workload[0].RepeatCount() != 3 {
			t.Fatalf("%s: unexpected spec %+v", kind, spec)
		}
	}
}

func TestInvalidSpecs(t *testing.T) {
	for name, body := range map[string]string{
		"no block":        "just text",
		"unknown field":   "```yaml\nkind: benchmark\nfoo: 1\n```",
		"optimize no src": "```yaml\nkind: optimize\nmodel: m\nruntime: {engine: llamacpp}\nworkload: [{name: a, prompt_text: hi, max_tokens: 1}]\n```",
		"escaping prompt": "```yaml\nkind: benchmark\nmodel: m\nruntime: {engine: llamacpp}\nworkload: [{name: a, prompt: ../x, max_tokens: 1}]\n```",
	} {
		if _, err := FromIssueBody(body); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestModelName(t *testing.T) {
	for model, want := range map[string]string{
		"Qwen3.8-27B/Qwen3.8-27B-Q4_0.gguf":                            "Qwen3.8-27B-Q4_0",
		"UD-Q4_K_XL/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf": "Qwen3.8-Flash-Next-UD-Q4_K_XL",
		"strata/config/strata-iq2_xs.json":                             "strata-iq2_xs",
		"qwen38-flash-next-nvfp4":                                      "qwen38-flash-next-nvfp4",
	} {
		if got := (Spec{Model: model}).ModelName(); got != want {
			t.Errorf("%s: %s", model, got)
		}
	}
}
