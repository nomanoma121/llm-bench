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
	"strings"
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
	// EnsureSandboxClaim creates the claim if absent. ready=false means the
	// claim exists but is not usable yet.
	EnsureSandboxClaim(ctx context.Context, claimName, warmPool string) (ready bool, err error)
	// Start launches the runtime detached; the handle is an opaque string.
	// Starting while a previous runtime is alive is a no-op.
	Start(ctx context.Context, runID, claimName string, argv []string, env map[string]string, cwd string) (handle string, err error)
	// Stop terminates the recorded runtime. Idempotent.
	Stop(ctx context.Context, claimName, handle string) error
	Exec(ctx context.Context, claimName string, argv []string, env map[string]string, cwd string) (stdout, stderr []byte, exitCode int, err error)
	Put(ctx context.Context, claimName string, r io.Reader, dest string) error
	Pull(ctx context.Context, claimName, path string) ([]byte, error)
	// ReleaseSandboxClaim waits until the claim and its GPU are gone.
	// released=false means it is still terminating.
	ReleaseSandboxClaim(ctx context.Context, claimName string) (released bool, err error)
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
	// Claim is the deterministic claim name from the run's frozen hook plan.
	Claim string
	// Limits resolves the operator ceilings for a target (readiness and
	// execution) so recipe timeouts can never exceed them.
	Limits func(target string) (readyLimit, execLimit time.Duration, ok bool)
	// ModelPins returns operator-pinned model tree digests per target.
	ModelPins func(target string) map[string]string
}

// In-sandbox constants. They match the chart's documented dev image layout.
const (
	sandboxWorkspace = "/workspace"
	sandboxSrc       = "/workspace/src"
	sandboxInput     = "/workspace/input"
	sandboxOutput    = "/workspace/output"
	sandboxModels    = "/models"
	sandboxTar       = "/workspace/src.tar"
)

// Execute implements run.Executor. The caller (engine) has already persisted
// ExecutionState=invoking and applied the execution time limit on ctx.
func (s *Sandbox) Execute(ctx context.Context, r run.Run) (run.Artifacts, error) {
	if r.InputCommit == "" {
		return run.Artifacts{}, errors.New("runner: sandbox execution requires an input commit")
	}
	if s.Claim == "" {
		return run.Artifacts{}, errors.New("runner: sandbox execution requires the claim name from the run's hook plan")
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

	// 2. Prepare the workspace and upload the source snapshot. Every command
	// is a plain argv: the runner never builds shell programs (quoting and the
	// shell boundary live in the sandbox adapter).
	if _, stderr, code, err := s.Client.Exec(ctx, s.Claim, []string{"mkdir", "-p", sandboxSrc, sandboxInput, sandboxOutput}, nil, "/"); err != nil || code != 0 {
		return run.Artifacts{}, fmt.Errorf("runner: prepare workspace: %v exit=%d: %s", err, code, truncate(string(stderr), 256))
	}
	tar, err := s.Git.Archive(ctx, r.InputCommit)
	if err != nil {
		return run.Artifacts{}, err
	}
	if err := s.Client.Put(ctx, s.Claim, bytes.NewReader(tar), sandboxTar); err != nil {
		return run.Artifacts{}, err
	}
	if _, stderr, code, err := s.Client.Exec(ctx, s.Claim, []string{"tar", "-xf", sandboxTar, "-C", sandboxSrc}, nil, "/"); err != nil || code != 0 {
		return run.Artifacts{}, fmt.Errorf("runner: unpack source: %v exit=%d: %s", err, code, truncate(string(stderr), 256))
	}
	// The frozen snapshot is the contract for prompt and recipe: upload it so
	// the environment variables point at existing files.
	if err := s.Client.Put(ctx, s.Claim, bytes.NewReader(inputPrompt), sandboxInput+"/prompt.md"); err != nil {
		return run.Artifacts{}, err
	}
	if err := s.Client.Put(ctx, s.Claim, bytes.NewReader(inputConfig), sandboxInput+"/config.yaml"); err != nil {
		return run.Artifacts{}, err
	}

	env := sandboxEnv(r, cfg)
	modelDir := filepath.Join(sandboxModels, cfg.Model)

	// 3. Model identity before invocation.
	pin := ""
	if s.ModelPins != nil {
		pins := s.ModelPins(r.Target)
		pin = pins[cfg.Model]
	}
	pre, err := s.modelDigest(ctx, modelDir)
	if err != nil {
		return run.Artifacts{}, err
	}
	if pin != "" && pre.TreeDigest != pin {
		return run.Artifacts{}, fmt.Errorf("runner: model digest %s does not match the pinned digest %s", pre.TreeDigest, pin)
	}

	// 4. Runtime start (optional) + readiness.
	if cfg.Runtime.Start != nil {
		if _, err := s.Client.Start(ctx, r.ID, s.Claim, cfg.Runtime.Start.Argv, env, sandboxSrc); err != nil {
			return run.Artifacts{}, fmt.Errorf("runner: start runtime: %w", err)
		}
		if err := s.waitReady(ctx, r, cfg); err != nil {
			return run.Artifacts{}, err
		}
	}

	// 5. Invoke. The log is persisted on the harness side because the sandbox
	// (and its filesystem) is removed at release time.
	stdout, stderr, code, execErr := s.Client.Exec(ctx, s.Claim, cfg.Invoke.Argv, env, sandboxSrc)
	logBody := formatInvokeLog(cfg.Invoke.Argv, stdout, stderr, code, execErr)
	logPath := filepath.Join(r.Artifacts.Dir, "invoke.log")
	if err := os.WriteFile(logPath, logBody, 0o644); err != nil {
		return run.Artifacts{}, err
	}
	if execErr != nil {
		return run.Artifacts{}, fmt.Errorf("runner: invoke transport: %v: %s", execErr, truncate(string(stderr), 512))
	}
	if code != 0 {
		return run.Artifacts{}, fmt.Errorf("runner: invoke failed with exit %d: %s", code, truncate(string(stderr), 512))
	}

	// 6. Model identity after invocation: must not have changed.
	post, err := s.modelDigest(ctx, modelDir)
	if err != nil {
		return run.Artifacts{}, err
	}
	if post.TreeDigest != pre.TreeDigest {
		return run.Artifacts{}, fmt.Errorf("runner: model changed during execution (%s -> %s)", pre.TreeDigest, post.TreeDigest)
	}

	// 7. Persist artifacts outside the sandbox. The payload is defined as the
	// single file index.html, so a benchmark that produced anything else in
	// /workspace/output fails here instead of silently losing those files.
	if _, stderr, code, err := s.Client.Exec(ctx, s.Claim, []string{"python3", "-c", verifySingleOutputScript(sandboxOutput)}, nil, "/"); err != nil || code != 0 {
		return run.Artifacts{}, fmt.Errorf("runner: verify sandbox output: %v exit=%d: %s", err, code, truncate(string(stderr), 256))
	}
	staging := stagingOutput(r.Artifacts.Dir)
	if err := os.RemoveAll(staging); err != nil {
		return run.Artifacts{}, err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return run.Artifacts{}, err
	}
	index, err := s.Client.Pull(ctx, s.Claim, sandboxOutput+"/index.html")
	if err != nil {
		return run.Artifacts{}, fmt.Errorf("runner: pull index.html: %w", err)
	}
	if len(index) > maxArtifactBytes {
		return run.Artifacts{}, fmt.Errorf("runner: artifact is %d bytes, over the %d byte limit", len(index), maxArtifactBytes)
	}
	if err := os.WriteFile(filepath.Join(staging, "index.html"), index, 0o644); err != nil {
		return run.Artifacts{}, err
	}
	digest, payload, err := sealOutput(r.Artifacts.Dir, staging)
	if err != nil {
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
		IndexSHA256:       payload[0].SHA256,
		LogSHA256:         sha256Hex(logBody),
		ModelTreeDigest:   pre.TreeDigest,
		ModelIdentityPath: identityPath,
		ArtifactDigest:    digest,
	}, nil
}

// verifySingleOutputScript returns the python3 program that fails unless the
// sandbox output directory contains exactly index.html.
func verifySingleOutputScript(root string) string {
	return fmt.Sprintf(`import os, stat, sys
root = %q
limit = %d
bad = []
count = 0
for dirpath, dirnames, filenames in os.walk(root):
    for name in filenames:
        path = os.path.join(dirpath, name)
        rel = os.path.relpath(path, root)
        if rel != "index.html":
            bad.append(rel)
            continue
        # The sealed payload is a single regular file: a symlink or any other
        # special would mean local and sandbox disagree about what was hashed.
        st = os.lstat(path)
        if not stat.S_ISREG(st.st_mode):
            bad.append(rel + " (not a regular file)")
            continue
        if st.st_size > limit:
            bad.append(rel + " (too large)")
            continue
        count += 1
if bad:
    sys.stderr.write("unexpected output files: " + ",".join(sorted(bad)))
    sys.exit(3)
if count != 1:
    sys.stderr.write("output/index.html is missing")
    sys.exit(3)
`, root, maxArtifactBytes)
}

// maxArtifactBytes bounds a single recovered artifact file. It matches the
// preview's serving limit so local and sandbox seal the same thing.
const maxArtifactBytes = 8 << 20

// formatInvokeLog records the invoked argv, captured output and exit status.
func formatInvokeLog(argv []string, stdout, stderr []byte, code int, execErr error) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "$ %s\n", strings.Join(argv, " "))
	b.Write(stdout)
	if len(stdout) > 0 && stdout[len(stdout)-1] != '\n' {
		b.WriteString("\n")
	}
	if len(stderr) > 0 {
		b.WriteString("--- stderr ---\n")
		b.Write(stderr)
		if stderr[len(stderr)-1] != '\n' {
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "[exit: %d", code)
	if execErr != nil {
		fmt.Fprintf(&b, " transport error: %v", execErr)
	}
	b.WriteString("]\n")
	return b.Bytes()
}

// modelDigest computes the model tree digest inside the sandbox with the
// shared manifest script.
func (s *Sandbox) modelDigest(ctx context.Context, modelDir string) (provenance.ModelIdentity, error) {
	stdout, stderr, code, err := s.Client.Exec(ctx, s.Claim, []string{"python3", "-c", provenance.HashTreeScript(modelDir)}, nil, "/")
	if err != nil {
		return provenance.ModelIdentity{}, fmt.Errorf("runner: model identity: %v: %s", err, truncate(string(stderr), 256))
	}
	if code != 0 {
		return provenance.ModelIdentity{}, fmt.Errorf("runner: model identity: exit %d: %s", code, truncate(string(stderr), 256))
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
		_, stderr, code, err := s.Client.Exec(ctx, s.Claim, start.ReadyArgv, nil, sandboxSrc)
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
		"LLMBENCH_PROMPT_PATH":  sandboxInput + "/prompt.md",
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
	Claim    string
	WarmPool string
	Client   SandboxClient
}

// Name implements run.Hook.
func (h *ClaimHook) Name() string { return "sandbox-claim" }

// Acquire implements run.Hook.
func (h *ClaimHook) Acquire(ctx context.Context) error {
	ready, err := h.Client.EnsureSandboxClaim(ctx, h.Claim, h.WarmPool)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("sandbox: claim %s is not ready yet: %w", h.Claim, run.ErrPending)
	}
	return nil
}

// Release implements run.Hook. The runtime is stopped best-effort; the claim
// release error decides the hook result because the claim must not outlive
// the release phase.
func (h *ClaimHook) Release(ctx context.Context) error {
	stopErr := h.Client.Stop(ctx, h.Claim, runtimePIDFileOf(h.RunID))
	released, err := h.Client.ReleaseSandboxClaim(ctx, h.Claim)
	if err != nil {
		return err
	}
	if !released {
		return fmt.Errorf("sandbox: claim %s is still terminating: %w", h.Claim, run.ErrPending)
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
