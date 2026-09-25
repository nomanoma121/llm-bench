package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/operator"
)

// regression: the serve dispatcher must pick up submitted runs.
func TestDispatcherAdvancesSubmittedRun(t *testing.T) {
	root := t.TempDir()
	promptDir := filepath.Join(root, "examples")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	out := t.TempDir()
	g := &globalFlags{root: root, state: state, output: out}

	opCfg := operator.Config{
		Targets: map[string]operator.Target{
			"local": {Hooks: []operator.CommandHook{}, AllowHTTPLocal: true},
		},
	}
	exPath := filepath.Join(root, "exp.yaml")
	if err := os.WriteFile(exPath, []byte("model: m\nbenchmark: examples/prompt.md\ntarget: local\nruntime:\n  engine: e\n  context_size: 8\ninvoke:\n  argv: [\"/bin/sh\",\"-c\",\"printf x > \\\"$LLMBENCH_OUTPUT_DIR/index.html\\\"\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, inputs, err := prepareRun(g, opCfg, exPath, "")
	if err != nil {
		t.Fatal(err)
	}
	engine := buildEngine(g, opCfg, 100*time.Millisecond, sandboxDepsFunc(g, opCfg), gitopsDepsFunc(g, opCfg))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := engine.Submit(ctx, r, inputs); err != nil {
		t.Fatal(err)
	}
	go engine.Run(ctx)

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("run did not complete; state on disk is stuck")
		default:
		}
		cur, err := engine.Store.LoadRun(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.Phase.Terminal() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
