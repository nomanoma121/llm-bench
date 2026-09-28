package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nomanoma121/llm-bench/internal/github"
	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/sandbox"
)

const (
	LabelBenchmark = "llmbench:benchmark"
	LabelOptimize  = "llmbench:optimize"
	LabelRunning   = "llmbench:running"
	LabelDone      = "llmbench:done"
	LabelFailed    = "llmbench:failed"
)

type GitHub interface {
	Issues(ctx context.Context, label string) ([]github.Issue, error)
	AddLabel(ctx context.Context, issue int, label string) error
	RemoveLabel(ctx context.Context, issue int, label string) error
	Comment(ctx context.Context, issue int, body string) error
	BranchExists(ctx context.Context, branch string) (bool, error)
	OpenPullRequest(ctx context.Context, head, title, body string) (int, error)
}

type Sandbox interface {
	Ensure(ctx context.Context, jobID string) error
	Delete(ctx context.Context, jobID string) error
	Exec(ctx context.Context, jobID string, argv []string, env map[string]string) (sandbox.Output, error)
	Put(ctx context.Context, jobID, path string, r io.Reader) error
}

type GitOps interface {
	Pause(ctx context.Context, jobID string) (bool, error)
	Restore(ctx context.Context, jobID string) (bool, error)
}

type Harness interface {
	Run(ctx context.Context, task string, onSession func(id string)) (string, error)
}

type Controller struct {
	GitHub  GitHub
	Sandbox Sandbox
	GitOps  GitOps
	Harness Harness

	Repository   string
	Workdir      string
	LLMBench     []string
	GitToken     func(ctx context.Context) (string, error)
	Interval     time.Duration
	PauseTimeout time.Duration
	Logf         func(format string, args ...any)
}

type Job struct {
	ID    string
	Issue github.Issue
	Spec  job.Spec
}

func (j Job) Branch() string   { return "llmbench/" + j.ID }
func (j Job) SpecPath() string { return "/tmp/llmbench-" + j.ID + ".yaml" }

func JobID(issue github.Issue) string {
	return fmt.Sprintf("%s-issue%d", issue.CreatedAt.UTC().Format("2006-01-02"), issue.Number)
}

func (c *Controller) Run(ctx context.Context) error {
	if err := c.recover(ctx); err != nil {
		c.Logf("recover: %v", err)
	}
	for {
		if err := c.Poll(ctx); err != nil {
			c.Logf("poll: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(c.Interval):
		}
	}
}

func (c *Controller) Poll(ctx context.Context) error {
	var pending []github.Issue
	for _, label := range []string{LabelBenchmark, LabelOptimize} {
		issues, err := c.GitHub.Issues(ctx, label)
		if err != nil {
			return err
		}
		for _, i := range issues {
			if !i.Has(LabelRunning) && !i.Has(LabelDone) && !i.Has(LabelFailed) {
				pending = append(pending, i)
			}
		}
	}
	if len(pending) == 0 {
		return nil
	}
	sort.Slice(pending, func(a, b int) bool { return pending[a].Number < pending[b].Number })
	issue := pending[0]

	j, err := c.newJob(issue)
	if err != nil {
		_ = c.GitHub.Comment(ctx, issue.Number, "llmbench rejected this request:\n\n```\n"+err.Error()+"\n```")
		return c.GitHub.AddLabel(ctx, issue.Number, LabelFailed)
	}
	if err := c.GitHub.AddLabel(ctx, issue.Number, LabelRunning); err != nil {
		return err
	}
	c.Logf("job %s: started", j.ID)
	c.finish(ctx, j, c.execute(ctx, j))
	return nil
}

func (c *Controller) recover(ctx context.Context) error {
	issues, err := c.GitHub.Issues(ctx, LabelRunning)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		j := Job{ID: JobID(issue), Issue: issue}
		if issue.Has(LabelDone) || issue.Has(LabelFailed) {
			_ = c.GitHub.RemoveLabel(ctx, issue.Number, LabelRunning)
			continue
		}
		c.Logf("job %s: recovering after a restart", j.ID)
		err := c.publish(ctx, j, "Recovered after a controller restart.")
		if err != nil {
			err = fmt.Errorf("the controller restarted while this job was running: %w", err)
		}
		c.finish(ctx, j, err)
	}
	return nil
}

func (c *Controller) newJob(issue github.Issue) (Job, error) {
	spec, err := job.FromIssueBody(issue.Body)
	if err != nil {
		return Job{}, err
	}
	return Job{ID: JobID(issue), Issue: issue, Spec: spec}, nil
}

func (c *Controller) execute(ctx context.Context, j Job) error {
	pauseCtx, cancel := context.WithTimeout(ctx, c.PauseTimeout)
	defer cancel()
	if err := c.until(pauseCtx, "pause", func() (bool, error) { return c.GitOps.Pause(pauseCtx, j.ID) }); err != nil {
		return fmt.Errorf("pause the inference workload: %w", err)
	}
	if err := c.prepare(ctx, j); err != nil {
		return fmt.Errorf("prepare the sandbox: %w", err)
	}
	var note string
	var err error
	switch j.Spec.Kind {
	case job.Benchmark:
		err = c.benchmark(ctx, j)
	case job.Optimize:
		note, err = c.optimize(ctx, j)
	}
	if err != nil {
		return err
	}
	return c.publish(ctx, j, note)
}

const setupScript = `set -e
git config --global user.name 'llmbench[bot]'
git config --global user.email 'llmbench[bot]@users.noreply.github.com'
git config --global credential.helper store
printf 'https://x-access-token:%s@github.com\n' "$LLMBENCH_GIT_TOKEN" > ~/.git-credentials
[ -d "$LLMBENCH_WORKDIR/.git" ] || git clone "https://github.com/$LLMBENCH_REPOSITORY" "$LLMBENCH_WORKDIR"
`

func (c *Controller) prepare(ctx context.Context, j Job) error {
	ensureCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := c.Sandbox.Ensure(ensureCtx, j.ID); err != nil {
		return err
	}
	token, err := c.GitToken(ctx)
	if err != nil {
		return err
	}
	if _, err := c.exec(ctx, j, []string{"sh", "-c", setupScript}, map[string]string{
		"LLMBENCH_GIT_TOKEN":  token,
		"LLMBENCH_WORKDIR":    c.Workdir,
		"LLMBENCH_REPOSITORY": c.Repository,
	}); err != nil {
		return err
	}
	spec, err := yaml.Marshal(j.Spec)
	if err != nil {
		return err
	}
	return c.Sandbox.Put(ctx, j.ID, j.SpecPath(), bytes.NewReader(spec))
}

func (c *Controller) benchmarkArgv(j Job) []string {
	return append(append([]string(nil), c.LLMBench...),
		"benchmark",
		"--job", j.SpecPath(),
		"--job-id", j.ID,
		"--root", c.Workdir,
	)
}

func (c *Controller) benchmark(ctx context.Context, j Job) error {
	_, err := c.exec(ctx, j, append(c.benchmarkArgv(j), "--push"), nil)
	return err
}

func (c *Controller) optimize(ctx context.Context, j Job) (string, error) {
	if c.Harness == nil {
		return "", errors.New("no harness is configured")
	}
	agentCtx, stop := context.WithCancel(ctx)
	defer stop()
	go c.keepSandbox(agentCtx, j)
	return c.Harness.Run(agentCtx, c.task(j), func(session string) {
		c.Logf("job %s: harness session %s", j.ID, session)
		_ = c.GitHub.Comment(ctx, j.Issue.Number, fmt.Sprintf("Harness session: `%s`", session))
	})
}

func (c *Controller) keepSandbox(ctx context.Context, j Job) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		if err := c.prepare(ctx, j); err != nil && ctx.Err() == nil {
			c.Logf("job %s: keep sandbox: %v", j.ID, err)
		}
	}
}

func (c *Controller) task(j Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Optimize the %s runtime for model %s. Job %s, requested in issue #%d.\n\n", j.Spec.Runtime.Engine, j.Spec.ModelName(), j.ID, j.Issue.Number)
	if s := j.Spec.Source; s != nil {
		fmt.Fprintf(&b, "Runtime source: https://github.com/%s (ref %s).\n", s.Repo, s.Ref)
	}
	if bud := j.Spec.Budget; bud != nil {
		fmt.Fprintf(&b, "Budget: at most %d benchmark runs.\n", bud.MaxRounds)
	}
	fmt.Fprintf(&b, `
A GPU sandbox is running for this job. If it dies it is recreated with the same name, so retry.
  llmbench sandbox exec %[1]s -- <argv...>
  llmbench sandbox put %[1]s <local-file> <sandbox-path>
  llmbench sandbox get %[1]s <sandbox-path>

Inside the sandbox the llm-bench checkout is %[2]s (git push works from there) and the job spec is %[3]s.
Measure only with llmbench benchmark; never write results by hand:
  %[4]s --out <dir> [--bin <your built server binary> --source <its source checkout>]
Compare two results with: llmbench compare <baseline-dir> <candidate-dir>
compare reports facts only. Deciding what to keep is your job.

When you are done, commit the final runtime change and the final measurement in experiments/%[5]s/%[1]s/ to the branch %[6]s and push it.
That branch is how the controller knows you finished; it opens the pull request. If you give up, stop without pushing it.
`, j.ID, c.Workdir, j.SpecPath(), strings.Join(c.benchmarkArgv(j), " "), j.Spec.ModelName(), j.Branch())
	return b.String()
}

func (c *Controller) publish(ctx context.Context, j Job, note string) error {
	ok, err := c.GitHub.BranchExists(ctx, j.Branch())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no result was pushed to %s", j.Branch())
	}
	body := fmt.Sprintf("Closes #%d\n\n%s\n", j.Issue.Number, note)
	pr, err := c.GitHub.OpenPullRequest(ctx, j.Branch(), fmt.Sprintf("llmbench: %s", j.ID), body)
	if err != nil {
		return err
	}
	return c.GitHub.Comment(ctx, j.Issue.Number, fmt.Sprintf("Completed: PR #%d", pr))
}

func (c *Controller) finish(ctx context.Context, j Job, jobErr error) {
	ctx = context.WithoutCancel(ctx)
	_ = c.until(ctx, "delete sandbox", func() (bool, error) { return true, c.Sandbox.Delete(ctx, j.ID) })
	_ = c.until(ctx, "restore", func() (bool, error) { return c.GitOps.Restore(ctx, j.ID) })
	label := LabelDone
	if jobErr != nil {
		c.Logf("job %s: failed: %v", j.ID, jobErr)
		label = LabelFailed
		_ = c.GitHub.Comment(ctx, j.Issue.Number, "llmbench failed:\n\n```\n"+jobErr.Error()+"\n```")
	}
	_ = c.until(ctx, "label", func() (bool, error) {
		if err := c.GitHub.AddLabel(ctx, j.Issue.Number, label); err != nil {
			return false, err
		}
		return true, c.GitHub.RemoveLabel(ctx, j.Issue.Number, LabelRunning)
	})
	c.Logf("job %s: %s", j.ID, label)
}

func (c *Controller) until(ctx context.Context, what string, step func() (bool, error)) error {
	for {
		done, err := step()
		if err == nil && done {
			return nil
		}
		if err != nil {
			c.Logf("%s: %v", what, err)
		}
		select {
		case <-ctx.Done():
			if err == nil {
				err = ctx.Err()
			}
			return err
		case <-time.After(c.Interval):
		}
	}
}

func (c *Controller) exec(ctx context.Context, j Job, argv []string, env map[string]string) (sandbox.Output, error) {
	out, err := c.Sandbox.Exec(ctx, j.ID, argv, env)
	if err != nil {
		return out, err
	}
	if out.ExitCode != 0 {
		stderr := out.Stderr
		if len(stderr) > 2000 {
			stderr = stderr[len(stderr)-2000:]
		}
		return out, fmt.Errorf("%s exited with %d: %s", argv[0], out.ExitCode, stderr)
	}
	return out, nil
}
