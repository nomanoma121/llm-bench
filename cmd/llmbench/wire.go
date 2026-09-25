package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"

	"github.com/nomanoma121/llm-bench/internal/filestore"
	"github.com/nomanoma121/llm-bench/internal/hook"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/run"
	"github.com/nomanoma121/llm-bench/internal/runner"
)

// newFileStore constructs the file-backed persistence layer. Artifacts live
// under <output>/runs/<run-id>/.
func newFileStore(g *globalFlags) (*filestore.Store, error) {
	return filestore.New(g.state, filepath.Join(g.output, "runs"))
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

// buildEngine wires the engine for the current configuration. The interval is
// short because the submit CLI drives runs synchronously; serve constructs a
// second engine with the operator retry interval in milestone 2.
func buildEngine(g *globalFlags, cfg operator.Config) *run.Engine {
	store := mustFileStore(g)
	return &run.Engine{
		Store:     store,
		Leases:    store,
		Snapshots: store,
		Hooks:     &planHookSource{dir: g.root},
		Executor:  runner.NewLocal(g.root),
		Finalizer: nil, // publication arrives with milestone 5; nil short-circuits finalizing
		Log:       logr.Discard(),
		Interval:  time.Second,
		MaxExecutionDuration: func(target string) time.Duration {
			t, ok := cfg.Targets[target]
			if !ok {
				return 0
			}
			return cfg.EffectiveLimits(t).MaxExecutionDuration
		},
	}
}

func mustFileStore(g *globalFlags) *filestore.Store {
	s, err := newFileStore(g)
	if err != nil {
		panic(err) // construction only fails on unusable state/output dirs
	}
	return s
}
