// Package e2e exercises the two MVP paths end to end in one process: a GitHub
// Issue becomes a measured result, the result becomes a pull request, and two
// results can be compared.
//
// Only the collaborators that need a cluster are substituted (the GitHub
// gateway, the GPU lease and the sandbox transport); the benchmark, the
// runtime adapter, the result schema, the push and the comparison are the real
// implementations. That is the closest this repository can get to the MVP
// running without a GPU node, and it is what the operator's real-cluster smoke
// test has to reproduce.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
	"github.com/nomanoma121/llm-bench/internal/compare"
	"github.com/nomanoma121/llm-bench/internal/controller"
	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/lease"
	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

const testToken = "ghs_test_token_value"

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	} else {
		return string(out)
	}
	return ""
}

// repository creates a checkout with a bare remote, which is where the
// benchmark pushes its branch.
func repository(t *testing.T) (root, remote string) {
	t.Helper()
	remote = filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--bare", "--initial-branch=main", remote)
	root = filepath.Join(t.TempDir(), "work")
	git(t, "", "init", "--initial-branch=main", root)
	git(t, root, "config", "user.name", "test")
	git(t, root, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("llm-bench\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "-m", "initial")
	git(t, root, "remote", "add", "origin", remote)
	git(t, root, "push", "origin", "main")
	return root, remote
}

// fakeRuntime speaks the llama.cpp endpoints the adapter uses. decodeMillis is
// configurable so two runs can differ on purpose.
func fakeRuntime(t *testing.T, decodeMillis int) (host string, port int, close func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/completion":
			// A real request takes longer than the collector interval; a few
			// milliseconds keeps that true without slowing the suite down.
			time.Sleep(3 * time.Millisecond)
			for i := range 3 {
				fmt.Fprintf(w, "data: {\"content\":\"tok%d\"}\n\n", i)
			}
			fmt.Fprintf(w, "data: {\"content\":\"\",\"stop\":true,\"timings\":{\"prompt_n\":16,\"predicted_n\":3,\"predicted_ms\":%d}}\n\n", decodeMillis)
		case "/metrics":
			fmt.Fprint(w, "llamacpp:kv_cache_usage_ratio 0.42\nllamacpp:predicted_tokens_seconds 44.5\n")
		default:
			http.NotFound(w, r)
		}
	}))
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname(), p, server.Close
}

// gateway is the GitHub side, in memory.
type gateway struct {
	mu       sync.Mutex
	issues   []controller.Issue
	labels   map[int][]string
	comments map[int][]string
	prs      []controller.PullRequest
}

func (g *gateway) Pending(_ context.Context, labels operator.Labels) ([]controller.Issue, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []controller.Issue
	for _, issue := range g.issues {
		state := false
		for _, l := range issue.Labels {
			if l == labels.Claimed || l == labels.Done || l == labels.Failed {
				state = true
			}
		}
		if !state {
			out = append(out, issue)
		}
	}
	return out, nil
}

func (g *gateway) Label(_ context.Context, issue int, label string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.labels[issue] = append(g.labels[issue], label)
	for i := range g.issues {
		if g.issues[i].Number == issue {
			g.issues[i].Labels = append(g.issues[i].Labels, label)
		}
	}
	return nil
}

func (g *gateway) Unlabel(_ context.Context, issue int, label string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var kept []string
	for _, l := range g.labels[issue] {
		if l != label {
			kept = append(kept, l)
		}
	}
	g.labels[issue] = kept
	for i := range g.issues {
		if g.issues[i].Number == issue {
			var remaining []string
			for _, l := range g.issues[i].Labels {
				if l != label {
					remaining = append(remaining, l)
				}
			}
			g.issues[i].Labels = remaining
		}
	}
	return nil
}

func (g *gateway) Comment(_ context.Context, issue int, body string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.comments[issue] = append(g.comments[issue], body)
	return nil
}

func (g *gateway) OpenPR(_ context.Context, pr controller.PullRequest) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prs = append(g.prs, pr)
	return 100 + len(g.prs), nil
}

func (g *gateway) Claimed(_ context.Context, issue int, labels operator.Labels) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, l := range g.labels[issue] {
		if l == labels.Claimed {
			return true, nil
		}
	}
	return false, nil
}

func (g *gateway) EnsureLabels(context.Context, operator.Labels) error { return nil }

func (g *gateway) has(issue int, label string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Contains(g.labels[issue], label)
}

// memoryLease is the GPU lease without a cluster.
type memoryLease struct {
	mu      sync.Mutex
	record  lease.Record
	live    bool
	phases  []lease.Phase
	renewed int
}

func (l *memoryLease) Acquire(_ context.Context, holder string, record lease.Record, reentrant bool) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.live {
		if l.record.Holder != holder || !reentrant {
			return false, nil
		}
	}
	record.Holder = holder
	l.record, l.live = record, true
	return true, nil
}

func (l *memoryLease) Renew(_ context.Context, holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.live || l.record.Holder != holder {
		return fmt.Errorf("lease is not ours")
	}
	l.renewed++
	return nil
}

func (l *memoryLease) Annotate(_ context.Context, holder string, mutate func(*lease.Record)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.live || l.record.Holder != holder {
		return fmt.Errorf("lease is held by %q", l.record.Holder)
	}
	mutate(&l.record)
	l.phases = append(l.phases, l.record.Phase)
	return nil
}

func (l *memoryLease) Release(_ context.Context, holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.live || l.record.Holder != holder {
		return fmt.Errorf("lease is held by %q", l.record.Holder)
	}
	l.live = false
	l.record.Holder = ""
	l.record.Phase = lease.PhaseReleased
	return nil
}

func (l *memoryLease) Get(context.Context) (lease.Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record, nil
}

func (l *memoryLease) released() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.live && l.record.Phase == lease.PhaseReleased
}

// noopStarter stands in for the runtime process: the runtime this test talks
// to is an httptest server on loopback.
type noopStarter struct{}

func (noopStarter) Start(context.Context, []string, string) (benchmark.Process, error) {
	return noopProcess{}, nil
}

type noopProcess struct{}

func (noopProcess) Wait() error                { return nil }
func (noopProcess) Stop(context.Context) error { return nil }

// sandbox runs the real benchmark in this process and pushes with the real
// push helper: the sandbox transport is the only thing replaced.
type sandbox struct {
	repoRoot string
	remote   string
	runtime  func() (string, int)
	specs    map[string][]byte
	specMu   sync.Mutex
	pushes   []string
}

func (s *sandbox) Ensure(context.Context, string) error { return nil }

func (s *sandbox) Put(_ context.Context, jobID, _ string, content []byte) error {
	s.specMu.Lock()
	defer s.specMu.Unlock()
	if s.specs == nil {
		s.specs = map[string][]byte{}
	}
	s.specs[jobID] = content
	return nil
}

func (s *sandbox) Exec(ctx context.Context, jobID string, argv []string, env map[string]string) ([]byte, error) {
	s.specMu.Lock()
	specYAML := s.specs[jobID]
	s.specMu.Unlock()
	if specYAML == nil {
		return nil, fmt.Errorf("no job spec was written for %s", jobID)
	}
	spec, err := job.Parse(strings.NewReader(string(specYAML)))
	if err != nil {
		return nil, err
	}
	if !slices.Contains(argv, "--push") {
		return nil, fmt.Errorf("the controller did not ask for --push: %v", argv)
	}
	host, port := s.runtime()
	adapter, err := runtime.New(spec.Runtime.Engine, runtime.Options{
		ModelID: spec.Model.ID, ModelPath: modelPath(argv), Host: host, Port: port,
	})
	if err != nil {
		return nil, err
	}
	outDir := filepath.Join(s.repoRoot, spec.Output.Dir, jobID)
	// The runtime is the httptest server, not a process this test can start,
	// so the harness gets a starter that does nothing. Everything else is the
	// production implementation.
	outcome, err := benchmark.Run(ctx, benchmark.Config{
		Spec: spec, JobID: jobID, RepoRoot: s.repoRoot, OutputDir: outDir,
		ModelPath: modelPath(argv), ModelDigest: modelDigest(argv),
		// A test-sized collector interval, so a millisecond-scale workload is
		// still sampled while it runs (production uses 500ms, which a real
		// request easily exceeds).
		SampleInterval: 2 * time.Millisecond,
	}, adapter, benchmark.Seams{Starter: noopStarter{}, Now: time.Now, ProbeTimeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(s.repoRoot, outcome.Dir)
	if err != nil {
		return nil, err
	}
	branch := "llmbench/" + jobID
	if _, err := benchmark.Push(ctx, benchmark.PushOptions{
		RepoRoot: s.repoRoot, Paths: []string{rel}, Branch: branch,
		Message: "benchmark: " + jobID, Token: env[controller.SandboxTokenEnv],
	}); err != nil {
		return nil, err
	}
	s.specMu.Lock()
	s.pushes = append(s.pushes, branch)
	s.specMu.Unlock()
	// The CLI's JSON summary, which is what the controller parses.
	return json.Marshal(map[string]any{
		"job_id": outcome.Result.JobID, "output_dir": outcome.Dir,
		"result_digest": outcome.Result.ResultDigest, "series_digest": outcome.Result.SeriesDigest,
		"measurement_valid": outcome.Result.MeasurementValid, "branch": branch, "commit": "deadbeef",
	})
}

func (s *sandbox) Delete(context.Context, string) (bool, error) { return true, nil }

func modelPath(argv []string) string {
	for i, a := range argv {
		if a == "--model-path" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func modelDigest(argv []string) string {
	for i, a := range argv {
		if a == "--model-digest" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// issueBody renders a benchmark request exactly the way the Issue form does.
func issueBody(t *testing.T, model string, port int, maxTokens int) string {
	t.Helper()
	spec := job.Spec{
		Kind:  job.KindBenchmark,
		Model: job.Model{ID: model},
		Runtime: job.Runtime{
			Engine: "llamacpp", Image: "ghcr.io/example/llama.cpp@sha256:" + strings.Repeat("a", 64),
			Ready: job.Ready{Port: port},
		},
		Workload: job.Workload{Cases: []job.Case{{Name: "short", PromptText: "hello world", MaxTokens: maxTokens, Repeats: 2}}},
		Metrics:  job.Metrics{Collectors: []job.Collector{job.CollectorHarness, job.CollectorRuntime}},
		Output:   job.Output{Dir: "experiments/" + model},
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	return "Requested by the operator.\n\n```yaml\nkind: benchmark\n" +
		yamlBody(t, spec) + "```\n"
}

// yamlBody renders the spec without its kind line, which the fence above adds.
func yamlBody(t *testing.T, spec job.Spec) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "model:\n  id: %s\n", spec.Model.ID)
	fmt.Fprintf(&b, "runtime:\n  engine: %s\n  image: %s\n  ready:\n    port: %d\n",
		spec.Runtime.Engine, spec.Runtime.Image, spec.Runtime.Ready.Port)
	fmt.Fprintf(&b, "workload:\n  cases:\n    - name: %s\n      prompt_text: %q\n      max_tokens: %d\n      repeats: %d\n",
		spec.Workload.Cases[0].Name, spec.Workload.Cases[0].PromptText, spec.Workload.Cases[0].MaxTokens, spec.Workload.Cases[0].Repeats)
	fmt.Fprintf(&b, "metrics:\n  collectors: [harness, runtime]\n")
	fmt.Fprintf(&b, "output:\n  dir: %s\n", spec.Output.Dir)
	return b.String()
}

// runJob drives one Issue through the controller.
type rig struct {
	logs    []string
	root    string
	remote  string
	gw      *gateway
	lease   *memoryLease
	box     *sandbox
	ctrl    *controller.Controller
	runtime func() (string, int)
}

func newRig(t *testing.T, host string, port int) *rig {
	t.Helper()
	root, remote := repository(t)
	r := &rig{
		root: root, remote: remote,
		gw:    &gateway{labels: map[int][]string{}, comments: map[int][]string{}},
		lease: &memoryLease{},
		runtime: func() (string, int) {
			return host, port
		},
	}
	r.box = &sandbox{repoRoot: root, remote: remote, runtime: r.runtime}
	r.ctrl = &controller.Controller{
		Config: controller.Config{
			Repo: "owner/repo", DefaultBranch: "main",
			Labels: operator.Labels{
				Benchmark: "llmbench:benchmark", Optimize: "llmbench:optimize",
				Claimed: "llmbench:claimed", Done: "llmbench:done", Failed: "llmbench:failed",
			},
			Models: []operator.MVPModel{{ID: "model-a", Path: "/models/a", Digest: strings.Repeat("d", 64)}},
			Constraints: job.Constraints{
				Engines: []string{"llamacpp"}, Images: []string{"ghcr.io/example/llama.cpp@sha256:" + strings.Repeat("a", 64)},
				Models: []string{"model-a"}, OutputRoots: []string{"experiments"},
			},
			ReservedArgs:   map[string][]string{"llamacpp": {"--model", "--host", "--port", "--metrics"}},
			LLMBench:       []string{"llmbench"},
			HolderIdentity: "e2e",
			LeaseDuration:  time.Minute,
			GitToken:       func(context.Context) (string, error) { return testToken, nil },
		},
		Gateway: r.gw,
		Lease:   r.lease,
		Sandbox: r.box,
		Now:     func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) },
		// The lease keeper logs from its own goroutine, where testing.T is not
		// safe to use after the test returns.
		Logf: func(format string, args ...any) { r.logs = append(r.logs, fmt.Sprintf(format, args...)) },
	}
	return r
}

func TestBenchmarkIssueBecomesAMeasuredPullRequest(t *testing.T) {
	host, port, closeRuntime := fakeRuntime(t, 20)
	defer closeRuntime()
	r := newRig(t, host, port)
	r.gw.issues = []controller.Issue{{
		Number: 42, Title: "benchmark model-a",
		Body:   issueBody(t, "model-a", port, 8),
		Labels: []string{"llmbench:benchmark"},
	}}

	handled, err := r.ctrl.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("the request was not handled")
	}
	if len(r.gw.prs) == 0 {
		t.Fatalf("no pull request was opened; controller log:\n%s", strings.Join(r.logs, "\n"))
	}
	// The result PR exists and its body carries facts, not a verdict.
	if len(r.gw.prs) != 1 {
		t.Fatalf("pull requests = %+v", r.gw.prs)
	}
	pr := r.gw.prs[0]
	if pr.Head != "llmbench/2026-09-27-issue42" || pr.Base != "main" {
		t.Fatalf("pull request = %+v", pr)
	}
	if !strings.Contains(pr.Body, "result digest") || !strings.Contains(pr.Body, "measurement valid: true") {
		t.Fatalf("pull request body = %q", pr.Body)
	}
	// The labels mirror the outcome: done, and no claim left behind.
	if !r.gw.has(42, "llmbench:done") || r.gw.has(42, "llmbench:claimed") {
		t.Fatalf("labels = %v", r.gw.labels[42])
	}
	if r.gw.has(42, "llmbench:failed") {
		t.Fatalf("labels = %v", r.gw.labels[42])
	}
	// The GPU is free again, and the phase sequence reached the release.
	if !r.lease.released() {
		t.Fatal("the gpu lease was not released")
	}
	// The branch is on the remote and its result verifies.
	branches := git(t, r.remote, "branch", "--list")
	if !strings.Contains(branches, "llmbench/2026-09-27-issue42") {
		t.Fatalf("remote branches = %q", branches)
	}
	resultDir := filepath.Join(r.root, "experiments", "model-a", "2026-09-27-issue42")
	if _, err := measurement.VerifyResultDir(resultDir); err != nil {
		t.Fatalf("the pushed result does not verify: %v", err)
	}
	loaded, _, err := measurement.LoadResult(measurement.ResultPath(resultDir))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.MeasurementValid {
		t.Fatalf("measurement invalid: %v", loaded.InvalidReasons)
	}
	if loaded.Inputs.ModelDigest != strings.Repeat("d", 64) {
		t.Fatalf("model digest = %q", loaded.Inputs.ModelDigest)
	}
	if _, err := measurement.VerifyResultDir(resultDir); err != nil {
		t.Fatal(err)
	}
}

func TestTwoMeasuredResultsCanBeCompared(t *testing.T) {
	// Two runs of the same job on two runtime builds: the comparison has to
	// report the deltas and stay factual.
	hostA, portA, closeA := fakeRuntime(t, 40)
	defer closeA()
	r := newRig(t, hostA, portA)
	body := issueBody(t, "model-a", portA, 8)

	r.gw.issues = []controller.Issue{{Number: 1, Body: body, Labels: []string{"llmbench:benchmark"}}}
	if _, err := r.ctrl.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The second request is a different Issue on a faster runtime build.
	hostB, portB, closeB := fakeRuntime(t, 10)
	defer closeB()
	r.runtime = func() (string, int) { return hostB, portB }
	r.box.runtime = r.runtime
	second := strings.ReplaceAll(body, fmt.Sprintf("port: %d", portA), fmt.Sprintf("port: %d", portB))
	r.gw.issues = append(r.gw.issues, controller.Issue{Number: 2, Body: second, Labels: []string{"llmbench:benchmark"}})
	if _, err := r.ctrl.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	baseline := filepath.Join(r.root, "experiments", "model-a", "2026-09-27-issue1")
	candidate := filepath.Join(r.root, "experiments", "model-a", "2026-09-27-issue2")
	got, err := compare.Compare(baseline, candidate, compare.KindRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Metrics) == 0 {
		t.Fatal("the comparison reported no metrics")
	}
	var compared int
	for _, d := range got.Metrics {
		if d.Status == compare.StatusCompared {
			compared++
		}
	}
	if compared == 0 {
		t.Fatalf("no metric could be compared: %+v", got.Metrics)
	}
	// Both runs used the same runtime image and the same workload, so the
	// pair is comparable for the environment, prompts and workload; what is
	// missing here is only a different build digest, which the fake runtimes
	// cannot produce. The reasons must therefore be about the build, not about
	// the measurement.
	for _, reason := range got.Reasons {
		if strings.Contains(reason, "invalid") || strings.Contains(reason, "prompt") || strings.Contains(reason, "workload") || strings.Contains(reason, "collector") {
			t.Fatalf("unexpected incomparability: %v", got.Reasons)
		}
	}
}
