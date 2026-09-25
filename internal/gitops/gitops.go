// Package gitops implements the inference pause/restore hook. It moves a
// single YAML scalar in an operator-owned manifest repository through GitHub
// PRs and gates completion on Argo CD sync and workload convergence.
//
// Decisions never guess: the current manifest value AND the state of the
// PR this run owns (deterministic branch names) decide what happens next.
// Anything ambiguous is a persistent error; everything not-yet-true is
// ErrNotConverged, which the engine retries.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nomanoma121/llm-bench/internal/operator"
)

// ErrNotConverged means the external state is not where it must be yet: the
// hook stays pending and the engine retries. It is deliberately defined here
// (not imported from the run package) so adapter packages never depend on
// workflow policy; the wiring maps it to ErrNotConverged.
var ErrNotConverged = errors.New("gitops: not converged yet")

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
	// DeleteBranch removes the deterministic branch of a rolled-back PR.
	DeleteBranch(ctx context.Context, branch string) error
}

// Kube gates convergence of the cluster after manifest changes. Implemented
// by internal/kube; tests substitute fakes.
type Kube interface {
	// ArgoSynced reports whether the Application is Synced at revision.
	ArgoSynced(ctx context.Context, namespace, name, revision string) (bool, error)
	// WorkloadStopped reports that the deployment has no ready replicas AND
	// its pods are gone (terminating pods still hold the GPU).
	WorkloadStopped(ctx context.Context, namespace, name string) (bool, error)
	// WorkloadReady reports that the deployment has the wanted ready
	// replicas, with that many pods ready.
	WorkloadReady(ctx context.Context, namespace, name string, want int32) (bool, error)
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
	value, content, baseSHA, err := h.snapshot(ctx)
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
			return ErrNotConverged // waiting for the human merge
		case "":
			if err := h.openPausePR(ctx, content, baseSHA); err != nil {
				return err
			}
			return ErrNotConverged
		case "closed":
			return fmt.Errorf("gitops: pause PR %s was closed without merge; manual intervention required", p.PauseBranch)
		case "merged":
			// The merge we just observed may be reflected in a newer base
			// revision than the one we read; re-read before declaring drift.
			if again, _, _, err := h.snapshot(ctx); err != nil {
				return err
			} else if again != p.ActiveValue {
				return ErrNotConverged // the merge landed; the next tick proceeds
			}
			return fmt.Errorf("gitops: drift: pause PR %s is merged but %s is still %q", p.PauseBranch, p.YAMLPathString(), value)
		default:
			return fmt.Errorf("gitops: unknown PR state %q", prState)
		}
	case p.PausedValue:
		switch prState {
		case "merged":
			return h.convergePaused(ctx)
		case "open":
			return ErrNotConverged
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
	value, content, baseSHA, err := h.snapshot(ctx)
	if err != nil {
		return err
	}
	pauseState, err := h.GH.PRState(ctx, p.PauseBranch)
	if err != nil {
		return err
	}
	// Rollback: the pause PR never merged, nothing to restore — close it and
	// drop the branch so the next run starts from a clean slate.
	if value == p.ActiveValue {
		switch pauseState {
		case "open":
			if err := h.GH.ClosePR(ctx, p.PauseBranch); err != nil {
				return err
			}
			return h.GH.DeleteBranch(ctx, p.PauseBranch)
		case "closed":
			// A previous rollback closed the PR but may have failed to delete
			// the branch; deleting is idempotent, so retry it.
			if err := h.GH.DeleteBranch(ctx, p.PauseBranch); err != nil {
				return err
			}
		}
	}

	restoreState, err := h.GH.PRState(ctx, p.RestoreBranch)
	if err != nil {
		return err
	}
	switch value {
	case p.PausedValue:
		if pauseState != "merged" {
			// Someone else paused the workload: restoring it is not ours to do.
			return fmt.Errorf("gitops: %s is %q but this run's pause PR is %q; refusing to restore a pause we did not create", p.YAMLPathString(), value, pauseState)
		}
		switch restoreState {
		case "open":
			return ErrNotConverged
		case "":
			if err := h.openRestorePR(ctx, content, baseSHA); err != nil {
				return err
			}
			return ErrNotConverged
		case "closed":
			return fmt.Errorf("gitops: restore PR %s was closed without merge; manual intervention required", p.RestoreBranch)
		case "merged":
			// The merge may be reflected in a newer base revision than the
			// one we read; re-read before declaring drift (symmetric with
			// Acquire).
			again, _, _, err := h.snapshot(ctx)
			if err != nil {
				return err
			}
			switch again {
			case p.ActiveValue:
				return ErrNotConverged // the merge landed; the next tick converges
			case p.PausedValue:
				return fmt.Errorf("gitops: drift: restore PR %s is merged but %s is still %q", p.RestoreBranch, p.YAMLPathString(), value)
			default:
				return fmt.Errorf("gitops: unexpected value %q at %s; refusing to continue", again, p.YAMLPathString())
			}
		default:
			return ErrNotConverged
		}
	case p.ActiveValue:
		switch restoreState {
		case "merged":
			return h.convergeActive(ctx)
		case "open":
			return ErrNotConverged
		case "closed":
			return fmt.Errorf("gitops: restore PR %s was closed without merge; manual intervention required", p.RestoreBranch)
		default:
			if pauseState == "merged" {
				// We paused it, yet the manifest is active without our
				// restore: someone else brought it back.
				return fmt.Errorf("gitops: drift: %s is %q again but this run's restore PR does not exist; refusing to treat a foreign change as our restore", p.YAMLPathString(), value)
			}
			// No pause of ours (never paused, or the pause PR was rolled
			// back): the workload simply must be up.
			return h.convergeActive(ctx)
		}
	default:
		return fmt.Errorf("gitops: unexpected value %q at %s; refusing to continue", value, p.YAMLPathString())
	}
}

// snapshot reads the manifest at a pinned base revision, returning the value,
// the raw document and the revision. Every later read/write uses this
// revision so the decision and the edit see the same tree.
func (h *PauseRestore) snapshot(ctx context.Context) (value string, content []byte, baseSHA string, err error) {
	baseSHA, err = h.GH.BaseBranchSHA(ctx)
	if err != nil {
		return "", nil, "", err
	}
	content, _, err = h.GH.FileAt(ctx, h.Plan.FilePath, baseSHA)
	if err != nil {
		return "", nil, "", fmt.Errorf("gitops: read %s: %w", h.Plan.FilePath, err)
	}
	value, err = GetYAMLScalar(content, h.Plan.YAMLPath)
	if err != nil {
		return "", nil, "", fmt.Errorf("gitops: %w", err)
	}
	return value, content, baseSHA, nil
}

func (h *PauseRestore) openPausePR(ctx context.Context, content []byte, baseSHA string) error {
	p := h.Plan
	current, err := GetYAMLScalar(content, p.YAMLPath)
	if err != nil {
		return fmt.Errorf("gitops: %w", err)
	}
	if current != p.ActiveValue {
		return fmt.Errorf("gitops: %s is %q, not %q; refusing to overwrite an unexpected value", p.YAMLPathString(), current, p.ActiveValue)
	}
	updated, err := SetYAMLScalar(content, p.YAMLPath, p.PausedValue)
	if err != nil {
		return fmt.Errorf("gitops: %w", err)
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

func (h *PauseRestore) openRestorePR(ctx context.Context, content []byte, baseSHA string) error {
	p := h.Plan
	current, err := GetYAMLScalar(content, p.YAMLPath)
	if err != nil {
		return fmt.Errorf("gitops: %w", err)
	}
	if current != p.PausedValue {
		return fmt.Errorf("gitops: %s is %q, not %q; refusing to overwrite an unexpected value", p.YAMLPathString(), current, p.PausedValue)
	}
	updated, err := SetYAMLScalar(content, p.YAMLPath, p.ActiveValue)
	if err != nil {
		return fmt.Errorf("gitops: %w", err)
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
		return ErrNotConverged // Argo CD has not converged yet
	}
	stopped, err := h.Kube.WorkloadStopped(ctx, h.Plan.WorkloadNamespace, h.Plan.Deployment)
	if err != nil {
		return err
	}
	if !stopped {
		return ErrNotConverged // pods are still running or terminating
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
		return ErrNotConverged
	}
	ready, err := h.Kube.WorkloadReady(ctx, h.Plan.WorkloadNamespace, h.Plan.Deployment, int32(h.Plan.ActiveReplicas))
	if err != nil {
		return err
	}
	if !ready {
		return ErrNotConverged
	}
	return nil
}

func shortID(branch string) string {
	if i := strings.LastIndex(branch, "-"); i >= 0 {
		return branch[i+1:]
	}
	return branch
}
