// Package harness runs coding agents once, non-interactively, against a
// runtime for a benchmark.
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

type Harness interface {
	// Tools are mise tool specs, pinned.
	Tools() []string
	Command(p Params) (Invocation, error)
}

type Params struct {
	Prompt string
	// BaseURL ends in /v1.
	BaseURL string
	Model   string
	// Context is 0 when the runtime does not say.
	Context int
	Home    string
	// Args and Env come from the job spec and are added to the harness's own.
	Args []string
	Env  map[string]string
}

// Invocation runs Argv, then the job's extra arguments, then Task.
type Invocation struct {
	Argv  []string
	Task  []string
	Env   map[string]string
	Files map[string][]byte
}

const (
	apiKey          = "llmbench"
	maxOutputTokens = 32768
)

var Names = []string{"opencode", "pi", "dsh", "hermes"}

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
	return nil, fmt.Errorf("harness: unknown %q (%s)", name, strings.Join(Names, ", "))
}

// Mise keeps its data in one place so tools installed once are found again
// under each harness's own HOME.
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
	args := append(append([]string{"exec"}, h.Tools()...), "--")
	args = append(append(append(args, inv.Argv...), p.Args...), inv.Task...)
	cmd := m.cmd(ctx, args...)
	cmd.Dir = workdir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(cmd.Env, "HOME="+p.Home, "XDG_CONFIG_HOME="+filepath.Join(p.Home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(p.Home, ".local", "share"), "XDG_CACHE_HOME="+filepath.Join(p.Home, ".cache"))
	for _, env := range []map[string]string{inv.Env, p.Env} {
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
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
