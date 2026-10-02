// Package harness runs coding agents (opencode, pi, DSH, Hermes) once and
// non-interactively against a runtime for a benchmark. Each harness pins the
// mise tools it needs; mise installs them in the sandbox.
package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Harness says how to run one coding agent.
type Harness interface {
	// Tools are the mise tools it needs, each with its version.
	Tools() []string
	// Command builds a single non-interactive run.
	Command(p Params) (Invocation, error)
}

// Params describe the run a harness is asked for.
type Params struct {
	// Prompt is the task.
	Prompt string
	// BaseURL is the OpenAI-compatible endpoint, ending in /v1.
	BaseURL string
	// Model is the model name the runtime serves.
	Model string
	// Context is the runtime's context length in tokens, 0 when unknown.
	Context int
	// Home is the harness's own home directory.
	Home string
}

// Invocation is a command line with the environment and the files under
// Params.Home it needs.
type Invocation struct {
	Argv  []string
	Env   map[string]string
	Files map[string][]byte
}

// apiKey is what harnesses send as their key; the runtime ignores it.
const apiKey = "llmbench"

// maxOutputTokens is the longest answer a harness may ask for.
const maxOutputTokens = 32768

func New(name string) (Harness, error) {
	switch name {
	case "opencode":
		return openCode{}, nil
	case "pi":
		return pi{}, nil
	case "dsh":
		return dsh{}, nil
	case "hermes":
		return hermes{}, nil
	}
	return nil, fmt.Errorf("harness: unknown %q (opencode, pi, dsh, hermes)", name)
}

// Mise installs and runs harness tools. The data directory is fixed so the
// tools installed once are found again under each harness's own home.
type Mise struct {
	DataDir string
}

func (m Mise) cmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "mise", args...)
	cmd.Env = append(os.Environ(), "MISE_DATA_DIR="+m.DataDir, "MISE_YES=1")
	return cmd
}

func (m Mise) Install(ctx context.Context, h Harness) error {
	out, err := m.cmd(ctx, append([]string{"install"}, h.Tools()...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mise install: %w\n%s", err, tail(out))
	}
	return nil
}

// Run runs the harness in workdir until it exits or ctx ends, writing its
// output to log. A harness that exits non-zero has still been measured.
func (m Mise) Run(ctx context.Context, h Harness, p Params, workdir string, log io.Writer) error {
	inv, err := h.Command(p)
	if err != nil {
		return err
	}
	for path, content := range inv.Files {
		full := filepath.Join(p.Home, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return err
		}
	}
	args := append([]string{"exec"}, h.Tools()...)
	cmd := m.cmd(ctx, append(append(args, "--"), inv.Argv...)...)
	cmd.Dir = workdir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(cmd.Env, "HOME="+p.Home, "XDG_CONFIG_HOME="+filepath.Join(p.Home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(p.Home, ".local", "share"), "XDG_CACHE_HOME="+filepath.Join(p.Home, ".cache"))
	for k, v := range inv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	err = cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("harness stopped: %w", ctx.Err())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("harness exited with %d", exit.ExitCode())
	}
	return err
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 2000 {
		s = s[len(s)-2000:]
	}
	return s
}
