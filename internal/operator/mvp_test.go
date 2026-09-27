package operator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validMVP() MVP {
	return MVP{
		Repository:    "owner/repo",
		DefaultBranch: "main",
		Lease:         Lease{Namespace: "llmb", Name: "gpu"},
		Sandbox: MVPSandbox{
			Namespace: "llmb", WarmPool: "gpu-pool",
			LLMBench: []string{"llmbench"},
		},
		Models:    []MVPModel{{ID: "model-a", Path: "/models/a", Digest: strings.Repeat("a", 64)}},
		Engines:   []string{"llamacpp"},
		Images:    []string{"img"},
		GitHubApp: MVPGitHubApp{AppID: 1, InstallationID: 2, PrivateKeyFile: "/keys/app.pem"},
	}
}

func TestMVPValidate(t *testing.T) {
	m := validMVP()
	m.applyDefaults()
	if err := m.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if m.Labels.Benchmark != "llmbench:benchmark" || m.Lease.DurationSeconds != 1800 {
		t.Fatalf("defaults = %+v", m)
	}
	if got := m.Constraints([]string{"--model"}); len(got.Models) != 1 || got.Engines[0] != "llamacpp" || got.ReservedArgs[0] != "--model" {
		t.Fatalf("constraints = %+v", got)
	}
	if model, ok := m.Model("model-a"); !ok || model.Path != "/models/a" {
		t.Fatalf("model lookup = %+v %t", model, ok)
	}
	if owner, repo := m.RepoParts(); owner != "owner" || repo != "repo" {
		t.Fatalf("repo parts = %q %q", owner, repo)
	}
	// Without gitops the controller cannot pause anything, and it says so
	// instead of running with the GPU contended.
	if _, err := m.GitOpsPlanFor("job"); err == nil {
		t.Fatal("a missing gitops plan was accepted")
	}
}

func TestMVPValidateRejectsIncompleteConfigs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MVP)
		want   string
	}{
		{"repository", func(m *MVP) { m.Repository = "repo" }, "owner/name"},
		{"lease", func(m *MVP) { m.Lease.Name = "" }, "lease"},
		{"sandbox", func(m *MVP) { m.Sandbox.LLMBench = nil }, "sandbox.llmbench"},
		{"github app", func(m *MVP) { m.GitHubApp.AppID = 0 }, "app_id"},
		{"no model", func(m *MVP) { m.Models = nil }, "at least one model"},
		{"model without path", func(m *MVP) { m.Models = []MVPModel{{ID: "a"}} }, "needs id and path"},
		{"duplicate model", func(m *MVP) { m.Models = append(m.Models, m.Models[0]) }, "duplicates"},
		{"no engine", func(m *MVP) { m.Engines = nil }, "engines"},
		{"no image", func(m *MVP) { m.Images = nil }, "images"},
		{"partial gitops", func(m *MVP) {
			m.GitOps = &GitOps{Owner: "o"}
		}, "gitops needs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := validMVP()
			tc.mutate(&m)
			m.applyDefaults()
			err := m.Validate()
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadMVPRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mvp.yaml")
	if err := os.WriteFile(path, []byte("repository: o/r\nboom: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMVP(path); err == nil {
		t.Fatal("an unknown field was accepted")
	}
}

func TestGitOpsPlanBindsBranchesToTheJob(t *testing.T) {
	m := validMVP()
	m.GitOps = &GitOps{
		Owner: "o", Repository: "manifests", BaseBranch: "main",
		FilePath: "apps/inference.yaml", YAMLPath: []string{"spec", "replicas"},
		ActiveValue: "1", PausedValue: "0",
	}
	m.GitOps.Application.Namespace = "argocd"
	m.GitOps.Application.Name = "inference"
	m.GitOps.Workload.Namespace = "llmb"
	m.GitOps.Workload.Deployment = "llama"
	m.applyDefaults()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	plan, err := m.GitOpsPlanFor("2026-09-27-issue42")
	if err != nil {
		t.Fatal(err)
	}
	if plan.PauseBranch != "llmbench/pause-2026-09-27-issue42" || plan.RestoreBranch != "llmbench/restore-2026-09-27-issue42" {
		t.Fatalf("branches = %q / %q", plan.PauseBranch, plan.RestoreBranch)
	}
	if plan.ActiveValue != "1" || plan.PausedValue != "0" {
		t.Fatalf("values = %+v", plan)
	}
}
