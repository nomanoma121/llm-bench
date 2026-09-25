package main

import (
	"context"
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
	"github.com/nomanoma121/llm-bench/internal/gitops"
	"github.com/nomanoma121/llm-bench/internal/hook"
	"github.com/nomanoma121/llm-bench/internal/httpapi"
	"github.com/nomanoma121/llm-bench/internal/kube"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/pages"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
	"github.com/nomanoma121/llm-bench/internal/runner"
	"github.com/nomanoma121/llm-bench/internal/sandbox"
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
// the plan digest on every call.
type planHookSource struct {
	dir string // working directory for command hooks (repository root)
	// sandboxFor builds (and caches) the sandbox client for a target.
	sandboxFor func(target string) SandboxHookDeps
	// gitopsFor builds (and caches) the GitHub and kube gateways for a target.
	gitopsFor func(target string) GitOpsHookDeps
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
			if s.gitopsFor == nil {
				return nil, fmt.Errorf("hook %q: no gitops gateways configured", p.Name)
			}
			deps := s.gitopsFor(r.Target)
			if deps.GitHub == nil || deps.Kube == nil {
				return nil, fmt.Errorf("hook %q: gitops gateways unavailable", p.Name)
			}
			hooks = append(hooks, &gitops.PauseRestore{
				Plan: *p.GitOps,
				GH:   deps.GitHub,
				Kube: deps.Kube,
			})
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

// engineStores lets serve swap run records and leases for the Kubernetes
// implementations while recipe snapshots and artifacts stay on the file
// store (the harness persistent volume).
type engineStores struct {
	Runs      run.RunStore
	Leases    run.LeaseStore
	Snapshots run.InputSnapshotter
}

// buildEngine wires the engine with the default file-backed stores.
func buildEngine(g *globalFlags, cfg operator.Config, interval time.Duration,
	sandboxFor func(target string) SandboxHookDeps,
	gitopsFor func(target string) GitOpsHookDeps,
) *run.Engine {
	store := mustFileStore(g)
	return buildEngineWithStores(g, cfg, interval, sandboxFor, gitopsFor, engineStores{
		Runs: store, Leases: store, Snapshots: store,
	})
}

// buildEngineWithStores wires the engine for the given stores.
func buildEngineWithStores(g *globalFlags, cfg operator.Config, interval time.Duration,
	sandboxFor func(target string) SandboxHookDeps,
	gitopsFor func(target string) GitOpsHookDeps,
	stores engineStores,
) *run.Engine {

	// Publication (site) wiring: only when the operator configured a site.
	var finalizer run.Finalizer
	if cfg.Site != nil {
		publisher, err := pages.New(cfg.Site.Owner, cfg.Site.Repository, cfg.Site.Branch, cfg.Site.BaseURL, cfg.Site.ExtraFiles)
		if err != nil {
			panic(fmt.Errorf("wire: pages publisher: %w", err)) // 設定不備は起動時にも分かる
		}
		finalizer = &runner.PublishFinalizer{Publisher: publisher}
	}

	return &run.Engine{
		Store:     stores.Runs,
		Leases:    stores.Leases,
		Snapshots: stores.Snapshots,
		Hooks:     &planHookSource{dir: g.root, sandboxFor: sandboxFor, gitopsFor: gitopsFor},
		Executor:  executorRouter{g: g, cfg: cfg, sandboxFor: sandboxFor},
		Finalizer: finalizer,
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
// LLMBENCH_LOG_LEVEL (higher values reduce output).
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

// sandboxDepsFunc lazily creates one sandbox client per target.
func sandboxDepsFunc(g *globalFlags, cfg operator.Config) func(target string) SandboxHookDeps {
	clients := map[string]runner.SandboxClient{}
	return func(target string) SandboxHookDeps {
		t, ok := cfg.Targets[target]
		if !ok || t.Sandbox == nil {
			return SandboxHookDeps{}
		}
		if c, ok := clients[target]; ok {
			return SandboxHookDeps{Client: c}
		}
		c := &sandbox.Client{Namespace: t.Sandbox.Namespace, Kubeconfig: g.kubeconfig}
		clients[target] = c
		return SandboxHookDeps{Client: c}
	}
}

// GitOpsHookDeps carries what a gitops hook needs at build time. The plan is
// taken from the run's frozen hook plan; only the gateways are injected.
type GitOpsHookDeps struct {
	GitHub gitops.GitHubAPI
	Kube   gitops.Kube
}

// gitopsDepsFunc lazily creates the GitHub and kube gateways per target.
func gitopsDepsFunc(g *globalFlags, cfg operator.Config) func(target string) GitOpsHookDeps {
	deps := map[string]GitOpsHookDeps{}
	return func(target string) GitOpsHookDeps {
		t, ok := cfg.Targets[target]
		if !ok || t.GitOps == nil {
			return GitOpsHookDeps{}
		}
		if d, ok := deps[target]; ok {
			return d
		}
		gh, err := gitops.NewGitHubAPIFromEnv(context.Background(), t.GitOps.Owner, t.GitOps.Repository, t.GitOps.BaseBranch)
		if err != nil {
			return GitOpsHookDeps{}
		}
		checker, kubeErr := kube.NewChecker(g.kubeconfig)
		if kubeErr != nil {
			return GitOpsHookDeps{}
		}
		d := GitOpsHookDeps{GitHub: gh, Kube: checker}
		deps[target] = d
		return d
	}
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
			Client:    deps.Client,
			Git:       provenance.Git{Root: r.g.root},
			Root:      r.g.root,
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
