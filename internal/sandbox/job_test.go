package sandbox

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	extv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

func jobClaim(name, jobID string) *extv1beta1.SandboxClaim {
	c := readyClaim(name)
	c.Labels = map[string]string{JobLabel: jobID}
	return c
}

func TestEnsureJobClaimCarriesTheJobLabel(t *testing.T) {
	ctx := context.Background()
	// Creating the claim is not readiness: the Agent Sandbox controller has to
	// make it ready, so the first call reports pending.
	c := testClient()
	ready, err := c.EnsureJobClaim(ctx, "2026-09-27-issue42", "pool")
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("a freshly created claim reported ready")
	}
	claim, err := c.Extensions.SandboxClaims("bench").Get(ctx, JobClaimName("2026-09-27-issue42"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Labels[JobLabel] != "2026-09-27-issue42" {
		t.Fatalf("labels = %v", claim.Labels)
	}
	// A ready claim with the job label satisfies the next call.
	readyClient := testClient(jobClaim(JobClaimName("2026-09-27-issue42"), "2026-09-27-issue42"))
	if ready, err := readyClient.EnsureJobClaim(ctx, "2026-09-27-issue42", "pool"); err != nil || !ready {
		t.Fatalf("ready claim: ready=%v err=%v", ready, err)
	}
	// An existing claim with the same name but another job is refused: it is
	// not ours to measure in.
	other := testClient(jobClaim(JobClaimName("job-b"), "job-a"))
	if _, err := other.EnsureJobClaim(ctx, "job-b", "pool"); err == nil {
		t.Fatal("a claim belonging to another job was reused")
	}
}

func TestFindJobClaimIsDiscovery(t *testing.T) {
	ctx := context.Background()
	// Rebind is just the next lookup: the replacement carries the same label.
	c := testClient(jobClaim("llmbench-job-old", "job-a"))
	name, err := c.FindJobClaim(ctx, "job-a")
	if err != nil || name != "llmbench-job-old" {
		t.Fatalf("find = %q, %v", name, err)
	}
	c = testClient(jobClaim("llmbench-job-new", "job-a"))
	name, err = c.FindJobClaim(ctx, "job-a")
	if err != nil || name != "llmbench-job-new" {
		t.Fatalf("find after replacement = %q, %v", name, err)
	}
	if _, err := c.FindJobClaim(ctx, "job-missing"); err == nil {
		t.Fatal("a job without a sandbox was accepted")
	}
	// Two matching claims mean a replacement is in flight: refusing is safer
	// than picking one and measuring in the wrong sandbox.
	both := testClient(jobClaim("llmbench-job-a", "job-a"), jobClaim("llmbench-job-b", "job-a"))
	if _, err := both.FindJobClaim(ctx, "job-a"); err == nil || !strings.Contains(err.Error(), "2 claims") {
		t.Fatalf("two claims: %v", err)
	}
}

func TestAnnotateJobClaimRecordsTheAgentResult(t *testing.T) {
	ctx := context.Background()
	c := testClient(jobClaim("llmbench-job-a", "job-a"))
	err := c.AnnotateJobClaim(ctx, "job-a", map[string]string{
		AgentResultAnnotation: "complete",
		AgentBranchAnnotation: "llmbench/job-a",
		AgentCommitAnnotation: "deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := c.Extensions.SandboxClaims("bench").Get(ctx, "llmbench-job-a", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Annotations[AgentResultAnnotation] != "complete" || claim.Annotations[AgentBranchAnnotation] != "llmbench/job-a" {
		t.Fatalf("annotations = %v", claim.Annotations)
	}
	// Annotating a job without a sandbox fails instead of creating anything.
	if err := c.AnnotateJobClaim(ctx, "job-b", map[string]string{AgentResultAnnotation: "complete"}); err == nil {
		t.Fatal("annotating a missing sandbox succeeded")
	}
}

func TestDeleteJobClaimIsIdempotent(t *testing.T) {
	ctx := context.Background()
	// Nothing exists: deleting must succeed so a failed run can clean up.
	c := testClient()
	done, err := c.DeleteJobClaim(ctx, "job-a")
	if err != nil || !done {
		t.Fatalf("delete of a missing claim: done=%v err=%v", done, err)
	}
}
