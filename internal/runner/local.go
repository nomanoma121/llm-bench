// Package runner implements the benchmark execution strategies: the local
// command runner (trusted development only) and, in later milestones, the
// Agent Sandbox runner.
package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

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
func (l *Local) Execute(ctx context.Context, r run.Run) (run.ExecutionOutputs, error) {
	if r.Artifacts.Dir == "" {
		return run.ExecutionOutputs{}, errors.New("runner: run has no artifact directory")
	}
	var cfg experiment.Config
	if err := json.Unmarshal([]byte(r.RecipeJSON), &cfg); err != nil {
		return run.ExecutionOutputs{}, fmt.Errorf("runner: decode recipe snapshot: %w", err)
	}
	if len(cfg.Invoke.Argv) == 0 {
		return run.ExecutionOutputs{}, errors.New("runner: recipe snapshot has empty invoke argv")
	}
	// Absolute paths are mandatory: cmd.Dir is relative to the controller's
	// working directory, so a relative artifact dir would double up inside
	// the child process.
	artifactDir, err := filepath.Abs(r.Artifacts.Dir)
	if err != nil {
		return run.ExecutionOutputs{}, err
	}
	modelsRoot, err := filepath.Abs(l.ModelsRoot)
	if err != nil {
		return run.ExecutionOutputs{}, err
	}
	promptPath := filepath.Join(artifactDir, "input", "prompt.md")
	// The benchmark writes into a staging directory; sealOutput validates the
	// single-file contract, fsyncs it and atomically renames it into place.
	outputDir := stagingOutput(artifactDir)
	if err := os.RemoveAll(outputDir); err != nil {
		return run.ExecutionOutputs{}, err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return run.ExecutionOutputs{}, err
	}

	// A measurement run produces evidence instead of a visual payload: the
	// single-file contract applies to visual runs only, and the driver — not
	// the recipe's invoke — is what the harness measures with
	// (docs/optimization.md §3/§5.4).
	if runKind(r) == run.RunKindMeasurement {
		started := time.Now()
		stdout, driverErr := l.runDriver(ctx, r, artifactDir, cfg, modelsRoot, promptPath)
		if ctx.Err() != nil {
			return run.ExecutionOutputs{}, fmt.Errorf("runner: execution interrupted: %w", ctx.Err())
		}
		evidence, err := sealEvidence(r, artifactDir, time.Since(started), stdout, driverErr)
		if err != nil {
			return run.ExecutionOutputs{}, err
		}
		return run.ExecutionOutputs{Evidence: evidence}, nil
	}

	cmd := exec.CommandContext(ctx, cfg.Invoke.Argv[0], cfg.Invoke.Argv[1:]...)
	cmd.Dir = artifactDir
	cmd.Env = l.envFor(r, cfg, artifactDir, modelsRoot, promptPath)

	logPath := filepath.Join(artifactDir, "invoke.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return run.ExecutionOutputs{}, err
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	fmt.Fprintf(logFile, "$ %s\n", cfg.Invoke.Argv)

	started := time.Now()
	runErr := cmd.Run()
	wallClock := time.Since(started)
	fmt.Fprintf(logFile, "[exit: %v]\n", runErr)

	if ctx.Err() != nil {
		return run.ExecutionOutputs{}, fmt.Errorf("runner: execution interrupted: %w", ctx.Err())
	}
	if runErr != nil {
		return run.ExecutionOutputs{}, fmt.Errorf("runner: invoke failed: %w", runErr)
	}

	indexPath := filepath.Join(outputDir, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil {
		return run.ExecutionOutputs{}, fmt.Errorf("runner: invoke did not produce output/index.html: %w", err)
	}
	if !info.Mode().IsRegular() {
		return run.ExecutionOutputs{}, errors.New("runner: output/index.html is not a regular file")
	}
	digest, payload, err := sealOutput(artifactDir, outputDir)
	if err != nil {
		return run.ExecutionOutputs{}, err
	}
	logSum, err := hashFile(logPath)
	if err != nil {
		return run.ExecutionOutputs{}, err
	}
	out := run.ExecutionOutputs{Artifacts: run.Artifacts{
		Dir:            artifactDir,
		IndexSHA256:    payload[0].SHA256,
		LogSHA256:      logSum,
		ArtifactDigest: digest,
	}}
	// A visual run with a protocol also carries harness evidence.
	if needsEvidence(r) {
		stdout, driverErr := l.runDriver(ctx, r, artifactDir, cfg, modelsRoot, promptPath)
		evidence, err := sealEvidence(r, artifactDir, wallClock, stdout, driverErr)
		if err != nil {
			return run.ExecutionOutputs{}, err
		}
		out.Evidence = evidence
	}
	return out, nil
}

// envFor builds the child environment from an allowlist: LLMBENCH_* variables
// computed for this run plus generic process lookups. Controller credentials
// (LLMBENCH_GITHUB_TOKEN and friends) are deliberately not inherited.
func (l *Local) envFor(r run.Run, cfg experiment.Config, artifactDir, modelsRoot, promptPath string) []string {

	env := []string{
		"LLMBENCH_RUN_ID=" + r.ID,
		"LLMBENCH_PROMPT_PATH=" + promptPath,
		"LLMBENCH_OUTPUT_DIR=" + stagingOutput(artifactDir),
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

// runDriver executes the frozen driver argv and returns its stdout. The driver
// is the harness's trusted measurement code: the candidate runtime cannot
// replace it or its output channel (docs/optimization.md §5.4). A protocol
// without a driver yields no output, which the protocol's required sources
// then reject.
func (l *Local) runDriver(ctx context.Context, r run.Run, artifactDir string, cfg experiment.Config, modelsRoot, promptPath string) ([]byte, error) {
	argv, err := driverArgv(r)
	if err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = artifactDir
	cmd.Env = l.envFor(r, cfg, artifactDir, modelsRoot, promptPath)
	logPath := filepath.Join(artifactDir, "driver.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = logFile
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("driver %v failed: %w", argv, err)
	}
	return stdout.Bytes(), nil
}
