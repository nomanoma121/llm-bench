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
	c.Labels = map[string]string{JobLabel: jobID, ManagedByLabel: ManagedByValue}
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
	// A claim that only copies the job label is not ours: discovery verifies
	// both labels instead of trusting the selector.
	foreign := readyClaim("llmbench-job-foreign")
	foreign.Labels = map[string]string{JobLabel: "job-x"}
	foreignClient := testClient(foreign)
	if _, err := foreignClient.FindJobClaim(ctx, "job-x"); err == nil {
		t.Fatal("a claim that is not managed by llmbench was used")
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

func TestDeleteJobClaimRefusesAForeignClaim(t *testing.T) {
	ctx := context.Background()
	// A claim with the deterministic name but without llmbench ownership must
	// not be deleted: the name is derived from a predictable job id.
	foreign := readyClaim(JobClaimName("job-a"))
	c := testClient(foreign)
	if _, err := c.DeleteJobClaim(ctx, "job-a"); err == nil {
		t.Fatal("a foreign claim was deleted")
	}
	if _, err := c.Extensions.SandboxClaims("bench").Get(ctx, JobClaimName("job-a"), metav1.GetOptions{}); err != nil {
		t.Fatalf("the foreign claim is gone: %v", err)
	}
	// Ours is deleted, and a second delete is idempotent.
	ours := testClient(jobClaim(JobClaimName("job-a"), "job-a"))
	if done, err := ours.DeleteJobClaim(ctx, "job-a"); err != nil || !done {
		t.Fatalf("delete: done=%v err=%v", done, err)
	}
	if done, err := ours.DeleteJobClaim(ctx, "job-a"); err != nil || !done {
		t.Fatalf("second delete: done=%v err=%v", done, err)
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

func TestAgentResultNeedsASandbox(t *testing.T) {
	// Both directions go through the job's sandbox, so a job without one is an
	// error rather than a silent success. The file transfer itself is covered
	// by the port-forward tests.
	ctx := context.Background()
	c := testClient()
	if err := c.WriteAgentResult(ctx, "job-a", AgentResult{Status: "complete", Branch: "b", Commit: "c"}); err == nil {
		t.Fatal("writing a result without a sandbox succeeded")
	}
	if _, _, err := c.ReadAgentResult(ctx, "job-a"); err == nil {
		t.Fatal("reading a result without a sandbox succeeded")
	}
}
