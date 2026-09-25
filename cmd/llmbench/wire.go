package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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

// httpBadRequest marks caller mistakes so the HTTP layer can answer 400.
type httpBadRequest struct{ err error }

func (e *httpBadRequest) Error() string { return e.err.Error() }
func (e *httpBadRequest) Unwrap() error { return e.err }

// prepareRun validates an experiment request and freezes everything the run
// needs: recipe snapshot, prompt hash, fingerprint, hook plan and inputs.
// Both the submit CLI and the HTTP API go through this function.
func prepareRun(g *globalFlags, opCfg operator.Config, expPath, commit string) (run.Run, map[string][]byte, error) {
	cfg, err := experiment.Load(expPath)
	if err != nil {
		return run.Run{}, nil, err
	}
	if err := experiment.Validate(cfg, g.root); err != nil {
		return run.Run{}, nil, &httpBadRequest{fmt.Errorf("%s: %w", expPath, err)}
	}
	ready := 0
	if cfg.Runtime.Start != nil {
		ready = cfg.Runtime.Start.ReadyTimeout()
	}
	if err := opCfg.ValidateRecipe(cfg.Target, ready); err != nil {
		return run.Run{}, nil, &httpBadRequest{err}
	}
	target, ok := opCfg.Targets[cfg.Target]
	if !ok {
		return run.Run{}, nil, &httpBadRequest{fmt.Errorf("submit: target %q is not allowlisted", cfg.Target)}
	}
	if target.Sandbox != nil && len(commit) != 40 {
		return run.Run{}, nil, &httpBadRequest{fmt.Errorf("submit: sandbox target %q requires a full 40-hex input commit", cfg.Target)}
	}

	promptBytes, err := os.ReadFile(experiment.BenchmarkPath(cfg, g.root))
	if err != nil {
		return run.Run{}, nil, &httpBadRequest{err}
	}
	rawBytes, err := os.ReadFile(expPath)
	if err != nil {
		return run.Run{}, nil, &httpBadRequest{err}
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
		Experiment:          filepath.ToSlash(expPath),
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
// not wired yet (sandbox claims arrive in milestone 3, GitOps in milestone 4).
type planHookSource struct {
	dir string // working directory for command hooks (repository root)
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
			return nil, fmt.Errorf("hook %q: sandbox claim hooks arrive with milestone 3; do not select this target yet", p.Name)
		default:
			return nil, fmt.Errorf("hook %q: unknown plan kind %q", p.Name, p.Kind)
		}
	}
	return hooks, nil
}

// buildEngine wires the engine for the current configuration.
func buildEngine(g *globalFlags, cfg operator.Config, interval time.Duration) *run.Engine {
	store := mustFileStore(g)
	return &run.Engine{
		Store:     store,
		Leases:    store,
		Snapshots: store,
		Hooks:     &planHookSource{dir: g.root},
		Executor:  runner.NewLocal(g.root),
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
