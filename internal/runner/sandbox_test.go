package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// fakeSandboxClient records every call and serves canned responses. Exec calls
// are disambiguated by their argv/script content.
type fakeSandboxClient struct {
	ensured  []string   // "claim:warmPool"
	starts   [][]string // start argv
	stops    []string   // claim names
	puts     []string   // destination paths
	pulls    []string   // paths
	execs    [][]string // exec argv
	releases []string   // claim names
	identity string     // model identity JSON served to the digest script
	index    []byte
	failCmd  func(argv []string) (int, error)
	// ensureReady/releaseFinished let tests exercise the pending paths.
	ensureReady     bool
	releaseFinished bool
}

func newFakeSandboxClient(t *testing.T) *fakeSandboxClient {
	t.Helper()
	f := &fakeSandboxClient{index: []byte("<html></html>"), ensureReady: true, releaseFinished: true}
	id := provenance.ModelIdentity{
		Root:  "/models/m",
		Files: []provenance.ModelFile{{Path: "weights.bin", Size: 10, SHA256: strings.Repeat("a", 64)}},
	}
	id.TreeDigest = id.ComputeTreeDigest()
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	f.identity = string(b)
	return f
}

func (f *fakeSandboxClient) EnsureSandboxClaim(_ context.Context, claimName, warmPool string) (bool, error) {
	f.ensured = append(f.ensured, claimName+":"+warmPool)
	return f.ensureReady, nil
}

func (f *fakeSandboxClient) Start(_ context.Context, _, claimName string, argv []string, _ map[string]string, _ string) (string, error) {
	f.starts = append(f.starts, argv)
	return "/tmp/pid", nil
}

func (f *fakeSandboxClient) Stop(_ context.Context, claimName, _ string) error {
	f.stops = append(f.stops, claimName)
	return nil
}

func (f *fakeSandboxClient) Exec(_ context.Context, _ string, argv []string, _ map[string]string, _ string) ([]byte, []byte, int, error) {
	f.execs = append(f.execs, argv)
	if f.failCmd != nil {
		if code, err := f.failCmd(argv); err != nil || code != 0 {
			return nil, []byte("boom"), code, err
		}
	}
	joined := strings.Join(argv, " ")
	switch {
	case strings.Contains(joined, "tree_digest"):
		return []byte(f.identity), nil, 0, nil
	default:
		return []byte("ran"), nil, 0, nil
	}
}

func (f *fakeSandboxClient) Put(_ context.Context, _ string, _ io.Reader, dest string) error {
	f.puts = append(f.puts, dest)
	return nil
}

func (f *fakeSandboxClient) Pull(_ context.Context, _, path string) ([]byte, error) {
	f.pulls = append(f.pulls, path)
	if strings.HasSuffix(path, "index.html") {
		return f.index, nil
	}
	return []byte("log"), nil
}

func (f *fakeSandboxClient) ReleaseSandboxClaim(_ context.Context, claimName string) (bool, error) {
	f.releases = append(f.releases, claimName)
	return f.releaseFinished, nil
}

type fakeGit struct{ archive []byte }

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

func newSandbox(f *fakeSandboxClient, dir string) *Sandbox {
	return &Sandbox{
		Client: f,
		Git:    &fakeGit{archive: []byte("tar-bytes")},
		Root:   dir,
		Claim:  "llmbench-r1",
	}
}

func TestSandboxExecutePreparesWorkspaceAndPersistsLog(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSandboxClient(t)
	s := newSandbox(f, dir)
	r := sandboxRun(t, dir)

	artifacts, err := s.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	// Workspace preparation must create the output directory and unpack the
	// archive without building a shell program in the runner.
	var sawMkdirOutput, sawTar bool
	for _, argv := range f.execs {
		if argv[0] == "/bin/sh" || argv[0] == "sh" {
			t.Fatalf("runner built a shell program: %v", argv)
		}
		joined := strings.Join(argv, " ")
		if strings.HasPrefix(joined, "mkdir ") && strings.Contains(joined, sandboxOutput) && strings.Contains(joined, sandboxInput) {
			sawMkdirOutput = true
		}
		if strings.HasPrefix(joined, "tar ") {
			sawTar = true
		}
	}
	if !sawMkdirOutput {
		t.Fatalf("output/input directories were not created: %v", f.execs)
	}
	if !sawTar {
		t.Fatalf("archive was not unpacked with tar: %v", f.execs)
	}

	// The frozen snapshot is uploaded and the env points at it.
	if !containsAll(f.puts, "/workspace/src.tar", "/workspace/input/prompt.md", "/workspace/input/config.yaml") {
		t.Fatalf("puts = %v", f.puts)
	}

	// The invoke log is written on the harness side with the command and exit.
	logPath := filepath.Join(dir, "invoke.log")
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "$ ./invoke.sh") || !strings.Contains(string(body), "[exit: 0]") {
		t.Fatalf("invoke log incomplete:\n%s", body)
	}
	sum := sha256.Sum256(body)
	if artifacts.LogSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("log hash must be computed from the persisted log")
	}
	if artifacts.IndexSHA256 == "" || artifacts.ModelTreeDigest == "" {
		t.Fatalf("artifacts = %+v", artifacts)
	}
}

func TestSandboxExecuteRequiresClaimAndCommit(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSandboxClient(t)
	s := newSandbox(f, dir)
	s.Claim = ""
	if _, err := s.Execute(context.Background(), sandboxRun(t, dir)); err == nil {
		t.Fatal("missing claim accepted")
	}
	s.Claim = "llmbench-r1"
	r := sandboxRun(t, dir)
	r.InputCommit = ""
	if _, err := s.Execute(context.Background(), r); err == nil {
		t.Fatal("missing commit accepted")
	}
}

func TestSandboxModelDigestExitCodeFails(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSandboxClient(t)
	f.failCmd = func(argv []string) (int, error) {
		if strings.Contains(strings.Join(argv, " "), "tree_digest") {
			return 3, nil
		}
		return 0, nil
	}
	s := newSandbox(f, dir)
	if _, err := s.Execute(context.Background(), sandboxRun(t, dir)); err == nil || !strings.Contains(err.Error(), "model identity") {
		t.Fatalf("want model identity failure, got %v", err)
	}
}

func TestSandboxPinMismatchFailsBeforeInvoke(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSandboxClient(t)
	s := newSandbox(f, dir)
	s.ModelPins = func(string) map[string]string {
		return map[string]string{"example-model": strings.Repeat("0", 64)}
	}
	if _, err := s.Execute(context.Background(), sandboxRun(t, dir)); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("want pin mismatch, got %v", err)
	}
	for _, argv := range f.execs {
		if strings.Contains(strings.Join(argv, " "), "invoke.sh") {
			t.Fatal("invoke must not run when the pin mismatches")
		}
	}
}

func TestSandboxModelChangeFailsAfterInvoke(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSandboxClient(t)
	count := 0
	f.failCmd = func(argv []string) (int, error) {
		if strings.Contains(strings.Join(argv, " "), "tree_digest") {
			count++
			if count == 2 {
				f.identity = strings.Replace(f.identity, `"size":10`, `"size":11`, 1)
			}
		}
		return 0, nil
	}
	s := newSandbox(f, dir)
	if _, err := s.Execute(context.Background(), sandboxRun(t, dir)); err == nil || !strings.Contains(err.Error(), "model changed") {
		t.Fatalf("want model change detection, got %v", err)
	}
}

func TestSandboxInvokeFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSandboxClient(t)
	f.failCmd = func(argv []string) (int, error) {
		if strings.Contains(strings.Join(argv, " "), "invoke.sh") {
			return 2, nil
		}
		return 0, nil
	}
	s := newSandbox(f, dir)
	if _, err := s.Execute(context.Background(), sandboxRun(t, dir)); err == nil || !strings.Contains(err.Error(), "exit 2") {
		t.Fatalf("want invoke exit failure, got %v", err)
	}
	// The failing invocation is still logged on the harness side.
	body, err := os.ReadFile(filepath.Join(dir, "invoke.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "[exit: 2]") {
		t.Fatalf("failure log incomplete:\n%s", body)
	}
}

func TestSandboxReadyTimeoutIsClampedByOperatorLimit(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSandboxClient(t)
	s := newSandbox(f, dir)
	s.Limits = func(string) (time.Duration, time.Duration, bool) {
		return time.Millisecond, time.Hour, true
	}
	cfg := experiment.Config{
		Model: "m", Benchmark: "benchmarks/visual/prompt.md", Target: "gpu",
		Runtime: experiment.Runtime{
			Engine: "e", ContextSize: 8,
			Start: &experiment.Start{
				Argv:                []string{"./server"},
				ReadyArgv:           []string{"./probe"},
				ReadyTimeoutSeconds: 3600,
			},
		},
		Invoke: experiment.Invoke{Argv: []string{"./invoke.sh"}},
	}
	body, err := experiment.CanonicalJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.failCmd = func(argv []string) (int, error) {
		if strings.Contains(strings.Join(argv, " "), "probe") {
			return 1, nil
		}
		return 0, nil
	}
	r := sandboxRun(t, dir)
	r.RecipeJSON = string(body)
	start := time.Now()
	if _, err := s.Execute(context.Background(), r); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("want readiness failure, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("readiness was not clamped: waited %s", elapsed)
	}
}

func TestClaimHookUsesFrozenClaimName(t *testing.T) {
	f := newFakeSandboxClient(t)
	h := &ClaimHook{RunID: "r1", Claim: "llmbench-r1", WarmPool: "pool", Client: f}
	if h.Name() != "sandbox-claim" {
		t.Fatalf("name = %q", h.Name())
	}
	if err := h.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.ensured) != 1 || f.ensured[0] != "llmbench-r1:pool" {
		t.Fatalf("ensured = %v", f.ensured)
	}
	if err := h.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.stops) != 1 || f.stops[0] != "llmbench-r1" {
		t.Fatalf("stops = %v", f.stops)
	}
	if len(f.releases) != 1 || f.releases[0] != "llmbench-r1" {
		t.Fatalf("releases = %v", f.releases)
	}
}

func TestClaimHookPendingWhileClaimConverges(t *testing.T) {
	f := newFakeSandboxClient(t)
	f.ensureReady = false
	h := &ClaimHook{RunID: "r1", Claim: "llmbench-r1", WarmPool: "pool", Client: f}
	if err := h.Acquire(context.Background()); !errors.Is(err, run.ErrPending) {
		t.Fatalf("want ErrPending while the claim converges, got %v", err)
	}
	f.releaseFinished = false
	if err := h.Release(context.Background()); !errors.Is(err, run.ErrPending) {
		t.Fatalf("want ErrPending while the claim terminates, got %v", err)
	}
}

func containsAll(hay []string, needles ...string) bool {
	for _, n := range needles {
		found := false
		for _, h := range hay {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
