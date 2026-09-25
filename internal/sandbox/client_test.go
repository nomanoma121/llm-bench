package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	extfake "sigs.k8s.io/agent-sandbox/clients/k8s/extensions/clientset/versioned/fake"
	extv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"

	"github.com/nomanoma121/llm-bench/internal/run"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		`plain`:       `'plain'`,
		`a b`:         `'a b'`,
		`it's`:        `'it'"'"'s'`,
		`$(rm -rf /)`: `'$(rm -rf /)'`,
		`a'b'c`:       `'a'"'"'b'"'"'c'`,
	}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExecScript(t *testing.T) {
	got, err := execScript(
		[]string{"./run.sh", "--flag", "value with spaces"},
		map[string]string{"FOO": "bar baz"},
		"/workspace/src",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"set -e",
		"export FOO='bar baz'",
		"cd '/workspace/src'",
		`'./run.sh' '--flag' 'value with spaces'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q:\n%s", want, got)
		}
	}
	// Invalid environment names are rejected, not silently rewritten.
	if _, err := execScript([]string{"true"}, map[string]string{"BAD!": "x"}, ""); err == nil {
		t.Error("invalid env name must be rejected")
	}
}

func TestBackgroundScriptRecordsPID(t *testing.T) {
	got, err := backgroundScript([]string{"./server"}, nil, "/w", "/tmp/pid")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"rm -f '/tmp/pid'",
		"nohup './server' > /tmp/llmbench-runtime.log 2>&1 &",
		"echo $! > '/tmp/pid'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q:\n%s", want, got)
		}
	}
}

func TestClaimName(t *testing.T) {
	if ClaimName("abc") != "llmbench-abc" {
		t.Fatal("claim name derivation changed")
	}
	if runtimePIDFile("abc") != "/tmp/llmbench-runtime-abc.pid" {
		t.Fatal("pid file derivation changed")
	}
}

func readyClaim(name string) *extv1beta1.SandboxClaim {
	return &extv1beta1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "bench"},
		Spec:       extv1beta1.SandboxClaimSpec{WarmPoolRef: extv1beta1.SandboxWarmPoolRef{Name: "pool"}},
		Status: extv1beta1.SandboxClaimStatus{Conditions: []metav1.Condition{
			{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()},
		}},
	}
}

func pendingClaim(name string) *extv1beta1.SandboxClaim {
	c := readyClaim(name)
	c.Status.Conditions = nil
	return c
}

func testClient(objs ...runtime.Object) *Client {
	return &Client{
		Namespace:          "bench",
		Extensions:         extfake.NewSimpleClientset(objs...).ExtensionsV1beta1(),
		ConvergenceTimeout: 200 * time.Millisecond,
	}
}

func TestEnsureSandboxClaimWaitsForReady(t *testing.T) {
	// A ready claim satisfies Acquire.
	c := testClient(readyClaim("llmbench-r1"))
	if err := c.EnsureSandboxClaim(context.Background(), "llmbench-r1", "pool"); err != nil {
		t.Fatalf("ready claim rejected: %v", err)
	}
	// An existing claim for another warm pool is refused.
	if err := c.EnsureSandboxClaim(context.Background(), "llmbench-r1", "other-pool"); err == nil {
		t.Fatal("warm pool mismatch accepted")
	}
}

func TestEnsureSandboxClaimPendingWhenNotReady(t *testing.T) {
	c := testClient(pendingClaim("llmbench-r1"))
	start := time.Now()
	err := c.EnsureSandboxClaim(context.Background(), "llmbench-r1", "pool")
	if !errors.Is(err, run.ErrPending) {
		t.Fatalf("want run.ErrPending, got %v", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatalf("returned before the convergence timeout: %s", time.Since(start))
	}
}

func TestReleaseSandboxClaimWaitsForDeletion(t *testing.T) {
	c := testClient()
	if err := c.EnsureSandboxClaim(context.Background(), "llmbench-gone", "pool"); err == nil {
		t.Fatal("expected pending for a missing claim that gets created without readiness")
	}
	// No claim: release is success (already gone).
	if err := c.ReleaseSandboxClaim(context.Background(), "llmbench-gone"); err != nil {
		t.Fatalf("missing claim must release cleanly: %v", err)
	}
	// A claim deleting in the background: release returns once it is gone.
	c2 := testClient(readyClaim("llmbench-r2"))
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = c2.Extensions.SandboxClaims("bench").Delete(context.Background(), "llmbench-r2", metav1.DeleteOptions{})
	}()
	if err := c2.ReleaseSandboxClaim(context.Background(), "llmbench-r2"); err != nil {
		t.Fatalf("release did not converge: %v", err)
	}
}

func TestExplicitKubeconfigErrorIsNotSwallowed(t *testing.T) {
	c := &Client{Namespace: "bench", Kubeconfig: "/nonexistent/kubeconfig"}
	if _, err := c.claims(); err == nil {
		t.Fatal("a bad explicit kubeconfig must fail instead of falling back to another cluster")
	}
}
