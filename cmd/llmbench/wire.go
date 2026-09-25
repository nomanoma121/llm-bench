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
	"sync"
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
	if target.Sandbox != nil {
		if err := provenance.ValidateCommitSHA(commit); err != nil {
			return run.Run{}, nil, &httpapi.BadRequestError{Err: fmt.Errorf("submit: sandbox target %q: %w", cfg.Target, err)}
		}
	}
	if target.GitOps != nil {
		// The gitops hook needs both gateways; if either cannot even be
		// constructed the run would hold the target lease while its hook can
		// never complete. Validate eagerly, before any record exists.
		if _, _, err := newGitopsGateways(g).gateways(operator.GitOpsPlan{
			Owner: target.GitOps.Owner, Repository: target.GitOps.Repository, BaseBranch: target.GitOps.BaseBranch,
		}); err != nil {
			// A missing token or kubeconfig is a controller/operator
			// configuration problem, not a caller mistake: keep it 500.
			return run.Run{}, nil, fmt.Errorf("submit: target %q: %w", cfg.Target, err)
		}
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
	// sandboxClients resolves the client for the namespace recorded in the
	// run's frozen hook plan.
	sandboxClients *sandboxClients
	// gitopsGateways resolves the GitHub/kube gateways for a run.
	gitopsGateways *gitopsGateways
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
			if s.gitopsGateways == nil || p.GitOps == nil {
				return nil, fmt.Errorf("hook %q: no gitops gateways configured", p.Name)
			}
			gh, checker, err := s.gitopsGateways.gateways(*p.GitOps)
			if err != nil {
				return nil, fmt.Errorf("hook %q: %w", p.Name, err)
			}
			// The frozen plan is the source of truth for the repository,
			// paths, branch names and convergence gates.
			hooks = append(hooks, hook.MapPending(&gitops.PauseRestore{
				Plan: *p.GitOps,
				GH:   gh,
				Kube: checker,
			}, gitops.ErrNotConverged))
		case operator.KindSandboxClaim:
			if s.sandboxClients == nil || p.Sandbox == nil {
				return nil, fmt.Errorf("hook %q: no sandbox client configured", p.Name)
			}
			hooks = append(hooks, &runner.ClaimHook{
				RunID:    r.ID,
				Claim:    p.Sandbox.SandboxClaimName,
				WarmPool: p.Sandbox.WarmPool,
				Client:   s.sandboxClients.client(p.Sandbox.Namespace),
			})
		default:
			return nil, fmt.Errorf("hook %q: unknown plan kind %q", p.Name, p.Kind)
		}
	}
	return hooks, nil
}

// gitopsGateways lazily builds the GitHub and kube gateways for a frozen
// plan. Access is guarded because different targets are dispatched
// concurrently.
type gitopsGateways struct {
	mu       sync.Mutex
	g        *globalFlags
	github   map[string]gitops.GitHubAPI // keyed by owner/repo/baseBranch
	checker  gitops.Kube                 // one cluster, shared
	checkErr error
}

func newGitopsGateways(g *globalFlags) *gitopsGateways {
	return &gitopsGateways{g: g, github: map[string]gitops.GitHubAPI{}}
}

// gateways resolves the GitHub client for the frozen plan's repository. The
// cache key is the repository triple: different targets may pause different
// manifest repositories, and sending one target's PR to another repository
// would be a serious mistake.
func (p *gitopsGateways) gateways(plan operator.GitOpsPlan) (gitops.GitHubAPI, gitops.Kube, error) {
	key := plan.Owner + "/" + plan.Repository + "@" + plan.BaseBranch
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.checkErr != nil {
		return nil, nil, p.checkErr
	}
	gh, ok := p.github[key]
	if !ok {
		client, err := gitops.NewGitHubAPIFromEnv(context.Background(), plan.Owner, plan.Repository, plan.BaseBranch)
		if err != nil {
			return nil, nil, err
		}
		p.github[key] = client
		gh = client
	}
	if p.checker == nil {
		checker, err := kube.NewChecker(p.g.kubeconfig)
		if err != nil {
			p.checkErr = err
			return nil, nil, err
		}
		p.checker = checker
	}
	return gh, p.checker, nil
}

// buildEngine wires the engine for the current configuration. It returns an
// error when the operator's site configuration cannot be used (for example a
// missing GitHub token): failing fast beats publishing runs silently.
func buildEngine(g *globalFlags, cfg operator.Config, interval time.Duration, sandboxes *sandboxClients, gitopsGateways *gitopsGateways) (*run.Engine, error) {
	store := mustFileStore(g)

	// Publication (site) wiring: only when the operator configured a site.
	var finalizer run.Finalizer
	if cfg.Site != nil {
		publisher, err := pages.New(cfg.Site.Owner, cfg.Site.Repository, cfg.Site.Branch, cfg.Site.BaseURL, cfg.Site.ExtraFiles)
		if err != nil {
			return nil, fmt.Errorf("wire: pages publisher: %w", err)
		}
		finalizer = &runner.PublishFinalizer{Publisher: publisher}
	}

	return &run.Engine{
		Store:     store,
		Leases:    store,
		Snapshots: store,
		Hooks:     &planHookSource{dir: g.root, sandboxClients: sandboxes, gitopsGateways: gitopsGateways},
		Executor:  executorRouter{g: g, cfg: cfg, sandboxClients: sandboxes},
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
	}, nil
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

// executorRouter picks the local or sandbox executor. The choice is driven
// by the run's frozen hook plan, never by the current operator config: a
// sandbox run recovered after a configuration change must not silently fall
// back to local execution.
type executorRouter struct {
	g              *globalFlags
	cfg            operator.Config
	sandboxClients *sandboxClients
}

// Execute implements run.Executor.
func (r executorRouter) Execute(ctx context.Context, rec run.Run) (run.Artifacts, error) {
	if plan := sandboxPlanOf(rec.HookPlan); plan != nil {
		if r.sandboxClients == nil {
			return run.Artifacts{}, fmt.Errorf("wire: run %s needs a sandbox executor but none is configured", rec.ID)
		}
		target, ok := r.cfg.Targets[rec.Target]
		if !ok {
			// Fail closed: without the operator target we would run with no
			// execution ceiling, no readiness clamp and no model pin (F17).
			return run.Artifacts{}, fmt.Errorf("wire: run %s targets %q which is no longer configured; refusing to execute without operator limits and model pin", rec.ID, rec.Target)
		}
		if target.Sandbox == nil {
			return run.Artifacts{}, fmt.Errorf("wire: run %s was submitted for a sandbox target but %q is now a local target; refusing to execute", rec.ID, rec.Target)
		}
		exec := &runner.Sandbox{
			Client: r.sandboxClients.client(plan.Namespace),
			Claim:  plan.SandboxClaimName,
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
			ModelPins: func(target string) map[string]string {
				t, ok := r.cfg.Targets[target]
				if !ok || t.Sandbox == nil {
					return nil
				}
				return t.Sandbox.ModelSHA256
			},
		}
		return exec.Execute(ctx, rec)
	}
	if target, ok := r.cfg.Targets[rec.Target]; !ok || target.Sandbox != nil {
		return run.Artifacts{}, fmt.Errorf("wire: run %s is not a local target; refusing to execute it locally", rec.ID)
	}
	return runner.NewLocal(r.g.root).Execute(ctx, rec)
}

// sandboxClients provides sandbox clients keyed by namespace. The key comes
// from the run's frozen hook plan, so an operator configuration change can
// never redirect a recovery to another namespace. Access is guarded because
// different targets are dispatched concurrently.
type sandboxClients struct {
	mu    sync.Mutex
	g     *globalFlags
	cache map[string]runner.SandboxClient
}

func newSandboxClients(g *globalFlags) *sandboxClients {
	return &sandboxClients{g: g, cache: map[string]runner.SandboxClient{}}
}

func (p *sandboxClients) client(namespace string) runner.SandboxClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[namespace]; ok {
		return c
	}
	c := &sandbox.Client{Namespace: namespace, Kubeconfig: p.g.kubeconfig}
	p.cache[namespace] = c
	return c
}

// sandboxPlanOf returns the frozen sandbox plan of a run, if any.
func sandboxPlanOf(plan []operator.PlannedHook) *operator.SandboxPlan {
	for i := range plan {
		if plan[i].Kind == operator.KindSandboxClaim && plan[i].Sandbox != nil {
			return plan[i].Sandbox
		}
	}
	return nil
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
