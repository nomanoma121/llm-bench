package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	extv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

// ManagedByLabel marks a claim as created by llmbench. Discovery requires it
// as well as the job label: without it, a claim that merely copies the
// job-id label (which is predictable) would be usable as the Agent's sandbox.
const ManagedByLabel = "app.kubernetes.io/managed-by"

// ManagedByValue is the value of ManagedByLabel for llmbench-owned claims.
const ManagedByValue = "llmbench"

// JobLabel marks a SandboxClaim as belonging to one MVP job. The label is how
// the Agent's CLI finds the current sandbox without the controller having to
// tell it anything: a replacement sandbox carries the same label, so "rebind"
// is just the next lookup (docs/mvp.md §8.1).
const JobLabel = "llmbench.io/job-id"

// JobClaimName is the deterministic claim name for a job.
func JobClaimName(jobID string) string { return "llmbench-job-" + jobID }

// ownsClaim reports whether a claim is this job's llmbench claim. Every path
// that attaches to or deletes a claim checks it: the claim name is derived
// from a predictable job id, so the name alone proves nothing.
func ownsClaim(claim *extv1beta1.SandboxClaim, jobID string) error {
	if claim.Labels[JobLabel] != jobID || claim.Labels[ManagedByLabel] != ManagedByValue {
		return fmt.Errorf("sandbox: claim %s is not this job's llmbench claim (labels %v)", claim.Name, claim.Labels)
	}
	return nil
}

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
				ManagedByLabel: ManagedByValue,
				JobLabel:       jobID,
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
//
// The ownership check happens before the delete: the name is derived from a
// predictable job id, so deleting by name alone could remove a claim that is
// not ours.
func (c *Client) DeleteJobClaim(ctx context.Context, jobID string) (bool, error) {
	claims, err := c.claims()
	if err != nil {
		return false, err
	}
	name := JobClaimName(jobID)
	existing, err := claims.Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return true, nil // already gone: cleanup is idempotent
	case err != nil:
		return false, fmt.Errorf("sandbox: read claim %s: %w", name, err)
	}
	if err := ownsClaim(existing, jobID); err != nil {
		return false, err
	}
	return c.ReleaseSandboxClaim(ctx, name)
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
	selector := JobLabel + "=" + jobID + "," + ManagedByLabel + "=" + ManagedByValue
	list, err := claims.List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return "", fmt.Errorf("sandbox: list claims for job %s: %w", jobID, err)
	}
	// The selector is not a trust boundary on its own: verify the labels of
	// every match, so a claim that only looks like ours is never used.
	owned := list.Items[:0]
	for _, item := range list.Items {
		if item.Labels[JobLabel] == jobID && item.Labels[ManagedByLabel] == ManagedByValue {
			owned = append(owned, item)
			continue
		}
		return "", fmt.Errorf("sandbox: claim %s carries the job label but is not managed by llmbench", item.Name)
	}
	switch len(owned) {
	case 0:
		return "", fmt.Errorf("sandbox: no sandbox exists for job %s", jobID)
	case 1:
		return owned[0].Name, nil
	default:
		names := make([]string, 0, len(owned))
		for _, item := range owned {
			names = append(names, item.Name)
		}
		return "", fmt.Errorf("sandbox: job %s has %d claims (%v); wait for the replacement to finish", jobID, len(owned), names)
	}
}

// AgentResultPath is where the Agent records that it finished. The result is a
// file inside its own sandbox rather than an annotation on the claim: the
// Agent's ServiceAccount is read-only for claims, so it cannot rewrite the
// sandbox spec, and the record travels with the artifacts it describes.
const AgentResultPath = "/workspace/.llmbench-agent-result.json"

// AgentResult is the Agent's completion record.
type AgentResult struct {
	Status string `json:"status"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	// Note is free text for the reviewer (what was tried, what was left out).
	Note string `json:"note,omitempty"`
}

// WriteAgentResult stores the Agent's completion record in its sandbox.
func (c *Client) WriteAgentResult(ctx context.Context, jobID string, result AgentResult) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("sandbox: encode the agent result: %w", err)
	}
	name, err := c.FindJobClaim(ctx, jobID)
	if err != nil {
		return err
	}
	if err := c.Put(ctx, name, bytes.NewReader(append(payload, '\n')), AgentResultPath); err != nil {
		return err
	}
	return nil
}

// ReadAgentResult reads the Agent's completion record, if it wrote one.
func (c *Client) ReadAgentResult(ctx context.Context, jobID string) (AgentResult, bool, error) {
	name, err := c.FindJobClaim(ctx, jobID)
	if err != nil {
		return AgentResult{}, false, err
	}
	payload, err := c.Pull(ctx, name, AgentResultPath)
	if err != nil {
		// A missing record is "the Agent has not finished", not a failure.
		return AgentResult{}, false, nil
	}
	var result AgentResult
	if err := json.Unmarshal(bytes.TrimSpace(payload), &result); err != nil {
		return AgentResult{}, false, fmt.Errorf("sandbox: decode the agent result: %w", err)
	}
	return result, true, nil
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
