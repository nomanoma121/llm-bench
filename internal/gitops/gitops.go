// Package gitops implements the inference pause/restore hook. It moves a
// single YAML scalar in an operator-owned manifest repository through GitHub
// PRs and gates completion on Argo CD sync and workload convergence.
//
// Decisions never guess: the current manifest value AND the state of the
// PR this run owns (deterministic branch names) decide what happens next.
// Anything ambiguous is a persistent error; everything not-yet-true is
// run.ErrPending, which the engine retries.
package gitops

import (
	"context"
	"fmt"
	"strings"

	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// GitHubAPI is the slice of GitHub the hook needs. Implemented over go-github
// in github.go; tests substitute fakes.
type GitHubAPI interface {
	// FileAt returns the file content and its blob SHA at a ref.
	FileAt(ctx context.Context, path, ref string) ([]byte, string, error)
	// CommitFile writes content to path on the given branch, resolving the
	// blob SHA on that branch itself (retry-safe).
	CommitFile(ctx context.Context, path, branch, message string, content []byte) error
	// CreateBranchFrom creates refs/heads/<branch> at fromSHA. Creating an
	// existing branch counts as success.
	CreateBranchFrom(ctx context.Context, branch, fromSHA string) error
	// BaseBranchSHA returns the head commit SHA of the base branch.
	BaseBranchSHA(ctx context.Context) (string, error)
	// PRState returns "", "open", "merged" or "closed" for the head branch.
	PRState(ctx context.Context, headBranch string) (string, error)
	// CreatePR opens a PR from headBranch into the base branch.
	CreatePR(ctx context.Context, title, headBranch, body string) error
	// ClosePR closes an open PR.
	ClosePR(ctx context.Context, headBranch string) error
}

// Kube gates convergence of the cluster after manifest changes. Implemented
// by internal/kube; tests substitute fakes.
type Kube interface {
	// ArgoSynced reports whether the Application is Synced at revision.
	ArgoSynced(ctx context.Context, namespace, name, revision string) (bool, error)
	// ReadyReplicas returns the ready replica count of the deployment.
	ReadyReplicas(ctx context.Context, namespace, name string) (int32, error)
}

// PauseRestore is the run.Hook implementation for one run's GitOps plan.
type PauseRestore struct {
	Plan operator.GitOpsPlan
	GH   GitHubAPI
	Kube Kube
}

// Name implements run.Hook.
func (h *PauseRestore) Name() string { return "gitops" }

// Acquire pauses the inference workload.
func (h *PauseRestore) Acquire(ctx context.Context) error {
	p := h.Plan
	value, err := h.current(ctx)
	if err != nil {
		return err
	}
	prState, err := h.GH.PRState(ctx, p.PauseBranch)
	if err != nil {
		return err
	}
	switch value {
	case p.ActiveValue:
		switch prState {
		case "open":
			return run.ErrPending // waiting for the human merge
		case "":
			if err := h.openPausePR(ctx); err != nil {
				return err
			}
			return run.ErrPending
		case "closed":
			return fmt.Errorf("gitops: pause PR %s was closed without merge; manual intervention required", p.PauseBranch)
		case "merged":
			// Drift: merged but the manifest is still active.
			return fmt.Errorf("gitops: drift: pause PR %s is merged but %s is still %q", p.PauseBranch, p.YAMLPathString(), value)
		default:
			return fmt.Errorf("gitops: unknown PR state %q", prState)
		}
	case p.PausedValue:
		switch prState {
		case "merged":
			return h.convergePaused(ctx)
		case "open":
			return run.ErrPending
		default:
			return fmt.Errorf("gitops: %s is already %q but no pause PR for this run exists; refusing to touch it", p.YAMLPathString(), value)
		}
	default:
		return fmt.Errorf("gitops: unexpected value %q at %s; refusing to continue", value, p.YAMLPathString())
	}
}

// Release restores the inference workload. Any error keeps the run in the
// releasing phase: restoration completes before the run becomes terminal.
func (h *PauseRestore) Release(ctx context.Context) error {
	p := h.Plan
	value, err := h.current(ctx)
	if err != nil {
		return err
	}
	pauseState, err := h.GH.PRState(ctx, p.PauseBranch)
	if err != nil {
		return err
	}
	// Rollback: the pause PR never merged, nothing to restore — close it.
	if pauseState == "open" && value == p.ActiveValue {
		return h.GH.ClosePR(ctx, p.PauseBranch)
	}

	restoreState, err := h.GH.PRState(ctx, p.RestoreBranch)
	if err != nil {
		return err
	}
	switch value {
	case p.PausedValue:
		switch restoreState {
		case "open":
			return run.ErrPending
		case "":
			if err := h.openRestorePR(ctx); err != nil {
				return err
			}
			return run.ErrPending
		case "closed":
			return fmt.Errorf("gitops: restore PR %s was closed without merge; manual intervention required", p.RestoreBranch)
		default:
			return run.ErrPending // merged but the manifest has not caught up
		}
	case p.ActiveValue:
		switch restoreState {
		case "merged":
			return h.convergeActive(ctx)
		case "open":
			return run.ErrPending
		case "closed":
			return fmt.Errorf("gitops: restore PR %s was closed without merge; manual intervention required", p.RestoreBranch)
		default:
			// Active without any of our PRs: the acquire never paused, so the
			// workload must simply be back (or never stopped).
			return h.convergeActive(ctx)
		}
	default:
		return fmt.Errorf("gitops: unexpected value %q at %s; refusing to continue", value, p.YAMLPathString())
	}
}

func (h *PauseRestore) current(ctx context.Context) (string, error) {
	content, _, err := h.GH.FileAt(ctx, h.Plan.FilePath, h.Plan.BaseBranch)
	if err != nil {
		return "", fmt.Errorf("gitops: read %s: %w", h.Plan.FilePath, err)
	}
	value := string(content)
	current, err := GetYAMLScalar([]byte(value), h.Plan.YAMLPath)
	if err != nil {
		return "", fmt.Errorf("gitops: %w", err)
	}
	return current, nil
}

func (h *PauseRestore) openPausePR(ctx context.Context) error {
	p := h.Plan
	value, err := h.rawFile(ctx)
	if err != nil {
		return err
	}
	updated, err := SetYAMLScalar(value, p.YAMLPath, p.PausedValue)
	if err != nil {
		return fmt.Errorf("gitops: %w", err)
	}
	baseSHA, err := h.GH.BaseBranchSHA(ctx)
	if err != nil {
		return err
	}
	if err := h.GH.CreateBranchFrom(ctx, p.PauseBranch, baseSHA); err != nil {
		return err
	}
	if err := h.GH.CommitFile(ctx, p.FilePath, p.PauseBranch,
		fmt.Sprintf("llmbench: pause inference (%s)", p.PauseBranch), []byte(updated)); err != nil {
		return err
	}
	return h.GH.CreatePR(ctx,
		fmt.Sprintf("llmbench: pause inference for %s", shortID(p.PauseBranch)),
		p.PauseBranch,
		fmt.Sprintf("Sets `%s` to `%s` for a benchmark run. Merge to start the run; the run restores it afterwards.", p.YAMLPathString(), p.PausedValue))
}

func (h *PauseRestore) openRestorePR(ctx context.Context) error {
	p := h.Plan
	value, err := h.rawFile(ctx)
	if err != nil {
		return err
	}
	updated, err := SetYAMLScalar(value, p.YAMLPath, p.ActiveValue)
	if err != nil {
		return fmt.Errorf("gitops: %w", err)
	}
	baseSHA, err := h.GH.BaseBranchSHA(ctx)
	if err != nil {
		return err
	}
	if err := h.GH.CreateBranchFrom(ctx, p.RestoreBranch, baseSHA); err != nil {
		return err
	}
	if err := h.GH.CommitFile(ctx, p.FilePath, p.RestoreBranch,
		fmt.Sprintf("llmbench: restore inference (%s)", p.RestoreBranch), []byte(updated)); err != nil {
		return err
	}
	return h.GH.CreatePR(ctx,
		fmt.Sprintf("llmbench: restore inference for %s", shortID(p.RestoreBranch)),
		p.RestoreBranch,
		fmt.Sprintf("Restores `%s` to `%s` after the benchmark run.", p.YAMLPathString(), p.ActiveValue))
}

// rawFile re-reads the file at the base branch for scalar rewriting.
func (h *PauseRestore) rawFile(ctx context.Context) ([]byte, error) {
	content, _, err := h.GH.FileAt(ctx, h.Plan.FilePath, h.Plan.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("gitops: read %s: %w", h.Plan.FilePath, err)
	}
	return content, nil
}

// convergePaused gates on Argo CD sync and the workload being stopped.
func (h *PauseRestore) convergePaused(ctx context.Context) error {
	rev, err := h.GH.BaseBranchSHA(ctx)
	if err != nil {
		return err
	}
	synced, err := h.Kube.ArgoSynced(ctx, h.Plan.AppNamespace, h.Plan.AppName, rev)
	if err != nil {
		return err
	}
	if !synced {
		return run.ErrPending // Argo CD has not converged yet
	}
	replicas, err := h.Kube.ReadyReplicas(ctx, h.Plan.WorkloadNamespace, h.Plan.Deployment)
	if err != nil {
		return err
	}
	if replicas != 0 {
		return run.ErrPending // inference pods are still running
	}
	return nil
}

// convergeActive gates on Argo CD sync and the workload being back.
func (h *PauseRestore) convergeActive(ctx context.Context) error {
	rev, err := h.GH.BaseBranchSHA(ctx)
	if err != nil {
		return err
	}
	synced, err := h.Kube.ArgoSynced(ctx, h.Plan.AppNamespace, h.Plan.AppName, rev)
	if err != nil {
		return err
	}
	if !synced {
		return run.ErrPending
	}
	replicas, err := h.Kube.ReadyReplicas(ctx, h.Plan.WorkloadNamespace, h.Plan.Deployment)
	if err != nil {
		return err
	}
	if int(replicas) != h.Plan.ActiveReplicas {
		return run.ErrPending
	}
	return nil
}

func shortID(branch string) string {
	if i := strings.LastIndex(branch, "-"); i >= 0 {
		return branch[i+1:]
	}
	return branch
}
