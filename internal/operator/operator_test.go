package operator

import (
	"strings"
	"testing"
	"time"
)

const serverYAML = `
targets:
  local:
    hooks: []
  gpu:
    hooks:
      - name: coord-lease
        acquire: ["bin/lease", "acquire"]
        release: ["bin/lease", "release"]
    gitops:
      owner: example-owner
      repository: example-manifests
      base_branch: main
      file_path: apps/inference/values.yaml
      yaml_path: [replicaCount]
      active_value: "1"
      paused_value: "0"
      application:
        namespace: argocd
        name: local-inference
      workload:
        namespace: inference
        deployment: local-inference
        active_replicas: 1
    sandbox:
      namespace: bench
      warm_pool: demo-llmbench
site:
  owner: example
  repository: llm-bench-site
  branch: gh-pages
  base_url: https://example.github.io/llm-bench-site
`

func TestParseExamples(t *testing.T) {
	c, err := Parse(strings.NewReader(serverYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := c.Targets["local"]; !ok {
		t.Fatal("local target missing")
	}
	plan := BuildHookPlan(c.Targets["gpu"], "runid")
	if len(plan) != 3 {
		t.Fatalf("plan length = %d, want 3", len(plan))
	}
	if plan[0].Kind != KindCommand || plan[1].Kind != KindGitOps || plan[2].Kind != KindSandboxClaim {
		t.Fatalf("plan order wrong: %s %s %s", plan[0].Kind, plan[1].Kind, plan[2].Kind)
	}
	if plan[1].GitOps.PauseBranch != "llmbench/pause-runid" {
		t.Fatalf("pause branch = %q", plan[1].GitOps.PauseBranch)
	}
	if plan[2].Sandbox.SandboxClaimName != "llmbench-runid" {
		t.Fatalf("claim name = %q", plan[2].Sandbox.SandboxClaimName)
	}
	if _, ok := c.Targets["local"]; !ok {
		t.Fatal("local missing")
	}
	if c.Site == nil || c.Site.Branch != "gh-pages" {
		t.Fatal("site not parsed")
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	in := `
targets:
  local:
    hooks: []
ssh_hosts:
  - somewhere
`
	if _, err := Parse(strings.NewReader(in)); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidateRejectsBrokenTargets(t *testing.T) {
	t.Run("hook missing release", func(t *testing.T) {
		in := `
targets:
  gpu:
    hooks:
      - name: lease
        acquire: ["a"]
`
		if _, err := Parse(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), "release") {
			t.Fatalf("expected release argv error, got %v", err)
		}
	})
	t.Run("incomplete gitops", func(t *testing.T) {
		in := `
targets:
  gpu:
    gitops:
      owner: o
      repository: r
`
		if _, err := Parse(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), "base_branch") {
			t.Fatalf("expected gitops error, got %v", err)
		}
	})
	t.Run("sandbox without warm pool", func(t *testing.T) {
		in := `
targets:
  gpu:
    sandbox:
      namespace: bench
`
		if _, err := Parse(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), "warm_pool") {
			t.Fatalf("expected sandbox error, got %v", err)
		}
	})
}

func TestValidateRecipe(t *testing.T) {
	c, err := Parse(strings.NewReader(serverYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateRecipe("local", 0); err != nil {
		t.Fatalf("local should be allowlisted: %v", err)
	}
	if err := c.ValidateRecipe("unknown", 0); err == nil {
		t.Fatal("expected allowlist error")
	}
	// MaxReadyDuration defaults to 10m; 11m must be rejected.
	if err := c.ValidateRecipe("gpu", 11*60); err == nil {
		t.Fatal("expected ready timeout limit error")
	}
	if err := c.ValidateRecipe("gpu", 5*60); err != nil {
		t.Fatalf("5m should pass: %v", err)
	}
}

func TestEffectiveLimitsDefaults(t *testing.T) {
	c, err := Parse(strings.NewReader("targets:\n  local:\n    hooks: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	lim := c.EffectiveLimits(c.Targets["local"])
	if lim.MaxReadyDuration != DefaultMaxReadyDuration || lim.MaxExecutionDuration != DefaultMaxExecutionDuration {
		t.Fatalf("defaults not applied: %+v", lim)
	}
}

func TestPlanDigestIsStable(t *testing.T) {
	c, err := Parse(strings.NewReader(serverYAML))
	if err != nil {
		t.Fatal(err)
	}
	p1 := BuildHookPlan(c.Targets["gpu"], "abc")
	p2 := BuildHookPlan(c.Targets["gpu"], "abc")
	d1, err := PlanDigest(p1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := PlanDigest(p2)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatal("digest unstable")
	}
	p3 := BuildHookPlan(c.Targets["gpu"], "different")
	d3, _ := PlanDigest(p3)
	if d1 == d3 {
		t.Fatal("digest should differ for different run IDs")
	}
	_ = time.Second
}
