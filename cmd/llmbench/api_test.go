package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/httpapi"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// regression: the serve dispatcher must pick up submitted runs.
func TestDispatcherAdvancesSubmittedRun(t *testing.T) {
	root := t.TempDir()
	promptDir := filepath.Join(root, "examples")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	out := t.TempDir()
	g := &globalFlags{root: root, state: state, output: out}

	opCfg := operator.Config{
		Targets: map[string]operator.Target{
			"local": {Hooks: []operator.CommandHook{}, AllowHTTPLocal: true},
		},
	}
	exPath := filepath.Join(root, "exp.yaml")
	if err := os.WriteFile(exPath, []byte("model: m\nbenchmark: examples/prompt.md\ntarget: local\nruntime:\n  engine: e\n  context_size: 8\ninvoke:\n  argv: [\"/bin/sh\",\"-c\",\"printf x > \\\"$LLMBENCH_OUTPUT_DIR/index.html\\\"\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, inputs, err := prepareRun(g, opCfg, exPath, "")
	if err != nil {
		t.Fatal(err)
	}
	engine := buildEngine(g, opCfg, 100*time.Millisecond, newSandboxClients(g), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := engine.Submit(ctx, r, inputs); err != nil {
		t.Fatal(err)
	}
	go engine.Run(ctx)

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("run did not complete; state on disk is stuck")
		default:
		}
		cur, err := engine.Store.LoadRun(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.Phase.Terminal() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestLoopbackBind(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		":8080":          false,
		"0.0.0.0:8080":   false,
		"[::]:8080":      false,
		"10.0.0.5:8080":  false,
		"not-an-addr":    false,
	}
	for addr, want := range cases {
		if got := loopbackBind(addr); got != want {
			t.Errorf("loopbackBind(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestResolveExperimentPathRejectsEscapes(t *testing.T) {
	root := "/repo"
	if _, _, err := resolveExperimentPath(root, "../outside.yaml"); err == nil {
		t.Error(".. escape accepted")
	}
	if _, _, err := resolveExperimentPath(root, "/etc/passwd"); err == nil {
		t.Error("absolute path outside the repository accepted")
	}
	if _, _, err := resolveExperimentPath(root, ""); err == nil {
		t.Error("empty path accepted")
	}
	full, rel, err := resolveExperimentPath(root, "experiments/m/e/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if full != "/repo/experiments/m/e/config.yaml" || rel != "experiments/m/e/config.yaml" {
		t.Fatalf("full=%q rel=%q", full, rel)
	}
	// Absolute paths inside the repository are accepted and normalized.
	full, rel, err = resolveExperimentPath(root, "/repo/experiments/m/e/config.yaml")
	if err != nil || rel != "experiments/m/e/config.yaml" || full != "/repo/experiments/m/e/config.yaml" {
		t.Fatalf("absolute-in-root: full=%q rel=%q err=%v", full, rel, err)
	}
}

func TestPrepareRunRejectsUnimplementedTargets(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "examples"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "examples", "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	exPath := filepath.Join(root, "exp.yaml")
	body := "model: m\nbenchmark: examples/prompt.md\ntarget: gpu\nruntime:\n  engine: e\n  context_size: 8\ninvoke:\n  argv: [\"true\"]\n"
	if err := os.WriteFile(exPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &globalFlags{root: root, state: t.TempDir(), output: t.TempDir()}

	// GitOps targets are implemented here: the frozen hook plan carries the
	// gitops element that drives recovery.
	gitopsCfg := operator.Config{Targets: map[string]operator.Target{
		"gpu": {GitOps: &operator.GitOps{
			Owner: "o", Repository: "r", BaseBranch: "main",
			FilePath: "apps/values.yaml", YAMLPath: []string{"replicas"},
			ActiveValue: "1", PausedValue: "0",
		}},
	}}
	// Without a GitHub token the hook cannot be built: refuse early instead
	// of persisting a run that can never acquire.
	t.Setenv("LLMBENCH_GITHUB_TOKEN", "")
	if _, _, err := prepareRun(g, gitopsCfg, exPath, ""); err == nil {
		t.Fatal("gitops target without a token must be refused")
	}
	t.Setenv("LLMBENCH_GITHUB_TOKEN", "test-token")
	rec, _, err := prepareRun(g, gitopsCfg, exPath, "")
	if err != nil {
		t.Fatalf("valid gitops target rejected: %v", err)
	}
	kinds := make([]string, 0, len(rec.HookPlan))
	for _, p := range rec.HookPlan {
		kinds = append(kinds, p.Kind)
	}
	if len(kinds) != 1 || kinds[0] != operator.KindGitOps {
		t.Fatalf("hook plan = %v", kinds)
	}

	// Sandbox targets require the full commit.
	sandboxCfg := operator.Config{Targets: map[string]operator.Target{
		"gpu": {Sandbox: &operator.Sandbox{Namespace: "bench", WarmPool: "pool"}},
	}}
	if _, _, err := prepareRun(g, sandboxCfg, exPath, ""); err == nil {
		t.Error("sandbox target accepted without an input commit")
	}
	if _, _, err := prepareRun(g, sandboxCfg, exPath, strings.Repeat("a", 40)); err != nil {
		t.Errorf("sandbox target with a full commit rejected: %v", err)
	}
}

func TestAPIServiceDeniesLocalWithoutToken(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "examples"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "examples", "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "exp.yaml"),
		[]byte("model: m\nbenchmark: examples/prompt.md\ntarget: local\nruntime:\n  engine: e\n  context_size: 8\ninvoke:\n  argv: [\"true\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &globalFlags{root: root, state: t.TempDir(), output: t.TempDir()}
	opCfg := operator.Config{Targets: map[string]operator.Target{
		"local": {Hooks: []operator.CommandHook{}, AllowHTTPLocal: true},
	}}
	engine := buildEngine(g, opCfg, time.Second, newSandboxClients(g), nil)

	// allow_http_local alone is not enough: the API must be authenticated.
	svc := &apiService{g: g, opCfg: opCfg, engine: engine, store: mustFileStore(g), authenticated: false}
	if _, err := svc.Submit(context.Background(), "exp.yaml", ""); !errors.Is(err, httpapi.ErrLocalForbidden) {
		t.Fatalf("want ErrLocalForbidden, got %v", err)
	}
	unfinished, err := engine.Store.ListUnfinished(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(unfinished) != 0 {
		t.Fatalf("a denied submit must not persist a run: %+v", unfinished)
	}
}

func TestSubmitErrorClassification(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "examples"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "examples", "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &globalFlags{root: root, state: t.TempDir(), output: t.TempDir()}
	opCfg := operator.Config{Targets: map[string]operator.Target{
		"local": {Hooks: []operator.CommandHook{}, AllowHTTPLocal: true},
	}}

	t.Run("malformed yaml is a caller mistake", func(t *testing.T) {
		path := filepath.Join(root, "bad.yaml")
		if err := os.WriteFile(path, []byte("model: [unclosed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := prepareRun(g, opCfg, path, "")
		if !isBadRequest(err) {
			t.Fatalf("want BadRequestError, got %v", err)
		}
	})
	t.Run("missing experiment is a caller mistake", func(t *testing.T) {
		_, _, err := prepareRun(g, opCfg, filepath.Join(root, "missing.yaml"), "")
		if !isBadRequest(err) {
			t.Fatalf("want BadRequestError, got %v", err)
		}
	})
	t.Run("missing prompt is a caller mistake", func(t *testing.T) {
		path := filepath.Join(root, "ok.yaml")
		if err := os.WriteFile(path, []byte("model: m\nbenchmark: examples/absent.md\ntarget: local\nruntime:\n  engine: e\n  context_size: 8\ninvoke:\n  argv: [\"true\"]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := prepareRun(g, opCfg, path, "")
		if !isBadRequest(err) {
			t.Fatalf("want BadRequestError, got %v", err)
		}
	})
	t.Run("unreadable prompt directory is internal", func(t *testing.T) {
		benchDir := filepath.Join(root, "benchmarks")
		if err := os.MkdirAll(benchDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(benchDir, "prompt.md"), []byte("p"), 0o644); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "prompt-perm.yaml")
		if err := os.WriteFile(path, []byte("model: m\nbenchmark: benchmarks/prompt.md\ntarget: local\nruntime:\n  engine: e\n  context_size: 8\ninvoke:\n  argv: [\"true\"]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(benchDir, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(benchDir, 0o755) })
		if os.Getuid() == 0 {
			t.Skip("running as root: permission bits are not enforced")
		}
		_, _, err := prepareRun(g, opCfg, path, "")
		if err == nil {
			t.Fatal("expected an error")
		}
		if isBadRequest(err) {
			t.Fatalf("prompt I/O failure must not be a caller mistake: %v", err)
		}
	})

	t.Run("permission failure is internal", func(t *testing.T) {
		dir := filepath.Join(root, "locked")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "exp.yaml")
		if err := os.WriteFile(path, []byte("model: m\nbenchmark: examples/prompt.md\ntarget: local\nruntime:\n  engine: e\n  context_size: 8\ninvoke:\n  argv: [\"true\"]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
		if os.Getuid() == 0 {
			t.Skip("running as root: permission bits are not enforced")
		}
		_, _, err := prepareRun(g, opCfg, path, "")
		if err == nil {
			t.Fatal("expected an error")
		}
		if isBadRequest(err) {
			t.Fatalf("permission failure must not be a caller mistake: %v", err)
		}
	})
}

// isBadRequest mirrors the HTTP layer's classification.
func isBadRequest(err error) bool {
	var bad *httpapi.BadRequestError
	return errors.As(err, &bad)
}

func TestSandboxClientsAreCachedAndNamespaceKeyed(t *testing.T) {
	g := &globalFlags{kubeconfig: "/tmp/kubeconfig"}
	clients := newSandboxClients(g)
	first := clients.client("bench")
	if first == nil {
		t.Fatal("sandbox client missing")
	}
	if clients.client("bench") != first {
		t.Error("client must be cached per namespace")
	}
	if clients.client("other") == first {
		t.Error("different namespaces must not share a client")
	}
}

func TestExecutorRoutingUsesFrozenPlan(t *testing.T) {
	g := &globalFlags{root: "/repo", state: t.TempDir(), output: t.TempDir()}
	cfg := operator.Config{Targets: map[string]operator.Target{
		// The target disappeared from the configuration, but the run's frozen
		// plan still says sandbox: local fallback must be refused.
	}}
	router := executorRouter{g: g, cfg: cfg, sandboxClients: newSandboxClients(g)}
	rec := run.Run{
		ID: "r1", Target: "gpu",
		HookPlan: []operator.PlannedHook{{
			Kind:    operator.KindSandboxClaim,
			Name:    "sandbox-claim",
			Sandbox: &operator.SandboxPlan{Namespace: "bench", WarmPool: "pool", SandboxClaimName: "llmbench-r1"},
		}},
	}
	// The sandbox path must be taken (and fail at the client, not silently
	// execute locally).
	_, err := router.Execute(context.Background(), rec)
	if err == nil {
		t.Fatal("expected an error from the sandbox path")
	}
	if !strings.Contains(err.Error(), "no longer configured") {
		t.Fatalf("missing target must fail closed, got %v", err)
	}
	if strings.Contains(err.Error(), "locally") {
		t.Fatalf("sandbox run fell back to the local executor: %v", err)
	}

	// A target that became local must not execute the sandbox run either.
	localCfg := operator.Config{Targets: map[string]operator.Target{
		"gpu": {Hooks: []operator.CommandHook{}, AllowHTTPLocal: true},
	}}
	router = executorRouter{g: g, cfg: localCfg, sandboxClients: newSandboxClients(g)}
	if _, err := router.Execute(context.Background(), rec); err == nil || !strings.Contains(err.Error(), "local target") {
		t.Fatalf("sandbox run on a now-local target must fail closed, got %v", err)
	}

	// A local run needs its target in the configuration.
	if _, err := router.Execute(context.Background(), run.Run{ID: "r2", Target: "gone"}); err == nil {
		t.Fatal("unknown local target must be refused")
	}
}
