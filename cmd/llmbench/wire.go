package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/filestore"
	"github.com/nomanoma121/llm-bench/internal/hook"
	"github.com/nomanoma121/llm-bench/internal/httpapi"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
	"github.com/nomanoma121/llm-bench/internal/runner"
)

const (
	// benchSchemaVersion participates in the benchmark fingerprint. Bump it
	// when the benchmark contract changes in a way that breaks A/B
	// comparability.
	benchSchemaVersion = "1"
	// controllerVersion is stamped into fingerprints and run records.
	controllerVersion = "0.1.0-dev"
)

// newFileStore constructs the file-backed persistence layer. Artifacts live
// under <output>/runs/<run-id>/.
func newFileStore(g *globalFlags) (*filestore.Store, error) {
	return filestore.New(g.state, filepath.Join(g.output, "runs"))
}

// mapCallerError classifies recipe/file mistakes as 400 (BadRequestError)
// while leaving I/O and internal failures for the handler to answer 500.
func mapCallerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, experiment.ErrInvalid) {
		return &httpapi.BadRequestError{Err: err}
	}
	return err
}

// resolveExperimentPath turns a caller-supplied experiment path into an
// absolute path confined to the repository root plus the repository-relative
// path recorded on the run. Absolute paths outside the repository and paths
// escaping it with ".." are rejected.
func resolveExperimentPath(root, p string) (full, rel string, err error) {
	if strings.TrimSpace(p) == "" {
		return "", "", errors.New("experiment is required")
	}
	if filepath.IsAbs(p) {
		abs := filepath.Clean(filepath.FromSlash(p))
		r, err := filepath.Rel(root, abs)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", "", fmt.Errorf("experiment path is outside the repository: %q", p)
		}
		return abs, filepath.ToSlash(r), nil
	}
	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("experiment path escapes the repository: %q", p)
	}
	return filepath.Join(root, clean), filepath.ToSlash(clean), nil
}

// prepareRun validates an experiment request and freezes everything the run
// needs: recipe snapshot, prompt hash, fingerprint, hook plan and inputs.
// Both the submit CLI and the HTTP API go through this function; the path is
// resolved inside the repository root.
func prepareRun(g *globalFlags, opCfg operator.Config, expPath, commit string) (run.Run, map[string][]byte, error) {
	fullPath, relPath, err := resolveExperimentPath(g.root, expPath)
	if err != nil {
		return run.Run{}, nil, &httpapi.BadRequestError{Err: err}
	}
	cfg, err := experiment.Load(fullPath)
	if err != nil {
		return run.Run{}, nil, mapCallerError(err)
	}
	if err := experiment.Validate(cfg, g.root); err != nil {
		return run.Run{}, nil, mapCallerError(fmt.Errorf("%s: %w", relPath, err))
	}
	ready := 0
	if cfg.Runtime.Start != nil {
		ready = cfg.Runtime.Start.ReadyTimeout()
	}
	if err := opCfg.ValidateRecipe(cfg.Target, ready); err != nil {
		return run.Run{}, nil, &httpapi.BadRequestError{Err: err}
	}
	target, ok := opCfg.Targets[cfg.Target]
	if !ok {
		return run.Run{}, nil, &httpapi.BadRequestError{Err: fmt.Errorf("submit: target %q is not allowlisted", cfg.Target)}
	}
	if target.GitOps != nil || target.Sandbox != nil {
		// The implementations of these hook kinds are not wired in this
		// build; refusing before the run record exists keeps the target lease
		// from being held by a run that can never progress.
		return run.Run{}, nil, &httpapi.BadRequestError{Err: fmt.Errorf("submit: target %q requires an integration that is not available in this build", cfg.Target)}
	}
	if target.Sandbox != nil && len(commit) != 40 {
		return run.Run{}, nil, &httpapi.BadRequestError{Err: fmt.Errorf("submit: sandbox target %q requires a full 40-hex input commit", cfg.Target)}
	}

	promptBytes, err := os.ReadFile(experiment.BenchmarkPath(cfg, g.root))
	if err != nil {
		return run.Run{}, nil, mapCallerError(err)
	}
	rawBytes, err := os.ReadFile(fullPath)
	if err != nil {
		return run.Run{}, nil, mapCallerError(err)
	}
	recipeJSON, err := experiment.CanonicalJSON(cfg)
	if err != nil {
		return run.Run{}, nil, err
	}

	runID, err := newRunID()
	if err != nil {
		return run.Run{}, nil, err
	}
	plan := operator.BuildHookPlan(target, runID)
	digest, err := operator.PlanDigest(plan)
	if err != nil {
		return run.Run{}, nil, err
	}
	store, err := newFileStore(g)
	if err != nil {
		return run.Run{}, nil, err
	}
	now := currentTime()
	fingerprint, err := provenance.Fingerprint(provenance.FingerprintInput{
		Prompt:                 promptBytes,
		BenchmarkSchemaVersion: benchSchemaVersion,
		ContextSize:            cfg.Runtime.ContextSize,
		RuntimeSignature:       cfg.Runtime.Engine + "/" + cfg.Runtime.Variant,
		TargetKind:             targetKind(target),
		ControllerVersion:      controllerVersion,
	})
	if err != nil {
		return run.Run{}, nil, err
	}

	r := run.Run{
		ID:                  runID,
		Target:              cfg.Target,
		Experiment:          relPath,
		InputCommit:         commit,
		Fingerprint:         fingerprint,
		RecipeSchemaVersion: 1,
		RecipeJSON:          string(recipeJSON),
		PromptSHA256:        provenance.SHA256Hex(promptBytes),
		HookPlan:            plan,
		HookPlanDigest:      digest,
		Artifacts:           run.Artifacts{Dir: store.ArtifactsDir(runID)},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	inputs := map[string][]byte{
		"config.yaml": rawBytes,
		"prompt.md":   promptBytes,
	}
	return r, inputs, nil
}

// planHookSource rebuilds hooks from a run's frozen hook plan. It verifies
// the plan digest on every call and refuses kinds whose implementations are
// not wired yet (GitOps arrives in a later change).
type planHookSource struct {
	dir string // working directory for command hooks (repository root)
	// sandboxClients builds (and caches) the sandbox client for a target.
	sandboxFor func(target string) SandboxHookDeps
}

// HooksFor implements run.HookSource.
func (s *planHookSource) HooksFor(r run.Run) ([]run.Hook, error) {
	digest, err := operator.PlanDigest(r.HookPlan)
	if err != nil {
		return nil, fmt.Errorf("hook plan digest: %w", err)
	}
	if digest != r.HookPlanDigest {
		return nil, fmt.Errorf("hook plan digest mismatch: recorded %q, computed %q", r.HookPlanDigest, digest)
	}
	hooks := make([]run.Hook, 0, len(r.HookPlan))
	for _, p := range r.HookPlan {
		switch p.Kind {
		case operator.KindCommand:
			h, err := hook.FromPlan(p, s.dir)
			if err != nil {
				return nil, err
			}
			hooks = append(hooks, h)
		case operator.KindGitOps:
			return nil, fmt.Errorf("hook %q: gitops hooks arrive with milestone 4; do not select this target yet", p.Name)
		case operator.KindSandboxClaim:
			if s.sandboxFor == nil {
				return nil, fmt.Errorf("hook %q: no sandbox client configured", p.Name)
			}
			deps := s.sandboxFor(r.Target)
			hooks = append(hooks, &runner.ClaimHook{
				RunID:    r.ID,
				WarmPool: p.Sandbox.WarmPool,
				Client:   deps.Client,
			})
		default:
			return nil, fmt.Errorf("hook %q: unknown plan kind %q", p.Name, p.Kind)
		}
	}
	return hooks, nil
}

// buildEngine wires the engine for the current configuration.
func buildEngine(g *globalFlags, cfg operator.Config, interval time.Duration, sandboxFor func(target string) SandboxHookDeps) *run.Engine {
	store := mustFileStore(g)
	return &run.Engine{
		Store:     store,
		Leases:    store,
		Snapshots: store,
		Hooks:     &planHookSource{dir: g.root, sandboxFor: sandboxFor},
		Executor:  executorRouter{g: g, cfg: cfg, sandboxFor: sandboxFor},
		Finalizer: nil, // publication arrives with milestone 5; nil short-circuits finalizing
		Log:       controllerLogger(),
		Interval:  interval,
		MaxExecutionDuration: func(target string) time.Duration {
			t, ok := cfg.Targets[target]
			if !ok {
				return 0
			}
			return cfg.EffectiveLimits(t).MaxExecutionDuration
		},
	}
}

func currentTime() time.Time { return time.Now() }

// controllerLogger returns a stderr logger. The verbosity is controlled with
// LLMBENCH_LOG_LEVEL (0 = info-equivalent warnings, higher = more detail).
func controllerLogger() logr.Logger {
	return stdr.New(log.New(os.Stderr, "", log.LstdFlags)).V(logLevel())
}

func logLevel() int {
	if v := os.Getenv("LLMBENCH_LOG_LEVEL"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return -n
		}
	}
	return 0
}

func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("submit: generate run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func targetKind(t operator.Target) string {
	if t.Sandbox != nil {
		return "sandbox"
	}
	return "local"
}

// SandboxHookDeps carries what a sandbox claim hook needs at build time.
type SandboxHookDeps struct {
	Client runner.SandboxClient
}

// executorRouter picks the local or sandbox executor per target.
type executorRouter struct {
	g          *globalFlags
	cfg        operator.Config
	sandboxFor func(target string) SandboxHookDeps
}

// Execute implements run.Executor.
func (r executorRouter) Execute(ctx context.Context, rec run.Run) (run.Artifacts, error) {
	target, ok := r.cfg.Targets[rec.Target]
	if ok && target.Sandbox != nil {
		deps := r.sandboxFor(rec.Target)
		exec := &runner.Sandbox{
			Client: deps.Client,
			Git:    provenance.Git{Root: r.g.root},
			Root:   r.g.root,
			// Operator limits are injected here so recipe readiness timeouts
			// can never exceed the operator ceiling (F17).
			Limits: func(target string) (time.Duration, time.Duration, bool) {
				t, ok := r.cfg.Targets[target]
				if !ok {
					return 0, 0, false
				}
				lim := r.cfg.EffectiveLimits(t)
				return lim.MaxReadyDuration, lim.MaxExecutionDuration, true
			},
			ModelPins: func(target string) map[string]string { return r.cfg.Targets[target].Sandbox.ModelSHA256 },
		}
		return exec.Execute(ctx, rec)
	}
	return runner.NewLocal(r.g.root).Execute(ctx, rec)
}

func mustFileStore(g *globalFlags) *filestore.Store {
	s, err := newFileStore(g)
	if err != nil {
		panic(err) // construction only fails on unusable state/output dirs
	}
	return s
}

// statusJSON renders the API/CLI status projection.
func statusJSON(r run.Run) ([]byte, error) {
	return json.MarshalIndent(httpapi.NewStatusView(r), "", "  ")
}
