// Package runner implements the benchmark execution strategies: the local
// command runner (trusted development only) and, in later milestones, the
// Agent Sandbox runner.
package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// Local executes the experiment invocation on the controller host. It is a
// trusted-development convenience, not a security boundary: the child process
// runs with the controller's filesystem and network permissions. Only the
// environment is restricted (see envFor).
type Local struct {
	// ModelsRoot is the directory that contains models/<model-id>.
	ModelsRoot string
}

// NewLocal constructs the local runner.
func NewLocal(modelsRoot string) *Local {
	return &Local{ModelsRoot: modelsRoot}
}

// Execute implements run.Executor. The invocation's stdout/stderr are stored
// in the artifact directory; a successful execution must have produced
// <output>/index.html, whose sha256 is recorded at persistence time.
func (l *Local) Execute(ctx context.Context, r run.Run) (run.Artifacts, error) {
	if r.Artifacts.Dir == "" {
		return run.Artifacts{}, errors.New("runner: run has no artifact directory")
	}
	var cfg experiment.Config
	if err := json.Unmarshal([]byte(r.RecipeJSON), &cfg); err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: decode recipe snapshot: %w", err)
	}
	if len(cfg.Invoke.Argv) == 0 {
		return run.Artifacts{}, errors.New("runner: recipe snapshot has empty invoke argv")
	}
	// Absolute paths are mandatory: cmd.Dir is relative to the controller's
	// working directory, so a relative artifact dir would double up inside
	// the child process.
	artifactDir, err := filepath.Abs(r.Artifacts.Dir)
	if err != nil {
		return run.Artifacts{}, err
	}
	modelsRoot, err := filepath.Abs(l.ModelsRoot)
	if err != nil {
		return run.Artifacts{}, err
	}
	promptPath := filepath.Join(artifactDir, "input", "prompt.md")
	outputDir := filepath.Join(artifactDir, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return run.Artifacts{}, err
	}

	cmd := exec.CommandContext(ctx, cfg.Invoke.Argv[0], cfg.Invoke.Argv[1:]...)
	cmd.Dir = artifactDir
	cmd.Env = l.envFor(r, cfg, artifactDir, modelsRoot, promptPath)

	logPath := filepath.Join(artifactDir, "invoke.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return run.Artifacts{}, err
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	fmt.Fprintf(logFile, "$ %s\n", cfg.Invoke.Argv)

	runErr := cmd.Run()
	fmt.Fprintf(logFile, "[exit: %v]\n", runErr)

	if ctx.Err() != nil {
		return run.Artifacts{}, fmt.Errorf("runner: execution interrupted: %w", ctx.Err())
	}
	if runErr != nil {
		return run.Artifacts{}, fmt.Errorf("runner: invoke failed: %w", runErr)
	}

	indexPath := filepath.Join(outputDir, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: invoke did not produce output/index.html: %w", err)
	}
	if !info.Mode().IsRegular() {
		return run.Artifacts{}, errors.New("runner: output/index.html is not a regular file")
	}
	indexSum, err := hashFile(indexPath)
	if err != nil {
		return run.Artifacts{}, err
	}
	logSum, err := hashFile(logPath)
	if err != nil {
		return run.Artifacts{}, err
	}
	return run.Artifacts{
		Dir:         artifactDir,
		IndexSHA256: indexSum,
		LogSHA256:   logSum,
	}, nil
}

// envFor builds the child environment from an allowlist: LLMBENCH_* variables
// computed for this run plus generic process lookups. Controller credentials
// (LLMBENCH_GITHUB_TOKEN and friends) are deliberately not inherited.
func (l *Local) envFor(r run.Run, cfg experiment.Config, artifactDir, modelsRoot, promptPath string) []string {
	env := []string{
		"LLMBENCH_RUN_ID=" + r.ID,
		"LLMBENCH_PROMPT_PATH=" + promptPath,
		"LLMBENCH_OUTPUT_DIR=" + filepath.Join(artifactDir, "output"),
		"LLMBENCH_MODEL_ID=" + cfg.Model,
		"LLMBENCH_MODEL_PATH=" + filepath.Join(modelsRoot, "models", cfg.Model),
		fmt.Sprintf("LLMBENCH_CONTEXT_SIZE=%d", cfg.Runtime.ContextSize),
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}

func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("runner: hash %s: %w", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
