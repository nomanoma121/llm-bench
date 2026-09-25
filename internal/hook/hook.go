// Package hook builds run.Hook implementations from operator hook plans.
// Only command hooks exist today; GitOps and sandbox claim hooks are provided
// by their own packages (gitops, sandbox) and are wired in later milestones.
package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// pendingExitCode is reserved by the controller protocol: an acquire command
// exiting 75 means "not ready yet" and must not be treated as a failure.
const pendingExitCode = 75

// Command is a command-backed hook built from a planned command hook. Both
// the acquire and release argv come from the frozen hook plan, so releases
// keep working even when the operator configuration changes mid-run.
type Command struct {
	plan operator.PlannedHook
	dir  string
}

// FromPlan builds a command hook from a plan element of kind "command".
func FromPlan(plan operator.PlannedHook, dir string) (*Command, error) {
	if plan.Kind != operator.KindCommand {
		return nil, fmt.Errorf("hook: plan %q has kind %q, want %q", plan.Name, plan.Kind, operator.KindCommand)
	}
	return &Command{plan: plan, dir: dir}, nil
}

// Name implements run.Hook.
func (c *Command) Name() string { return c.plan.Name }

// Acquire implements run.Hook.
func (c *Command) Acquire(ctx context.Context) error {
	return c.run(ctx, c.plan.Command, "acquire")
}

// Release implements run.Hook. It must tolerate being called even when the
// matching acquire never took effect.
func (c *Command) Release(ctx context.Context) error {
	return c.run(ctx, c.plan.Release, "release")
}

func (c *Command) run(ctx context.Context, argv []string, phase string) error {
	if len(argv) == 0 {
		return fmt.Errorf("hook %q: empty %s argv", c.plan.Name, phase)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = c.dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == pendingExitCode {
		return run.ErrPending
	}
	if ctx.Err() != nil {
		return fmt.Errorf("hook %q %s interrupted: %w (output: %s)", c.plan.Name, phase, ctx.Err(), tail(&out))
	}
	return fmt.Errorf("hook %q %s failed: %v (output: %s)", c.plan.Name, phase, err, tail(&out))
}

// MapPending adapts a hook that reports "not converged" with its own
// sentinel (gitops.ErrNotConverged) to the engine's run.ErrPending, keeping
// adapter packages free of policy imports.
func MapPending(inner run.Hook, notConverged error) run.Hook {
	return &pendingMapper{inner: inner, sentinel: notConverged}
}

type pendingMapper struct {
	inner    run.Hook
	sentinel error
}

func (p *pendingMapper) Name() string { return p.inner.Name() }

func (p *pendingMapper) Acquire(ctx context.Context) error {
	return p.wrap(p.inner.Acquire(ctx))
}

func (p *pendingMapper) Release(ctx context.Context) error {
	return p.wrap(p.inner.Release(ctx))
}

func (p *pendingMapper) wrap(err error) error {
	if err != nil && errors.Is(err, p.sentinel) {
		return fmt.Errorf("%s: %w", p.inner.Name(), run.ErrPending)
	}
	return err
}

func tail(b *bytes.Buffer) string {
	const max = 512
	s := b.String()
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			out = append(out, r)
		}
	}
	return string(out)
}
