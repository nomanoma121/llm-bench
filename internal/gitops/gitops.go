package gitops

import (
	"context"
	"fmt"
	"strings"

	"github.com/nomanoma121/llm-bench/internal/github"
)

type Target struct {
	Repository  string   `yaml:"repository"`
	Branch      string   `yaml:"branch"`
	File        string   `yaml:"file"`
	Path        []string `yaml:"path"`
	ActiveValue string   `yaml:"active_value"`
	PausedValue string   `yaml:"paused_value"`
	Application struct {
		Namespace string `yaml:"namespace"`
		Name      string `yaml:"name"`
	} `yaml:"application"`
	Deployment struct {
		Namespace string `yaml:"namespace"`
		Name      string `yaml:"name"`
		Replicas  int32  `yaml:"replicas"`
	} `yaml:"deployment"`
}

type Manifests interface {
	HeadSHA(ctx context.Context) (string, error)
	File(ctx context.Context, path, ref string) ([]byte, error)
	CreateBranch(ctx context.Context, branch, sha string) error
	DeleteBranch(ctx context.Context, branch string) error
	CommitFile(ctx context.Context, branch, path, message string, content []byte) error
	PullRequest(ctx context.Context, head string) (*github.PullRequest, error)
	OpenPullRequest(ctx context.Context, head, title, body string) (int, error)
	ClosePullRequest(ctx context.Context, number int) error
}

type Cluster interface {
	ArgoSynced(ctx context.Context, namespace, name, revision string) (bool, error)
	DeploymentStopped(ctx context.Context, namespace, name string) (bool, error)
	DeploymentReady(ctx context.Context, namespace, name string, replicas int32) (bool, error)
}

type GitOps struct {
	Target    Target
	Manifests Manifests
	Cluster   Cluster
}

func (g *GitOps) Pause(ctx context.Context, jobID string) (bool, error) {
	value, sha, content, err := g.read(ctx)
	if err != nil {
		return false, err
	}
	switch value {
	case g.Target.PausedValue:
		return g.converged(ctx, sha, false)
	case g.Target.ActiveValue:
		return false, g.propose(ctx, "llmbench/pause-"+jobID, sha, content, g.Target.PausedValue,
			"llmbench: pause inference for "+jobID)
	}
	return false, fmt.Errorf("gitops: unexpected value %q in %s", value, g.Target.File)
}

func (g *GitOps) Restore(ctx context.Context, jobID string) (bool, error) {
	pause := "llmbench/pause-" + jobID
	if pr, err := g.Manifests.PullRequest(ctx, pause); err != nil {
		return false, err
	} else if pr != nil && pr.State == "open" {
		if err := g.Manifests.ClosePullRequest(ctx, pr.Number); err != nil {
			return false, err
		}
		if err := g.Manifests.DeleteBranch(ctx, pause); err != nil {
			return false, err
		}
	}
	value, sha, content, err := g.read(ctx)
	if err != nil {
		return false, err
	}
	switch value {
	case g.Target.ActiveValue:
		return g.converged(ctx, sha, true)
	case g.Target.PausedValue:
		return false, g.propose(ctx, "llmbench/restore-"+jobID, sha, content, g.Target.ActiveValue,
			"llmbench: restore inference after "+jobID)
	}
	return false, fmt.Errorf("gitops: unexpected value %q in %s", value, g.Target.File)
}

func (g *GitOps) read(ctx context.Context) (string, string, []byte, error) {
	sha, err := g.Manifests.HeadSHA(ctx)
	if err != nil {
		return "", "", nil, err
	}
	content, err := g.Manifests.File(ctx, g.Target.File, sha)
	if err != nil {
		return "", "", nil, err
	}
	value, err := GetScalar(content, g.Target.Path)
	return value, sha, content, err
}

func (g *GitOps) propose(ctx context.Context, branch, sha string, content []byte, value, title string) error {
	pr, err := g.Manifests.PullRequest(ctx, branch)
	if err != nil {
		return err
	}
	if pr != nil {
		if pr.State == "closed" && !pr.Merged {
			return fmt.Errorf("gitops: %s was closed without merging", branch)
		}
		return nil
	}
	updated, err := SetScalar(content, g.Target.Path, value)
	if err != nil {
		return err
	}
	if err := g.Manifests.CreateBranch(ctx, branch, sha); err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	if err := g.Manifests.CommitFile(ctx, branch, g.Target.File, title, updated); err != nil {
		return err
	}
	body := fmt.Sprintf("Sets `%s` in `%s` to `%s`.", strings.Join(g.Target.Path, "."), g.Target.File, value)
	_, err = g.Manifests.OpenPullRequest(ctx, branch, title, body)
	return err
}

func (g *GitOps) converged(ctx context.Context, sha string, active bool) (bool, error) {
	t := g.Target
	synced, err := g.Cluster.ArgoSynced(ctx, t.Application.Namespace, t.Application.Name, sha)
	if err != nil || !synced {
		return false, err
	}
	if active {
		return g.Cluster.DeploymentReady(ctx, t.Deployment.Namespace, t.Deployment.Name, t.Deployment.Replicas)
	}
	return g.Cluster.DeploymentStopped(ctx, t.Deployment.Namespace, t.Deployment.Name)
}
