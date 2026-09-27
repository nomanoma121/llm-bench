package job

import (
	"errors"
	"strings"
	"testing"
)

func TestFromIssueBody(t *testing.T) {
	body := "### What to measure\n\nplease benchmark this\n\n```yaml\n" +
		strings.TrimRight(benchmarkTemplate, "\n") + "\n```\n\nthanks!\n"
	spec, err := FromIssueBody(body)
	if err != nil {
		t.Fatalf("FromIssueBody: %v", err)
	}
	if spec.Kind != KindBenchmark {
		t.Fatalf("kind = %q", spec.Kind)
	}
	if spec.Model.ID != "REPLACE-model-id" {
		t.Fatalf("model = %q", spec.Model.ID)
	}
}

func TestFromIssueBodyUsesTheFirstBlock(t *testing.T) {
	body := "```yaml\nkind: benchmark\nmodel: {id: first}\n```\n\n```yaml\nkind: optimize\nmodel: {id: second}\n```\n"
	spec, err := FromIssueBody(body)
	if err != nil {
		t.Fatalf("FromIssueBody: %v", err)
	}
	if spec.Model.ID != "first" {
		t.Fatalf("picked the wrong block: %q", spec.Model.ID)
	}
}

func TestFromIssueBodyAcceptsFenceVariants(t *testing.T) {
	for _, body := range []string{
		"```YAML\nkind: benchmark\n```\n",
		"```yml\nkind: benchmark\n```\n",
		"  ```yaml\nkind: benchmark\n```\n",
		"```yaml\r\nkind: benchmark\r\n```\r\n",
	} {
		if _, err := FromIssueBody(body); err != nil {
			t.Errorf("body %q: %v", body, err)
		}
	}
}

func TestFromIssueBodyRejects(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no block", "benchmark please\n"},
		{"wrong language", "```json\n{\"kind\":\"benchmark\"}\n```\n"},
		{"unterminated", "```yaml\nkind: benchmark\n"},
		{"unknown field", "```yaml\nkind: benchmark\nlease: gpu\n```\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromIssueBody(tc.body)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected ErrInvalid, got %v", err)
			}
		})
	}
}
