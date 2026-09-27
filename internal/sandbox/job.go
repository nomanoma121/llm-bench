package sandbox

import (
	"context"
	"fmt"
	"io"

	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	extv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

// JobLabel marks a SandboxClaim as belonging to one MVP job. The label is how
// the Agent's CLI finds the current sandbox without the controller having to
// tell it anything: a replacement sandbox carries the same label, so "rebind"
// is just the next lookup (docs/mvp.md §8.1).
const JobLabel = "llmbench.io/job-id"

// AgentResultAnnotation is where the Agent records that it finished, so the
// controller learns an optimization round is done without polling the DSH.
const AgentResultAnnotation = "llmbench.io/agent-result"

// AgentBranchAnnotation and AgentCommitAnnotation carry the pushed result.
const (
	AgentBranchAnnotation = "llmbench.io/branch"
	AgentCommitAnnotation = "llmbench.io/commit"
)

// JobClaimName is the deterministic claim name for a job.
func JobClaimName(jobID string) string { return "llmbench-job-" + jobID }

// EnsureJobClaim creates the job's claim if it is absent and waits until it is
// ready. ready=false means "not yet": the caller retries rather than treating
// it as a failure.
func (c *Client) EnsureJobClaim(ctx context.Context, jobID, warmPool string) (bool, error) {
	claims, err := c.claims()
	if err != nil {
		return false, err
	}
	name := JobClaimName(jobID)
	claim := &extv1beta1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "llmbench",
				JobLabel:                       jobID,
			},
		},
		Spec: extv1beta1.SandboxClaimSpec{
			WarmPoolRef: extv1beta1.SandboxWarmPoolRef{Name: warmPool},
		},
	}
	if _, err := claims.Create(ctx, claim, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return false, fmt.Errorf("sandbox: create claim %s: %w", name, err)
		}
		stored, getErr := claims.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return false, fmt.Errorf("sandbox: get claim %s: %w", name, getErr)
		}
		// A claim without the job label belongs to something else; reusing it
		// would run this job in a sandbox that is not ours.
		if stored.Labels[JobLabel] != jobID {
			return false, fmt.Errorf("sandbox: claim %s exists without the job label %s=%s", name, JobLabel, jobID)
		}
		if stored.Spec.WarmPoolRef.Name != warmPool {
			return false, fmt.Errorf("sandbox: claim %s exists with warm pool %q, want %q", name, stored.Spec.WarmPoolRef.Name, warmPool)
		}
	}
	return c.waitClaimReady(ctx, name)
}

// DeleteJobClaim removes the job's claim and waits until it is gone.
func (c *Client) DeleteJobClaim(ctx context.Context, jobID string) (bool, error) {
	return c.ReleaseSandboxClaim(ctx, JobClaimName(jobID))
}

// FindJobClaim returns the claim of a job.
//
// Exactly one claim may match: two matching claims mean a replacement is in
// flight and its predecessor has not finished deleting, which the caller has
// to resolve rather than guess at.
func (c *Client) FindJobClaim(ctx context.Context, jobID string) (string, error) {
	claims, err := c.claims()
	if err != nil {
		return "", err
	}
	list, err := claims.List(ctx, metav1.ListOptions{LabelSelector: JobLabel + "=" + jobID})
	if err != nil {
		return "", fmt.Errorf("sandbox: list claims for job %s: %w", jobID, err)
	}
	switch len(list.Items) {
	case 0:
		return "", fmt.Errorf("sandbox: no sandbox exists for job %s", jobID)
	case 1:
		return list.Items[0].Name, nil
	default:
		names := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
		return "", fmt.Errorf("sandbox: job %s has %d claims (%v); wait for the replacement to finish", jobID, len(list.Items), names)
	}
}

// AnnotateJobClaim records the Agent's result on the claim.
func (c *Client) AnnotateJobClaim(ctx context.Context, jobID string, annotations map[string]string) error {
	name, err := c.FindJobClaim(ctx, jobID)
	if err != nil {
		return err
	}
	claims, err := c.claims()
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		claim, err := claims.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("sandbox: read claim %s: %w", name, err)
		}
		if claim.Annotations == nil {
			claim.Annotations = map[string]string{}
		}
		for k, v := range annotations {
			claim.Annotations[k] = v
		}
		if _, err := claims.Update(ctx, claim, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return fmt.Errorf("sandbox: annotate claim %s: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("sandbox: claim %s kept changing; the annotation may not have landed", name)
}

// JobExec runs a command in the job's current sandbox.
func (c *Client) JobExec(ctx context.Context, jobID string, argv []string, env map[string]string, cwd string) ([]byte, []byte, int, error) {
	name, err := c.FindJobClaim(ctx, jobID)
	if err != nil {
		return nil, nil, 0, err
	}
	return c.Exec(ctx, name, argv, env, cwd)
}

// JobPut writes a file into the job's sandbox.
func (c *Client) JobPut(ctx context.Context, jobID, remotePath string, content io.Reader) error {
	name, err := c.FindJobClaim(ctx, jobID)
	if err != nil {
		return err
	}
	return c.Put(ctx, name, content, remotePath)
}

// JobPull copies a file out of the job's sandbox.
func (c *Client) JobPull(ctx context.Context, jobID, remotePath string, w io.Writer) error {
	name, err := c.FindJobClaim(ctx, jobID)
	if err != nil {
		return err
	}
	content, err := c.Pull(ctx, name, remotePath)
	if err != nil {
		return err
	}
	_, err = w.Write(content)
	return err
}

// FileEntry is one entry of a sandbox directory listing.
type FileEntry struct {
	Name  string
	Size  int64
	IsDir bool
}

// JobList lists a directory in the job's sandbox.
func (c *Client) JobList(ctx context.Context, jobID, dir string) ([]FileEntry, error) {
	name, err := c.FindJobClaim(ctx, jobID)
	if err != nil {
		return nil, err
	}
	sb, err := c.sandboxHandle(ctx, name)
	if err != nil {
		return nil, err
	}
	entries, err := sb.List(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("sandbox: list %s: %w", dir, err)
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, FileEntry{Name: e.Name, Size: e.Size, IsDir: e.Type == sandboxsdk.FileTypeDirectory})
	}
	return out, nil
}
