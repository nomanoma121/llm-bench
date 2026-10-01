package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/github"
	"github.com/nomanoma121/llm-bench/internal/sandbox"
)

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf(format, args...))
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.events, "\n")
}

type fakeGitHub struct {
	*recorder
	issues   map[int]*github.Issue
	branches map[string]bool
}

func (g *fakeGitHub) Issues(_ context.Context, label string) ([]github.Issue, error) {
	var out []github.Issue
	for _, i := range g.issues {
		if i.Has(label) {
			out = append(out, *i)
		}
	}
	return out, nil
}

func (g *fakeGitHub) AddLabel(_ context.Context, n int, label string) error {
	g.add("label +%s", label)
	g.issues[n].Labels = append(g.issues[n].Labels, label)
	return nil
}

func (g *fakeGitHub) RemoveLabel(_ context.Context, n int, label string) error {
	g.add("label -%s", label)
	var kept []string
	for _, l := range g.issues[n].Labels {
		if l != label {
			kept = append(kept, l)
		}
	}
	g.issues[n].Labels = kept
	return nil
}

func (g *fakeGitHub) Comment(_ context.Context, _ int, body string) error {
	g.add("comment %s", strings.SplitN(body, "\n", 2)[0])
	return nil
}

func (g *fakeGitHub) BranchExists(_ context.Context, branch string) (bool, error) {
	return g.branches[branch], nil
}

func (g *fakeGitHub) OpenPullRequest(_ context.Context, head, _, _ string) (int, error) {
	g.add("pr %s", head)
	return 7, nil
}

type fakeSandbox struct {
	*recorder
	onExec   func(argv []string) sandbox.Output
	setupEnv map[string]string
}

func (s *fakeSandbox) Ensure(context.Context, string) error { s.add("sandbox ensure"); return nil }
func (s *fakeSandbox) Delete(context.Context, string) error { s.add("sandbox delete"); return nil }
func (s *fakeSandbox) Exec(_ context.Context, _ string, argv []string, env map[string]string) (sandbox.Output, error) {
	if argv[0] == "sh" {
		s.setupEnv = env
	}
	if s.onExec == nil || argv[0] == "sh" {
		return sandbox.Output{}, nil
	}
	return s.onExec(argv), nil
}

type fakeGitOps struct {
	*recorder
	pauseErr error
}

func (g *fakeGitOps) Pause(context.Context, string) (bool, error) {
	g.add("pause")
	return g.pauseErr == nil, g.pauseErr
}

func (g *fakeGitOps) Restore(context.Context, string) (bool, error) {
	g.add("restore")
	return true, nil
}

const benchmarkIssue = "```yaml\nkind: benchmark\nmodel: m\nruntime: {engine: llamacpp}\nworkload: [{name: a, prompt_text: hi, max_tokens: 8}]\n```"

func setup(labels ...string) (*Controller, *fakeGitHub, *fakeSandbox, *fakeGitOps, *recorder) {
	rec := &recorder{}
	created := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	gh := &fakeGitHub{recorder: rec, branches: map[string]bool{}, issues: map[int]*github.Issue{
		1: {Number: 1, Body: benchmarkIssue, Labels: labels, CreatedAt: created},
	}}
	sb := &fakeSandbox{recorder: rec}
	ops := &fakeGitOps{recorder: rec}
	c := &Controller{
		GitHub: gh, Sandbox: sb, GitOps: ops,
		LLMBench:     []string{"llmbench"},
		GitToken:     func(context.Context) (string, error) { return "t", nil },
		Interval:     time.Millisecond,
		PauseTimeout: 20 * time.Millisecond,
		Logf:         func(string, ...any) {},
	}
	return c, gh, sb, ops, rec
}

func TestBenchmarkOpensPRThenRestores(t *testing.T) {
	c, gh, sb, _, rec := setup(LabelBenchmark)
	sb.onExec = func(argv []string) sandbox.Output {
		gh.branches["llmbench/2026-09-27-issue1"] = true
		return sandbox.Output{}
	}
	if err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"label +llmbench:running", "pause", "sandbox ensure", "pr llmbench/2026-09-27-issue1",
		"comment Completed: PR #7", "sandbox delete", "restore", "label +llmbench:done", "label -llmbench:running"}
	if got := rec.String(); got != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s", got)
	}
	if !strings.Contains(sb.setupEnv["LLMBENCH_SPEC"], "kind: benchmark") || sb.setupEnv["LLMBENCH_SPEC_PATH"] != "/tmp/llmbench-2026-09-27-issue1.yaml" {
		t.Fatalf("setup env %v", sb.setupEnv)
	}
	if err := c.Poll(context.Background()); err != nil || len(rec.events) != len(want) {
		t.Fatal("a finished issue was picked up again")
	}
}

func TestFailedPauseStillRestores(t *testing.T) {
	c, _, _, ops, rec := setup(LabelBenchmark)
	ops.pauseErr = errors.New("pause PR closed")
	if err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rec.String()
	for _, want := range []string{"sandbox delete", "restore", "label +llmbench:failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sandbox ensure") {
		t.Fatal("sandbox was created although the pause failed")
	}
}

func TestBenchmarkWithoutBranchFails(t *testing.T) {
	c, _, sb, _, rec := setup(LabelBenchmark)
	sb.onExec = func([]string) sandbox.Output { return sandbox.Output{ExitCode: 10, Stderr: "no result"} }
	if err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.String(), "label +llmbench:failed") || strings.Contains(rec.String(), "pr ") {
		t.Fatalf("events:\n%s", rec.String())
	}
}

func TestRecoverPublishesPushedBranch(t *testing.T) {
	c, gh, _, _, rec := setup(LabelBenchmark, LabelRunning)
	gh.branches["llmbench/2026-09-27-issue1"] = true
	if err := c.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rec.String()
	for _, want := range []string{"pr llmbench/2026-09-27-issue1", "sandbox delete", "restore", "label +llmbench:done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRejectsInvalidSpec(t *testing.T) {
	c, gh, _, _, rec := setup(LabelBenchmark)
	gh.issues[1].Body = strings.Replace(benchmarkIssue, "model: m", "model: ../m", 1)
	if err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.String(), "label +llmbench:failed") || strings.Contains(rec.String(), "pause") {
		t.Fatalf("events:\n%s", rec.String())
	}
}

type fakeHarness struct{ done func() }

func (h fakeHarness) Run(_ context.Context, task string, onSession func(string)) (string, error) {
	onSession("s1")
	h.done()
	return "kept the faster build", nil
}

func TestOptimizeHandsSandboxToHarness(t *testing.T) {
	c, gh, _, _, rec := setup(LabelOptimize)
	gh.issues[1].Body = strings.Replace(benchmarkIssue, "kind: benchmark", "kind: optimize\nsource: {repo: ggml-org/llama.cpp, ref: master}", 1)
	c.Harness = fakeHarness{done: func() { gh.branches["llmbench/2026-09-27-issue1"] = true }}
	if err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rec.String()
	for _, want := range []string{"comment Harness session: `s1`", "pr llmbench/2026-09-27-issue1", "restore", "label +llmbench:done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
