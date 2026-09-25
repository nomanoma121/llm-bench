package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
)

type fakeSandboxClient struct {
	claims   map[string]string // runID -> warm pool
	execs    []string          // recorded scripts
	starts   [][]string        // recorded start argv
	stopped  int
	putDest  []string
	pulled   []string
	identity string // canned model identity JSON
	index    []byte
	failExec func(script string) (int, error) // optional failure injection
}

func newFakeSandboxClient(t *testing.T, dir string) *fakeSandboxClient {
	t.Helper()
	// Build a canned identity JSON consistent with ParseModelIdentity.
	f := &fakeSandboxClient{
		claims: map[string]string{},
		index:  []byte("<html></html>"),
	}
	id := provenance.ModelIdentity{
		Root: "/models/m",
		Files: []provenance.ModelFile{
			{Path: "weights.bin", Size: 10, SHA256: strings.Repeat("a", 64)},
		},
	}
	id.TreeDigest = id.ComputeTreeDigest()
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	f.identity = string(b)
	return f
}

func (f *fakeSandboxClient) EnsureSandboxClaim(_ context.Context, runID, warmPool string) error {
	f.claims[runID] = warmPool
	return nil
}

func (f *fakeSandboxClient) Start(_ context.Context, _ string, argv []string, _ map[string]string, _ string) (string, error) {
	f.starts = append(f.starts, argv)
	return "/tmp/pid", nil
}

func (f *fakeSandboxClient) Stop(_ context.Context, _ string, _ string) error {
	f.stopped++
	return nil
}

func (f *fakeSandboxClient) respond(script string) ([]byte, []byte, int, error) {
	if f.failExec != nil {
		if code, err := f.failExec(script); err != nil || code != 0 {
			return nil, []byte("boom"), code, err
		}
	}
	switch {
	case strings.Contains(script, "tree_digest"):
		return []byte(f.identity), nil, 0, nil
	case strings.Contains(script, "src.tar"):
		return nil, nil, 0, nil
	default:
		return []byte("ran"), nil, 0, nil
	}
}

func (f *fakeSandboxClient) Put(_ context.Context, _ string, _ io.Reader, dest string) error {
	f.putDest = append(f.putDest, dest)
	return nil
}

func (f *fakeSandboxClient) Pull(_ context.Context, _ string, path string) ([]byte, error) {
	f.pulled = append(f.pulled, path)
	if strings.HasSuffix(path, "index.html") {
		return f.index, nil
	}
	return []byte("log"), nil
}

func (f *fakeSandboxClient) ReleaseSandboxClaim(_ context.Context, _ string) error {
	return nil
}

// recordExec wraps Exec to record scripts (the fake above dispatches on the
// last recorded script).
type recordingClient struct {
	inner *fakeSandboxClient
	execs *[]string
}

func (r *recordingClient) EnsureSandboxClaim(ctx context.Context, runID, warmPool string) error {
	return r.inner.EnsureSandboxClaim(ctx, runID, warmPool)
}
func (r *recordingClient) Start(ctx context.Context, runID string, argv []string, env map[string]string, cwd string) (string, error) {
	return r.inner.Start(ctx, runID, argv, env, cwd)
}
func (r *recordingClient) Stop(ctx context.Context, runID, handle string) error {
	return r.inner.Stop(ctx, runID, handle)
}
func (r *recordingClient) Exec(ctx context.Context, runID string, argv []string, env map[string]string, cwd string) ([]byte, []byte, int, error) {
	script := argv[len(argv)-1]
	*r.execs = append(*r.execs, script)
	return r.inner.respond(script)
}
func (r *recordingClient) Put(ctx context.Context, runID string, reader io.Reader, dest string) error {
	return r.inner.Put(ctx, runID, reader, dest)
}
func (r *recordingClient) Pull(ctx context.Context, runID, path string) ([]byte, error) {
	return r.inner.Pull(ctx, runID, path)
}
func (r *recordingClient) ReleaseSandboxClaim(ctx context.Context, runID string) error {
	return r.inner.ReleaseSandboxClaim(ctx, runID)
}

type fakeGit struct {
	archive []byte
}

func (f *fakeGit) VerifyCommit(_ context.Context, _ string, _ []provenance.ExpectedFile) error {
	return nil
}
func (f *fakeGit) Archive(_ context.Context, _ string) ([]byte, error) { return f.archive, nil }

func sandboxRun(t *testing.T, dir string) run.Run {
	t.Helper()
	cfg := experiment.Config{
		Model:     "example-model",
		Benchmark: "benchmarks/visual/prompt.md",
		Target:    "gpu",
		Runtime:   experiment.Runtime{Engine: "example", ContextSize: 8},
		Invoke:    experiment.Invoke{Argv: []string{"./invoke.sh"}},
	}
	b, err := experiment.CanonicalJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input")
	if err := os.MkdirAll(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "config.yaml"), []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "prompt.md"), []byte("prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	return run.Run{
		ID:           "r1",
		Target:       "gpu",
		Experiment:   "experiments/m/e/config.yaml",
		InputCommit:  strings.Repeat("a", 40),
		RecipeJSON:   string(b),
		PromptSHA256: provenance.SHA256Hex([]byte("prompt")),
		Artifacts:    run.Artifacts{Dir: dir},
	}
}

func TestSandboxExecuteHappyPath(t *testing.T) {
	dir := t.TempDir()
	inner := newFakeSandboxClient(t, dir)
	var execs []string
	client := &recordingClient{inner: inner, execs: &execs}
	s := &Sandbox{Client: client, Git: &fakeGit{archive: []byte("tar-bytes")}, Root: dir}
	r := sandboxRun(t, dir)

	artifacts, err := s.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.IndexSHA256 == "" || artifacts.LogSHA256 == "" {
		t.Fatal("hashes missing")
	}
	if artifacts.ModelTreeDigest == "" {
		t.Fatal("model digest missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "model-identity.json")); err != nil {
		t.Fatal("model identity not persisted")
	}
	// Runtime start recorded (recipe has no start → no starts).
	if len(inner.starts) != 0 {
		t.Fatalf("starts = %v", inner.starts)
	}
	// Source uploaded.
	if len(inner.putDest) != 1 || !strings.Contains(inner.putDest[0], "src.tar") {
		t.Fatalf("put destinations = %v", inner.putDest)
	}
	// Index and log pulled from the sandbox output path.
	joined := strings.Join(inner.pulled, ",")
	if !strings.Contains(joined, "index.html") || !strings.Contains(joined, "invoke.log") {
		t.Fatalf("pulls = %v", inner.pulled)
	}
}

func TestSandboxExecuteWithoutCommitFails(t *testing.T) {
	dir := t.TempDir()
	inner := newFakeSandboxClient(t, dir)
	s := &Sandbox{Client: &recordingClient{inner: inner, execs: &[]string{}}, Git: &fakeGit{}, Root: dir}
	r := sandboxRun(t, dir)
	r.InputCommit = ""
	if _, err := s.Execute(context.Background(), r); err == nil {
		t.Fatal("expected commit requirement error")
	}
}

func TestSandboxPinMismatchFailsBeforeInvoke(t *testing.T) {
	dir := t.TempDir()
	inner := newFakeSandboxClient(t, dir)
	s := &Sandbox{Client: &recordingClient{inner: inner, execs: &[]string{}}, Git: &fakeGit{}, Root: dir, ModelPins: func(string) map[string]string {
		return map[string]string{"example-model": strings.Repeat("0", 64)}
	}}
	r := sandboxRun(t, dir)
	if _, err := s.Execute(context.Background(), r); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("want pin mismatch, got %v", err)
	}
}

func TestSandboxModelChangeFailsAfterInvoke(t *testing.T) {
	dir := t.TempDir()
	inner := newFakeSandboxClient(t, dir)
	var execs []string
	s := &Sandbox{Client: &recordingClient{inner: inner, execs: &execs}, Git: &fakeGit{}, Root: dir}
	// Corrupt the identity returned after the invoke step: flip a byte.
	count := 0
	_ = count
	inner.failExec = func(script string) (int, error) {
		if strings.Contains(script, "tree_digest") {
			count++
			if count == 2 { // post-invocation digest
				inner.identity = strings.Replace(inner.identity, `"size":10`, `"size":11`, 1)
			}
		}
		return 0, nil
	}
	r := sandboxRun(t, dir)
	if _, err := s.Execute(context.Background(), r); err == nil || !strings.Contains(err.Error(), "model changed") {
		t.Fatalf("want model change detection, got %v", err)
	}
}

func TestSandboxInvokeFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	inner := newFakeSandboxClient(t, dir)
	var execs []string
	client := &recordingClient{inner: inner, execs: &execs}
	s := &Sandbox{Client: client, Git: &fakeGit{}, Root: dir}
	inner.failExec = func(script string) (int, error) {
		if strings.Contains(script, "invoke.sh") {
			return 2, nil
		}
		return 0, nil
	}
	r := sandboxRun(t, dir)
	if _, err := s.Execute(context.Background(), r); err == nil || !strings.Contains(err.Error(), "exit 2") {
		t.Fatalf("want invoke exit failure, got %v", err)
	}
}

func TestClaimHookAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	inner := newFakeSandboxClient(t, dir)
	h := &ClaimHook{RunID: "r1", WarmPool: "pool", Client: &recordingClient{inner: inner, execs: &[]string{}}}
	if h.Name() != "sandbox-claim" {
		t.Fatalf("name = %q", h.Name())
	}
	if err := h.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inner.claims["r1"] != "pool" {
		t.Fatal("claim not ensured")
	}
	if err := h.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inner.stopped != 1 {
		t.Fatal("runtime stop not called")
	}
}

var _ = errors.New
