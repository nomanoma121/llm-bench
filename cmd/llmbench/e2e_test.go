package main

import (
	"bytes"
	"context"
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

	"github.com/nomanoma121/llm-bench/internal/compare"
	"github.com/nomanoma121/llm-bench/internal/controller"
	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/lease"
	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/operator"
)

// The end-to-end tests drive the MVP path in one process: an Issue becomes a
// measurement, the measurement becomes a pull request, and two measurements
// can be compared. Only the collaborators that need a cluster are replaced
// (the GitHub gateway, the GPU lease, and the sandbox transport — which here
// runs the real CLI instead of shelling into a Pod). Everything else is the
// production implementation, so a change that breaks the argv contract, the
// result schema, the push or the comparison shows up here.

const e2eToken = "ghs_e2e_token_value"

const imageA = "ghcr.io/example/llama.cpp@sha256:" + "aaaa"
const imageB = "ghcr.io/example/llama.cpp@sha256:" + "bbbb"

// events is an ordered log of the external effects the controller performed.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) add(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, name)
}

func (e *events) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.list...)
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// repository creates a checkout with a bare remote: the branch the benchmark
// pushes lands there, which is what the pull request would point at.
func repository(t *testing.T) (root, remote string) {
	t.Helper()
	remote = filepath.Join(t.TempDir(), "remote.git")
	gitCmd(t, "", "init", "--bare", "--initial-branch=main", remote)
	root = filepath.Join(t.TempDir(), "work")
	gitCmd(t, "", "init", "--initial-branch=main", root)
	gitCmd(t, root, "config", "user.name", "test")
	gitCmd(t, root, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("llm-bench\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "add", "README.md")
	gitCmd(t, root, "commit", "-m", "initial")
	gitCmd(t, root, "remote", "add", "origin", remote)
	gitCmd(t, root, "push", "origin", "main")
	return root, remote
}

// fakeRuntime speaks the llama.cpp endpoints the adapter uses. chunkDelay
// controls how long the runtime takes between tokens, which is what the
// harness actually measures.
func fakeRuntime(t *testing.T, chunkDelay time.Duration) (host string, port int, closeFn func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/completion":
			flusher, _ := w.(http.Flusher)
			for i := range 5 {
				time.Sleep(chunkDelay)
				fmt.Fprintf(w, "data: {\"content\":\"tok%d\"}\n\n", i)
				if flusher != nil {
					flusher.Flush()
				}
			}
			fmt.Fprint(w, "data: {\"content\":\"\",\"stop\":true,\"timings\":{\"prompt_n\":16,\"predicted_n\":5,\"predicted_ms\":50}}\n\n")
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
	mu sync.Mutex
	// ev records the claim, so a test can assert that the lease was taken
	// before the Issue was claimed (the lease is the job mutex).
	ev       *events
	claimed  string
	issues   []controller.Issue
	labels   map[int][]string
	comments map[int][]string
	prs      []controller.PullRequest
}

func newGateway(ev *events, claimedLabel string, issues ...controller.Issue) *gateway {
	return &gateway{ev: ev, claimed: claimedLabel, issues: issues, labels: map[int][]string{}, comments: map[int][]string{}}
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
	if g.ev != nil && label == g.claimed {
		g.ev.add("claim")
	}
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
	remove := func(in []string) []string {
		out := in[:0]
		for _, l := range in {
			if l != label {
				out = append(out, l)
			}
		}
		return out
	}
	g.labels[issue] = remove(g.labels[issue])
	for i := range g.issues {
		if g.issues[i].Number == issue {
			g.issues[i].Labels = remove(g.issues[i].Labels)
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
	return slices.Contains(g.labels[issue], labels.Claimed), nil
}

func (g *gateway) EnsureLabels(context.Context, operator.Labels) error { return nil }

func (g *gateway) has(issue int, label string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Contains(g.labels[issue], label)
}

func (g *gateway) pullRequests() []controller.PullRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]controller.PullRequest(nil), g.prs...)
}

// memoryLease is the GPU lease without a cluster.
type memoryLease struct {
	ev     *events
	mu     sync.Mutex
	record lease.Record
	live   bool
	phases []lease.Phase
}

func (l *memoryLease) Acquire(_ context.Context, holder string, record lease.Record, reentrant bool) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.live && (l.record.Holder != holder || !reentrant) {
		return false, nil
	}
	record.Holder = holder
	l.record, l.live = record, true
	if l.ev != nil {
		l.ev.add("lease-acquire")
	}
	return true, nil
}

func (l *memoryLease) Renew(_ context.Context, holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.live || l.record.Holder != holder {
		return fmt.Errorf("lease is not ours")
	}
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
	l.phases = append(l.phases, lease.PhaseReleased)
	if l.ev != nil {
		l.ev.add("lease-release")
	}
	return nil
}

// recordSnapshot reads the durable record safely.
func (l *memoryLease) recordSnapshot() lease.Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record
}

func (l *memoryLease) Get(context.Context) (lease.Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record, nil
}

func (l *memoryLease) phaseList() []lease.Phase {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]lease.Phase(nil), l.phases...)
}

// recordingPauser logs the pause and restore.
type recordingPauser struct{ ev *events }

func (p *recordingPauser) Pause(context.Context, string) error {
	p.ev.add("pause")
	return nil
}

func (p *recordingPauser) Restore(context.Context, string) error {
	p.ev.add("restore")
	return nil
}

// cliSandbox plays the sandbox transport: it writes the job spec where the
// controller asked for it, then runs the real CLI with the argv the controller
// built, and returns the CLI's stdout — exactly what the Pod would do.
type cliSandbox struct {
	ev       *events
	repoRoot string
	dir      string
	argv     [][]string
	mu       sync.Mutex
}

func newCLISandbox(t *testing.T, ev *events, repoRoot string) *cliSandbox {
	return &cliSandbox{ev: ev, repoRoot: repoRoot, dir: t.TempDir()}
}

func (s *cliSandbox) Ensure(context.Context, string) error {
	s.ev.add("ensure")
	return nil
}

func (s *cliSandbox) Put(_ context.Context, _, path string, content []byte) error {
	s.ev.add("put")
	return os.WriteFile(filepath.Join(s.dir, filepath.Base(path)), content, 0o644)
}

func (s *cliSandbox) Exec(_ context.Context, _ string, argv []string, env map[string]string) ([]byte, error) {
	s.ev.add("exec")
	s.mu.Lock()
	s.argv = append(s.argv, append([]string(nil), argv...))
	s.mu.Unlock()
	// The sandbox has the repository at its working directory; here the test
	// redirects the CLI with --root instead, and the spec path is rewritten to
	// the file Put wrote.
	args := []string{"--root", s.repoRoot}
	// The controller prefixes the command with the binary name; here the CLI is
	// already this process, so the prefix is dropped.
	command := argv
	if len(command) > 0 && command[0] == "llmbench" {
		command = command[1:]
	}
	for i := 0; i < len(command); i++ {
		if command[i] == "--job" && i+1 < len(command) {
			args = append(args, "--job", filepath.Join(s.dir, filepath.Base(command[i+1])))
			i++
			continue
		}
		args = append(args, command[i])
	}
	for k, v := range env {
		if k == controller.SandboxTokenEnv {
			// The CLI reads the token from its environment, which is how the
			// controller hands it over in the real sandbox.
			os.Setenv(k, v)
		}
	}
	cmd := newRootCmd()
	cmd.SetArgs(args)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(""))
	if err := cmd.Execute(); err != nil {
		return nil, fmt.Errorf("the benchmark CLI failed: %w (stderr: %s)", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (s *cliSandbox) Delete(context.Context, string) (bool, error) {
	s.ev.add("delete")
	return true, nil
}

// ReadAgentResult is not used by a benchmark job; the optimizer path has its
// own tests.
func (s *cliSandbox) ReadAgentResult(context.Context, string) (controller.AgentResult, bool, error) {
	return controller.AgentResult{}, false, nil
}

func (s *cliSandbox) lastArgv() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.argv) == 0 {
		return nil
	}
	return s.argv[len(s.argv)-1]
}

// rig wires the controller with the fakes.
type rig struct {
	logs   []string
	root   string
	remote string
	gw     *gateway
	lease  *memoryLease
	ev     *events
	box    *cliSandbox
	ctrl   *controller.Controller
}

func newRig(t *testing.T, issues ...controller.Issue) *rig {
	t.Helper()
	root, remote := repository(t)
	ev := &events{}
	gw := newGateway(ev, "llmbench:claimed", issues...)
	box := newCLISandbox(t, ev, root)
	r := &rig{
		root: root, remote: remote, gw: gw, lease: &memoryLease{ev: ev}, ev: ev, box: box,
	}
	t.Setenv(controller.SandboxTokenEnv, e2eToken)
	// The collectors sample every 500ms in production, which a real request
	// exceeds; this test's workload is milliseconds long, so it asks for a
	// shorter interval through the same operator knob a real sandbox would use.
	t.Setenv("LLMBENCH_SAMPLE_INTERVAL", "2ms")
	// The CLI really starts the runtime process; here that process is a stub
	// that does nothing, because the runtime it talks to is the httptest server
	// already listening on the port the job spec names.
	stub := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, "llama-server"), []byte("#!/bin/sh\nsleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The harness identifies the machine with nvidia-smi, so the test provides
	// one: the environment is part of the result's identity and the comparison
	// refuses to compare two unknown machines.
	gpu := "#!/bin/sh\necho '0, NVIDIA GeForce RTX 5090, 4200, 32768, 87, 580.1'\n"
	if err := os.WriteFile(filepath.Join(stub, "nvidia-smi"), []byte(gpu), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	r.ctrl = &controller.Controller{
		Config: controller.Config{
			Repo: "owner/repo", DefaultBranch: "main",
			Labels: operator.Labels{
				Benchmark: "llmbench:benchmark", Optimize: "llmbench:optimize",
				Claimed: "llmbench:claimed", Done: "llmbench:done", Failed: "llmbench:failed",
			},
			Models: []operator.MVPModel{{ID: "model-a", Path: "/models/a", Digest: strings.Repeat("d", 64)}},
			Constraints: job.Constraints{
				Engines: []string{"llamacpp"}, Images: []string{imageA, imageB},
				Models: []string{"model-a"}, OutputRoots: []string{"experiments"},
			},
			ReservedArgs:   map[string][]string{"llamacpp": {"--model", "--host", "--port", "--metrics"}},
			LLMBench:       []string{"llmbench"},
			HolderIdentity: "e2e",
			LeaseDuration:  time.Minute,
			GitToken:       func(context.Context) (string, error) { return e2eToken, nil },
		},
		Gateway: gw,
		Lease:   r.lease,
		Pauser:  &recordingPauser{ev: ev},
		Sandbox: box,
		Now:     func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) },
		// The lease keeper logs from another goroutine, where testing.T is not
		// safe after the test returns.
		Logf: func(format string, args ...any) { r.logs = append(r.logs, fmt.Sprintf(format, args...)) },
	}
	return r
}

// count returns how often a name appears in the effect log.
func count(events []string, name string) int {
	n := 0
	for _, e := range events {
		if e == name {
			n++
		}
	}
	return n
}

func (r *rig) runOnce(t *testing.T) {
	t.Helper()
	handled, err := r.ctrl.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("controller: %v (log: %s)", err, strings.Join(r.logs, " | "))
	}
	if !handled {
		t.Fatalf("the request was not handled (log: %s)", strings.Join(r.logs, " | "))
	}
}

// request renders a benchmark request the way the Issue form does.
func request(t *testing.T, model string, port int, image string) string {
	t.Helper()
	spec := job.Spec{
		Kind:  job.KindBenchmark,
		Model: job.Model{ID: model},
		Runtime: job.Runtime{
			Engine: "llamacpp", Image: image, Ready: job.Ready{Port: port},
		},
		Workload: job.Workload{Cases: []job.Case{{Name: "short", PromptText: "hello world", MaxTokens: 8, Repeats: 2}}},
		Metrics:  job.Metrics{Collectors: []job.Collector{job.CollectorHarness, job.CollectorRuntime}},
		Output:   job.Output{Dir: "experiments/" + model},
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("Requested by the operator.\n\n```yaml\nkind: benchmark\n")
	fmt.Fprintf(&b, "model:\n  id: %s\n", spec.Model.ID)
	fmt.Fprintf(&b, "runtime:\n  engine: %s\n  image: %s\n  ready:\n    port: %d\n", spec.Runtime.Engine, spec.Runtime.Image, spec.Runtime.Ready.Port)
	fmt.Fprintf(&b, "workload:\n  cases:\n    - name: short\n      prompt_text: %q\n      max_tokens: 8\n      repeats: 2\n", spec.Workload.Cases[0].PromptText)
	fmt.Fprintf(&b, "metrics:\n  collectors: [harness, runtime]\n")
	fmt.Fprintf(&b, "output:\n  dir: %s\n", spec.Output.Dir)
	b.WriteString("```\n")
	return b.String()
}

func TestBenchmarkIssueBecomesAMeasuredPullRequest(t *testing.T) {
	host, port, closeRuntime := fakeRuntime(t, 3*time.Millisecond)
	defer closeRuntime()
	r := newRig(t, controller.Issue{
		Number: 42, Title: "benchmark model-a",
		Body:   request(t, "model-a", port, imageA),
		Labels: []string{"llmbench:benchmark"},
	})
	r.ctrl.Config.Constraints.OutputRoots = []string{"experiments"}
	_ = host
	r.runOnce(t)

	// The external effects happened in the order the design requires: the
	// workload is paused before the sandbox exists, the sandbox is created
	// before the measurement and deleted before the workload comes back.
	// The lease is taken *before* the Issue is claimed (it is the job mutex),
	// the workload is paused before the sandbox exists, and the lease is
	// released only after the workload is restored.
	wantOrder := []string{
		"lease-acquire", "claim", "pause", "ensure", "put", "exec", "delete", "restore", "lease-release",
	}
	if got := r.ev.snapshot(); !slices.Equal(got, wantOrder) {
		t.Fatalf("effects = %v, want %v", got, wantOrder)
	}
	// The controller asked the CLI for the things the CLI needs, and the
	// command that ran is the real one.
	argv := r.box.lastArgv()
	for _, want := range []string{"benchmark", "--job", "--model-path", "/models/a", "--model-digest", "--job-id", "--push", "--json"} {
		if !slices.Contains(argv, want) {
			t.Fatalf("argv %v is missing %q", argv, want)
		}
	}
	// The result PR carries facts and points at the pushed branch.
	prs := r.gw.pullRequests()
	if len(prs) != 1 {
		t.Fatalf("pull requests = %+v (log: %s)", prs, strings.Join(r.logs, " | "))
	}
	pr := prs[0]
	if pr.Head != "llmbench/2026-09-27-issue42" || pr.Base != "main" {
		t.Fatalf("pull request = %+v", pr)
	}
	if !strings.Contains(pr.Body, "result digest: `") || !strings.Contains(pr.Body, "measurement valid: true") {
		t.Fatalf("pull request body = %q", pr.Body)
	}
	for _, forbidden := range []string{"accept", "reject", "better", "recommended"} {
		if strings.Contains(strings.ToLower(pr.Body), forbidden) {
			t.Fatalf("pull request body carries a verdict (%q): %q", forbidden, pr.Body)
		}
	}
	// The labels mirror the outcome.
	if !r.gw.has(42, "llmbench:done") || r.gw.has(42, "llmbench:claimed") || r.gw.has(42, "llmbench:failed") {
		t.Fatalf("labels = %v", r.gw.labels[42])
	}
	// The GPU is free, and the phase record reached the release.
	phases := r.lease.phaseList()
	if len(phases) == 0 || phases[len(phases)-1] != lease.PhaseReleased {
		t.Fatalf("phases = %v", phases)
	}
	// The branch is on the remote and the pushed result verifies.
	if branches := gitCmd(t, r.remote, "branch", "--list"); !strings.Contains(branches, "llmbench/2026-09-27-issue42") {
		t.Fatalf("remote branches = %q", branches)
	}
	resultDir := filepath.Join(r.root, "experiments", "model-a", "2026-09-27-issue42")
	loaded, err := measurement.VerifyResultDir(resultDir)
	if err != nil {
		t.Fatalf("the pushed result does not verify: %v", err)
	}
	if !loaded.MeasurementValid {
		t.Fatalf("measurement invalid: %v", loaded.InvalidReasons)
	}
	if loaded.Inputs.ModelDigest != strings.Repeat("d", 64) {
		t.Fatalf("model digest = %q", loaded.Inputs.ModelDigest)
	}
	if loaded.Runtime.BuildDigest != imageA[len("ghcr.io/example/llama.cpp@"):] {
		t.Fatalf("build digest = %q", loaded.Runtime.BuildDigest)
	}
	// The commit the CLI reported travelled through the controller into the
	// durable lease record, and it is the commit that is really on the remote.
	remoteSHA := strings.TrimSpace(gitCmd(t, r.remote, "rev-parse", "refs/heads/llmbench/2026-09-27-issue42"))
	if remoteSHA == "" {
		t.Fatal("the branch has no commit on the remote")
	}
	if got := r.lease.recordSnapshot().Commit; got != remoteSHA {
		t.Fatalf("the durable record has commit %q, the remote has %q", got, remoteSHA)
	}
}

func TestTwoMeasuredRunsWithDifferentBuildsAreComparable(t *testing.T) {
	// Two runtime builds, the same model and workload: the comparison must
	// report a real movement and no incomparability, because the build is the
	// only thing that differs.
	slowHost, slowPort, closeSlow := fakeRuntime(t, 15*time.Millisecond)
	defer closeSlow()
	fastHost, fastPort, closeFast := fakeRuntime(t, 1*time.Millisecond)
	defer closeFast()
	_ = slowHost
	_ = fastHost

	r := newRig(t,
		controller.Issue{Number: 1, Body: request(t, "model-a", slowPort, imageA), Labels: []string{"llmbench:benchmark"}},
		controller.Issue{Number: 2, Body: request(t, "model-a", fastPort, imageB), Labels: []string{"llmbench:benchmark"}},
	)
	r.runOnce(t)
	r.runOnce(t)
	// Both jobs took and released the lease, so exactly two of each appear.
	effects := r.ev.snapshot()
	if n := count(effects, "lease-acquire"); n != 2 {
		t.Fatalf("lease acquisitions = %d in %v", n, effects)
	}
	if n := count(effects, "lease-release"); n != 2 {
		t.Fatalf("lease releases = %d in %v", n, effects)
	}

	baseline := filepath.Join(r.root, "experiments", "model-a", "2026-09-27-issue1")
	candidate := filepath.Join(r.root, "experiments", "model-a", "2026-09-27-issue2")
	got, err := compare.Compare(baseline, candidate, compare.KindRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Comparable || len(got.Reasons) != 0 {
		t.Fatalf("the pair should be comparable: %v", got.Reasons)
	}
	var ttft, decode *compare.MetricDelta
	for i := range got.Metrics {
		d := &got.Metrics[i]
		switch d.Name {
		case "ttft_ms":
			ttft = d
		case "decode_tok_per_s":
			decode = d
		}
	}
	if ttft == nil || decode == nil {
		t.Fatalf("the comparison is missing the harness metrics: %+v", got.Metrics)
	}
	// The faster runtime streamed its tokens sooner, so the latency moved: a
	// comparison that reports no movement would not be measuring anything.
	if ttft.AbsChange == nil || *ttft.AbsChange >= 0 {
		t.Fatalf("ttft delta = %+v", ttft)
	}
	if decode.AbsChange == nil || *decode.AbsChange == 0 {
		t.Fatalf("decode delta = %+v", decode)
	}
	// The builds differ, which is what makes this a runtime comparison.
	if got.Baseline.RuntimeBuildDigest == got.Candidate.RuntimeBuildDigest {
		t.Fatalf("both sides report build %q", got.Baseline.RuntimeBuildDigest)
	}
}
