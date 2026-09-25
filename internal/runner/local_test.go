package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/run"
)

func recipe(t *testing.T, invoke []string) string {
	t.Helper()
	cfg := experiment.Config{
		Model:     "example-model",
		Benchmark: "benchmarks/visual/prompt.md",
		Target:    "local",
		Runtime:   experiment.Runtime{Engine: "example", ContextSize: 4096},
		Invoke:    experiment.Invoke{Argv: invoke},
	}
	b, err := experiment.CanonicalJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func artifactRun(dir, recipeJSON string) run.Run {
	return run.Run{
		ID:           "r1",
		RecipeJSON:   recipeJSON,
		Artifacts:    run.Artifacts{Dir: dir},
		PromptSHA256: "x",
	}
}

func TestLocalExecuteWritesIndexAndHashes(t *testing.T) {
	// Controller credentials must never leak into the child environment.
	t.Setenv("LLMBENCH_GITHUB_TOKEN", "secret")
	t.Setenv("LLMBENCH_API_TOKEN", "secret")

	dir := t.TempDir()
	inputDir := filepath.Join(dir, "input")
	if err := os.MkdirAll(inputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := "Create a scene."
	if err := os.WriteFile(filepath.Join(inputDir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}

	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c",
		`printf '<html></html>' > "$LLMBENCH_OUTPUT_DIR/index.html"; env > "$LLMBENCH_OUTPUT_DIR/env.txt"`}))

	artifacts, err := l.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.Dir != dir {
		t.Fatalf("dir = %q", artifacts.Dir)
	}
	index := filepath.Join(dir, "output", "index.html")
	b, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256HexOf(b)
	if artifacts.IndexSHA256 != sum {
		t.Fatalf("index hash mismatch: %s vs %s", artifacts.IndexSHA256, sum)
	}
	if artifacts.LogSHA256 == "" {
		t.Fatal("log hash missing")
	}

	env, err := os.ReadFile(filepath.Join(dir, "output", "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	e := string(env)
	for _, want := range []string{
		"LLMBENCH_RUN_ID=r1",
		"LLMBENCH_CONTEXT_SIZE=4096",
		"LLMBENCH_MODEL_ID=example-model",
		"LLMBENCH_PROMPT_PATH=" + filepath.Join(inputDir, "benchmarks/visual/prompt.md"),
	} {
		if !strings.Contains(e, want) {
			t.Errorf("env missing %q:\n%s", want, e)
		}
	}
	if strings.Contains(e, "secret") {
		t.Fatal("controller credentials leaked into child environment")
	}
}

func sha256HexOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestLocalExecuteFailsWithoutIndex(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c", "echo done"}))
	if _, err := l.Execute(context.Background(), r); err == nil {
		t.Fatal("expected failure when index.html is missing")
	}
}

func TestLocalExecuteFailsOnNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c", "exit 3"}))
	if _, err := l.Execute(context.Background(), r); err == nil {
		t.Fatal("expected failure on non-zero exit")
	}
}

func TestLocalExecuteRequiresRecipeSnapshot(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)
	r := artifactRun(dir, "not-json")
	if _, err := l.Execute(context.Background(), r); err == nil {
		t.Fatal("expected recipe decode failure")
	}
}
