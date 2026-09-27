package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/nomanoma121/llm-bench/internal/controller"
	"github.com/nomanoma121/llm-bench/internal/githubapp"
	"github.com/nomanoma121/llm-bench/internal/gitops"
	"github.com/nomanoma121/llm-bench/internal/issues"
	"github.com/nomanoma121/llm-bench/internal/kube"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/runtime"
	"github.com/nomanoma121/llm-bench/internal/sandbox"
)

// sandboxTokenEnv is the environment variable the benchmark CLI reads the
// short-lived installation token from. The token is minted per job and never
// written to disk (docs/mvp.md §7).
const sandboxTokenEnv = "LLMBENCH_GIT_TOKEN"

func newControllerCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Run the MVP control loop: poll Issues, borrow the GPU, publish a PR",
		Long: "The controller is a thin loop (docs/mvp.md §6): it lists Issues that carry\n" +
			"an explicit request, takes the global GPU lease (which is also the job\n" +
			"mutex), pauses the inference workload, runs the measurement in a sandbox,\n" +
			"opens the result pull request, and always restores what it borrowed.\n\n" +
			"It keeps one job in flight and holds its state in the GPU lease plus the\n" +
			"Issue labels, so a restart recovers rather than loses the job.",
	}
	cmd.AddCommand(newControllerRunCmd(g))
	return cmd
}

func newControllerRunCmd(g *globalFlags) *cobra.Command {
	var configPath string
	var once bool
	cmd := &cobra.Command{
		Use:   "run --config <operator.yaml>",
		Short: "Run the control loop until interrupted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := operator.LoadMVP(configPath)
			if err != nil {
				return err
			}
			ctrl, err := buildController(cmd.Context(), cfg, g, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			if once {
				handled, err := ctrl.RunOnce(ctx)
				if err != nil {
					return err
				}
				if !handled {
					fmt.Fprintln(cmd.OutOrStdout(), "no pending request")
				}
				return nil
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "controller running for %s\n", cfg.Repository)
			return ctrl.Run(ctx,
				time.Duration(cfg.PollIntervalSeconds)*time.Second,
				time.Duration(cfg.RecoveryIntervalSeconds)*time.Second,
			)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "operator.yaml", "operator configuration (docs/mvp.md §9)")
	cmd.Flags().BoolVar(&once, "once", false, "poll once and exit (for a dry run)")
	return cmd
}

// buildController wires the real adapters. Everything the controller touches
// is constructed here, so the policy package itself stays free of SDKs.
func buildController(ctx context.Context, cfg operator.MVP, g *globalFlags, logw io.Writer) (*controller.Controller, error) {
	owner, repo := cfg.RepoParts()
	if repo == "" {
		return nil, fmt.Errorf("controller: repository %q is not owner/name", cfg.Repository)
	}
	app, err := githubapp.NewFromFiles(cfg.GitHubApp.AppID, cfg.GitHubApp.InstallationID, cfg.GitHubApp.PrivateKeyFile, cfg.GitHubApp.BaseURL)
	if err != nil {
		return nil, err
	}
	gh, err := app.Client(ctx)
	if err != nil {
		return nil, err
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", g.kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("controller: kubeconfig: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("controller: kubernetes client: %w", err)
	}
	leaseDuration := time.Duration(cfg.Lease.DurationSeconds) * time.Second
	leaseStore := kube.NewGPULease(client, cfg.Lease.Namespace, cfg.Lease.Name, leaseDuration)

	// The pause/restore plan is bound per job (deterministic branch names), so
	// the pauser resolves the hook from the job id it is given.
	checker, err := kube.NewChecker(g.kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("controller: kubernetes checker: %w", err)
	}
	pause := &pauser{cfg: cfg, gh: app, checker: checker}
	sandboxClient := &sandbox.Client{Namespace: cfg.Sandbox.Namespace, Kubeconfig: g.kubeconfig}
	box := &sandboxAdapter{client: sandboxClient, warmPool: cfg.Sandbox.WarmPool, logf: func(format string, args ...any) {
		fmt.Fprintf(logw, format+"\n", args...)
	}}
	reserved := make(map[string][]string, len(cfg.Engines))
	for _, engine := range cfg.Engines {
		args, err := runtime.ReservedArgs(engine)
		if err != nil {
			return nil, err
		}
		reserved[engine] = args
	}
	holder, err := os.Hostname()
	if err != nil || holder == "" {
		return nil, fmt.Errorf("controller: cannot determine the instance identity (hostname): %w", err)
	}
	return &controller.Controller{
		Config: controller.Config{
			Repo:          cfg.Repository,
			DefaultBranch: cfg.DefaultBranch,
			Labels:        cfg.Labels,
			Models:        cfg.Models,
			Constraints:   cfg.Constraints(nil),
			ReservedArgs:  reserved,
			Sandbox:       cfg.Sandbox,
			LLMBench:      cfg.Sandbox.LLMBench,
			// The holder is instance-unique (the Pod name). A shared identity
			// would let a new Pod take over a live lease and clean up a
			// sandbox that is being measured in, so the two instances must be
			// distinguishable.
			HolderIdentity: "llmbench-controller-" + holder,
			LeaseDuration:  leaseDuration,
			// A fresh, narrowly scoped token per job: the sandbox may push to
			// this repository and nothing else. A token lives an hour, so an
			// optimization run that outlasts it fails its push loudly instead
			// of publishing with stale credentials (docs/mvp.md §7).
			GitToken: func(ctx context.Context) (string, error) {
				return app.ScopedToken(ctx, githubapp.TokenOptions{
					Repositories: []string{repo},
					Permissions:  map[string]string{"contents": "write"},
				})
			},
		},
		Gateway: issues.NewGateway(gh, owner, repo, ""),
		Lease:   leaseStore,
		Pauser:  pause,
		Sandbox: box,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(logw, format+"\n", args...)
		},
	}, nil
}

// pauser adapts the GitOps hook to the controller's pause/restore contract.
// The hook signals "wait for the human merge" with its own sentinel, which is
// translated here so the controller never treats a pending pause as a failure.
type pauser struct {
	cfg     operator.MVP
	gh      *githubapp.App
	checker *kube.Checker
}

func (p *pauser) hook(ctx context.Context, jobID string) (*gitops.PauseRestore, error) {
	plan, err := p.cfg.GitOpsPlanFor(jobID)
	if err != nil {
		return nil, err
	}
	client, err := p.gh.Client(ctx)
	if err != nil {
		return nil, err
	}
	api := gitops.NewGitHubAPI(client, plan.Owner, plan.Repository, plan.BaseBranch)
	return &gitops.PauseRestore{Plan: *plan, GH: api, Kube: p.checker}, nil
}

func (p *pauser) Pause(ctx context.Context, jobID string) error {
	hook, err := p.hook(ctx, jobID)
	if err != nil {
		return err
	}
	if err := hook.Acquire(ctx); err != nil {
		if errors.Is(err, gitops.ErrNotConverged) {
			return controller.ErrNotConverged
		}
		return err
	}
	return nil
}

func (p *pauser) Restore(ctx context.Context, jobID string) error {
	hook, err := p.hook(ctx, jobID)
	if err != nil {
		return err
	}
	if err := hook.Release(ctx); err != nil {
		if errors.Is(err, gitops.ErrNotConverged) {
			return controller.ErrNotConverged
		}
		return err
	}
	return nil
}

// sandboxAdapter runs the benchmark CLI in the job's sandbox.
type sandboxAdapter struct {
	client   *sandbox.Client
	warmPool string
	logf     func(format string, args ...any)
}

func (s *sandboxAdapter) Ensure(ctx context.Context, jobID string) error {
	for {
		ready, err := s.client.EnsureJobClaim(ctx, jobID, s.warmPool)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *sandboxAdapter) Exec(ctx context.Context, jobID string, argv []string, env map[string]string) ([]byte, error) {
	stdout, stderr, code, err := s.client.JobExec(ctx, jobID, argv, env)
	if err != nil {
		return nil, err
	}
	if stderr != nil {
		s.logf("sandbox %s stderr: %s", jobID, string(stderr))
	}
	if code != 0 {
		return nil, fmt.Errorf("sandbox: the benchmark exited with %d", code)
	}
	return stdout, nil
}

// Delete reports whether the claim is gone. The controller retries a
// not-yet-deleted claim without releasing the lease, so this must not block
// until its own deadline: the retry belongs to the loop, not to the adapter.
func (s *sandboxAdapter) Delete(ctx context.Context, jobID string) (bool, error) {
	return s.client.DeleteJobClaim(ctx, jobID)
}

// Put writes the job spec into the sandbox before the CLI is started. A file
// rather than stdin: the CLI re-reads and validates it, and the sandbox keeps
// the exact spec that ran next to the result.
func (s *sandboxAdapter) Put(ctx context.Context, jobID, path string, content []byte) error {
	name, err := s.client.FindJobClaim(ctx, jobID)
	if err != nil {
		return err
	}
	return s.client.Put(ctx, name, bytes.NewReader(content), path)
}
