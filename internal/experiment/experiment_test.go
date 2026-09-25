package experiment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndValidate(t *testing.T) {
	root := t.TempDir()
	promptDir := filepath.Join(root, "benchmarks", "visual")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "prompt.md"), []byte("Create a 3D scene."), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("valid", func(t *testing.T) {
		in := `
model: example-model
benchmark: benchmarks/visual/prompt.md
target: local
runtime:
  engine: example
  variant: upstream
  context_size: 4096
invoke:
  argv: ["go", "version"]
`
		c, err := Parse(strings.NewReader(in))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := Validate(c, root); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if c.Runtime.ContextSize != 4096 {
			t.Fatalf("context_size = %d", c.Runtime.ContextSize)
		}
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		in := `
model: m
benchmark: benchmarks/visual/prompt.md
target: local
runtime:
  engine: example
  context_size: 8
invoke:
  argv: ["true"]
privileged:
  pause_manifests: true
`
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Fatal("expected error for unknown field")
		}
	})

	t.Run("empty file is rejected", func(t *testing.T) {
		if _, err := Parse(strings.NewReader("")); err == nil {
			t.Fatal("expected error for empty recipe")
		}
	})

	t.Run("validation errors", func(t *testing.T) {
		in := `
model: ""
benchmark: benchmarks/visual/missing.md
target: ""
runtime:
  engine: ""
  context_size: 0
invoke:
  argv: []
`
		c, err := Parse(strings.NewReader(in))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		err = Validate(c, root)
		if err == nil {
			t.Fatal("expected validation errors")
		}
		for _, want := range []string{"model is required", "target is required", "context_size", "invoke.argv"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("start recipe validation", func(t *testing.T) {
		in := `
model: m
benchmark: benchmarks/visual/prompt.md
target: local
runtime:
  engine: example
  context_size: 8
  start:
    argv: []
invoke:
  argv: ["true"]
`
		c, err := Parse(strings.NewReader(in))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := Validate(c, root); err == nil || !strings.Contains(err.Error(), "start.argv") {
			t.Fatalf("expected start.argv error, got %v", err)
		}
	})
}

func TestCanonicalJSONIsDeterministic(t *testing.T) {
	in := `
model: m
benchmark: benchmarks/visual/prompt.md
target: local
runtime:
  engine: example
  variant: upstream
  context_size: 8
invoke:
  argv: ["true"]
`
	c1, _ := Parse(strings.NewReader(in))
	c2, _ := Parse(strings.NewReader(in))
	a, err := CanonicalJSON(c1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalJSON(c2)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("canonical json not deterministic:\n%s\n%s", a, b)
	}
}
