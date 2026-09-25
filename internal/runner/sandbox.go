package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// SandboxClient abstracts the Agent Sandbox transport. Implemented by
// internal/sandbox; declared here at the point of use. All methods must be
// safe to retry at the engine's write-ahead boundaries except where noted
// (Exec has side effects and is never replayed by the transport).
type SandboxClient interface {
	EnsureSandboxClaim(ctx context.Context, runID, warmPool string) error
	// Start launches the runtime detached; the handle is an opaque string.
	// Starting while a previous runtime is alive is a no-op.
	Start(ctx context.Context, runID string, argv []string, env map[string]string, cwd string) (handle string, err error)
	// Stop terminates the recorded runtime. Idempotent.
	Stop(ctx context.Context, runID, handle string) error
	Exec(ctx context.Context, runID string, argv []string, env map[string]string, cwd string) (stdout, stderr []byte, exitCode int, err error)
	Put(ctx context.Context, runID string, r io.Reader, dest string) error
	Pull(ctx context.Context, runID, path string) ([]byte, error)
	ReleaseSandboxClaim(ctx context.Context, runID string) error
}

// GitVerifier verifies commits and exports snapshots. Implemented by
// provenance.Git; the expected-file type lives in provenance to avoid a cycle.
type GitVerifier interface {
	VerifyCommit(ctx context.Context, commit string, expected []provenance.ExpectedFile) error
	Archive(ctx context.Context, commit string) ([]byte, error)
}

// Sandbox executes benchmarks inside an Agent Sandbox. Layout in the sandbox:
//
//	/workspace/src       the git archive of the input commit
//	/models/<model-id>   the operator-provisioned model mount
//	/workspace/output    the invocation's output directory
type Sandbox struct {
	Client SandboxClient
	Git    GitVerifier
	Root   string // repository root used for commit verification
	Limits func(target string) (readyLimit, execLimit time.Duration, ok bool)
	// ModelPins returns operator-pinned model tree digests per target.
	ModelPins func(target string) map[string]string
	Log       func() (sink interface{})
}

// In-sandbox constants. They match the chart's documented dev image layout.
const (
	sandboxWorkspace = "/workspace"
	sandboxSrc       = "/workspace/src"
	sandboxOutput    = "/workspace/output"
	sandboxModels    = "/models"
	sandboxLog       = "/workspace/invoke.log"
)

// Execute implements run.Executor. The caller (engine) has already persisted
// ExecutionState=invoking and applied the execution time limit on ctx.
func (s *Sandbox) Execute(ctx context.Context, r run.Run) (run.Artifacts, error) {
	if r.InputCommit == "" {
		return run.Artifacts{}, errors.New("runner: sandbox execution requires an input commit")
	}
	if r.Artifacts.Dir == "" {
		return run.Artifacts{}, errors.New("runner: run has no artifact directory")
	}
	var cfg experiment.Config
	if err := json.Unmarshal([]byte(r.RecipeJSON), &cfg); err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: decode recipe snapshot: %w", err)
	}

	// 1. Verify the commit against the frozen snapshot (double defense: the
	// snapshot was already validated at submit time).
	inputConfig, err := os.ReadFile(filepath.Join(r.Artifacts.Dir, "input", "config.yaml"))
	if err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: recipe snapshot missing: %w", err)
	}
	inputPrompt, err := os.ReadFile(filepath.Join(r.Artifacts.Dir, "input", "prompt.md"))
	if err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: prompt snapshot missing: %w", err)
	}
	expected := []provenance.ExpectedFile{
		{Path: r.Experiment, SHA256: sha256Hex(inputConfig)},
		{Path: cfg.Benchmark, SHA256: sha256Hex(inputPrompt)},
	}
	if err := s.Git.VerifyCommit(ctx, r.InputCommit, expected); err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: commit verification failed: %w", err)
	}

	// 2. Upload the source snapshot.
	tar, err := s.Git.Archive(ctx, r.InputCommit)
	if err != nil {
		return run.Artifacts{}, err
	}
	if err := s.Client.Put(ctx, r.ID, bytes.NewReader(tar), sandboxWorkspace+"/src.tar"); err != nil {
		return run.Artifacts{}, err
	}
	if _, _, code, err := s.Client.Exec(ctx, r.ID, []string{"/bin/sh", "-c", "mkdir -p " + sandboxSrc + " && tar -xf " + sandboxWorkspace + "/src.tar -C " + sandboxSrc}, nil, "/"); err != nil || code != 0 {
		return run.Artifacts{}, fmt.Errorf("runner: unpack source: %w", err)
	}

	env := sandboxEnv(r, cfg)
	modelDir := filepath.Join(sandboxModels, cfg.Model)

	// 3. Model identity before invocation.
	pin := ""
	if s.ModelPins != nil {
		pins := s.ModelPins(r.Target)
		pin = pins[cfg.Model]
	}
	pre, err := s.modelDigest(ctx, r.ID, modelDir)
	if err != nil {
		return run.Artifacts{}, err
	}
	if pin != "" && pre.TreeDigest != pin {
		return run.Artifacts{}, fmt.Errorf("runner: model digest %s does not match the pinned digest %s", pre.TreeDigest, pin)
	}

	// 4. Runtime start (optional) + readiness.
	if cfg.Runtime.Start != nil {
		handle, err := s.Client.Start(ctx, r.ID, cfg.Runtime.Start.Argv, env, sandboxSrc)
		if err != nil {
			return run.Artifacts{}, fmt.Errorf("runner: start runtime: %w", err)
		}
		_ = handle
		if err := s.waitReady(ctx, r, cfg); err != nil {
			return run.Artifacts{}, err
		}
	}

	// 5. Invoke.
	_, stderr, code, err := s.Client.Exec(ctx, r.ID, cfg.Invoke.Argv, env, sandboxSrc)
	if err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: invoke transport: %v: %s", err, truncate(string(stderr), 512))
	}
	if code != 0 {
		return run.Artifacts{}, fmt.Errorf("runner: invoke failed with exit %d: %s", code, truncate(string(stderr), 512))
	}

	// 6. Model identity after invocation: must not have changed.
	post, err := s.modelDigest(ctx, r.ID, modelDir)
	if err != nil {
		return run.Artifacts{}, err
	}
	if post.TreeDigest != pre.TreeDigest {
		return run.Artifacts{}, fmt.Errorf("runner: model changed during execution (%s -> %s)", pre.TreeDigest, post.TreeDigest)
	}

	// 7. Persist artifacts outside the sandbox and hash them at rest.
	if err := os.MkdirAll(filepath.Join(r.Artifacts.Dir, "output"), 0o755); err != nil {
		return run.Artifacts{}, err
	}
	index, err := s.Client.Pull(ctx, r.ID, sandboxOutput+"/index.html")
	if err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: pull index.html: %w", err)
	}
	if err := os.WriteFile(filepath.Join(r.Artifacts.Dir, "output", "index.html"), index, 0o644); err != nil {
		return run.Artifacts{}, err
	}
	logData, err := s.Client.Pull(ctx, r.ID, sandboxLog)
	if err != nil {
		logData = nil // a run without a log still produced its artifact
	}
	logPath := filepath.Join(r.Artifacts.Dir, "invoke.log")
	if err := os.WriteFile(logPath, logData, 0o644); err != nil {
		return run.Artifacts{}, err
	}
	identityPath := filepath.Join(r.Artifacts.Dir, "model-identity.json")
	identityJSON, err := json.MarshalIndent(pre, "", "  ")
	if err != nil {
		return run.Artifacts{}, err
	}
	if err := os.WriteFile(identityPath, identityJSON, 0o644); err != nil {
		return run.Artifacts{}, err
	}

	return run.Artifacts{
		Dir:               r.Artifacts.Dir,
		IndexSHA256:       sha256Hex(index),
		LogSHA256:         sha256Hex(logData),
		ModelTreeDigest:   pre.TreeDigest,
		ModelIdentityPath: identityPath,
	}, nil
}

// modelDigest computes the model tree digest inside the sandbox with the
// shared manifest script.
func (s *Sandbox) modelDigest(ctx context.Context, runID, modelDir string) (provenance.ModelIdentity, error) {
	stdout, stderr, _, err := s.Client.Exec(ctx, runID, []string{"python3", "-c", provenance.HashTreeScript(modelDir)}, nil, "/")
	if err != nil {
		return provenance.ModelIdentity{}, fmt.Errorf("runner: model identity: %v: %s", err, truncate(string(stderr), 256))
	}
	id, err := provenance.ParseModelIdentity(stdout)
	if err != nil {
		return provenance.ModelIdentity{}, fmt.Errorf("runner: model identity: %w: %s", err, truncate(string(stderr), 256))
	}
	return id, nil
}

// waitReady polls ReadyArgv until it exits zero or the ready timeout
// elapses. The timeout is the recipe request clamped by the operator limit.
func (s *Sandbox) waitReady(ctx context.Context, r run.Run, cfg experiment.Config) error {
	start := cfg.Runtime.Start
	readyLimit := time.Duration(start.ReadyTimeout()) * time.Second
	if s.Limits != nil {
		if readyMax, _, ok := s.Limits(r.Target); ok && readyMax > 0 && readyLimit > readyMax {
			readyLimit = readyMax
		}
	}
	deadline := time.Now().Add(readyLimit)
	for {
		_, stderr, code, err := s.Client.Exec(ctx, r.ID, start.ReadyArgv, nil, sandboxSrc)
		if err == nil && code == 0 {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("runner: readiness interrupted: %w", ctx.Err())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("runner: runtime not ready within %s (last error: %v: %s)", readyLimit, err, truncate(string(stderr), 256))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// sandboxEnv builds the LLMBENCH_* environment for sandbox commands.
func sandboxEnv(r run.Run, cfg experiment.Config) map[string]string {
	return map[string]string{
		"LLMBENCH_RUN_ID":       r.ID,
		"LLMBENCH_PROMPT_PATH":  sandboxSrc + "/input/prompt.md",
		"LLMBENCH_OUTPUT_DIR":   sandboxOutput,
		"LLMBENCH_MODEL_ID":     cfg.Model,
		"LLMBENCH_MODEL_PATH":   filepath.Join(sandboxModels, cfg.Model),
		"LLMBENCH_CONTEXT_SIZE": fmt.Sprintf("%d", cfg.Runtime.ContextSize),
	}
}

// ClaimHook acquires and releases the run's SandboxClaim. It is a run.Hook,
// so the engine's reverse release order guarantees the claim is deleted
// before any restore hook runs.
type ClaimHook struct {
	RunID    string
	WarmPool string
	Client   SandboxClient
}

// Name implements run.Hook.
func (h *ClaimHook) Name() string { return "sandbox-claim" }

// Acquire implements run.Hook.
func (h *ClaimHook) Acquire(ctx context.Context) error {
	return h.Client.EnsureSandboxClaim(ctx, h.RunID, h.WarmPool)
}

// Release implements run.Hook. The runtime is stopped best-effort; the claim
// release error decides the hook result because the claim must not outlive
// the release phase.
func (h *ClaimHook) Release(ctx context.Context) error {
	stopErr := h.Client.Stop(ctx, h.RunID, runtimePIDFileOf(h.RunID))
	if err := h.Client.ReleaseSandboxClaim(ctx, h.RunID); err != nil {
		return err
	}
	_ = stopErr // the claim is gone; a leaked process dies with the sandbox pod
	return nil
}

// runtimePIDFileOf mirrors the in-sandbox pid file path used by the sandbox
// adapter. It is derived from the run ID deterministically.
func runtimePIDFileOf(runID string) string { return "/tmp/llmbench-runtime-" + runID + ".pid" }

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
