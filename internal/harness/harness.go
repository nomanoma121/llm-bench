// Package harness runs coding agents (opencode, pi, ...) against a runtime
// for a benchmark. A harness is defined in harnesses/harnesses.yaml and
// installed with mise from harnesses/mise.toml, so adding one needs no code.
package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Dir is where the harness definitions live, relative to the repository.
const Dir = "harnesses"

// Definition says how to run one harness non-interactively. Command, Env and
// Files are templates over Params.
type Definition struct {
	// Tool is the mise tool that provides the harness; its version is recorded.
	Tool    string            `yaml:"tool"`
	Command []string          `yaml:"command"`
	Env     map[string]string `yaml:"env,omitempty"`
	// Files are written under the harness's home before it starts, for
	// harnesses that read their settings from a file.
	Files map[string]string `yaml:"files,omitempty"`
}

// Params are what a definition's templates can use.
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

func Load(root string) (map[string]Definition, error) {
	b, err := os.ReadFile(filepath.Join(root, Dir, "harnesses.yaml"))
	if err != nil {
		return nil, err
	}
	defs := map[string]Definition{}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&defs); err != nil {
		return nil, fmt.Errorf("%s/harnesses.yaml: %w", Dir, err)
	}
	for name, d := range defs {
		if d.Tool == "" || len(d.Command) == 0 {
			return nil, fmt.Errorf("%s/harnesses.yaml: %s needs tool and command", Dir, name)
		}
	}
	return defs, nil
}

// Mise runs mise on the harness tool set. The data directory is fixed so the
// tools installed once are found again under each harness's own home.
type Mise struct {
	Root    string
	DataDir string
}

func (m Mise) cmd(ctx context.Context, args ...string) *exec.Cmd {
	// Absolute, because a harness runs in its own working directory.
	root, _ := filepath.Abs(m.Root)
	cmd := exec.CommandContext(ctx, "mise", append([]string{"-C", filepath.Join(root, Dir)}, args...)...)
	cmd.Env = append(os.Environ(),
		"MISE_DATA_DIR="+m.DataDir,
		// mise also reads the configs above harnesses/; the repository's own
		// mise.toml holds the development tools, not the harnesses.
		"MISE_IGNORED_CONFIG_PATHS="+filepath.Join(root, "mise.toml"),
		"MISE_TRUSTED_CONFIG_PATHS="+filepath.Join(root, Dir),
		"MISE_YES=1",
	)
	return cmd
}

func (m Mise) Install(ctx context.Context) error {
	out, err := m.cmd(ctx, "install").CombinedOutput()
	if err != nil {
		return fmt.Errorf("mise install: %w\n%s", err, tail(out))
	}
	return nil
}

func (m Mise) Version(ctx context.Context, tool string) string {
	out, err := m.cmd(ctx, "current", tool).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Run runs the harness in workdir until it exits or ctx ends, writing its
// output to log. A harness that exits non-zero has still been measured.
func (m Mise) Run(ctx context.Context, d Definition, p Params, workdir string, log io.Writer) error {
	r, err := d.render(p)
	if err != nil {
		return err
	}
	for path, content := range r.files {
		full := filepath.Join(p.Home, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	cmd := m.cmd(ctx, append([]string{"exec", "--"}, r.argv...)...)
	cmd.Dir = workdir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(cmd.Env, "HOME="+p.Home, "XDG_CONFIG_HOME="+filepath.Join(p.Home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(p.Home, ".local", "share"), "XDG_CACHE_HOME="+filepath.Join(p.Home, ".cache"))
	cmd.Env = append(cmd.Env, r.env...)
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

type rendered struct {
	argv  []string
	env   []string
	files map[string]string
}

func (d Definition) render(p Params) (rendered, error) {
	exec := func(s string) (string, error) {
		t, err := template.New("").Option("missingkey=error").Parse(s)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		err = t.Execute(&b, p)
		return b.String(), err
	}
	r := rendered{files: map[string]string{}}
	for _, a := range d.Command {
		v, err := exec(a)
		if err != nil {
			return rendered{}, fmt.Errorf("command: %w", err)
		}
		r.argv = append(r.argv, v)
	}
	for k, tmpl := range d.Env {
		v, err := exec(tmpl)
		if err != nil {
			return rendered{}, fmt.Errorf("env %s: %w", k, err)
		}
		r.env = append(r.env, k+"="+v)
	}
	for path, tmpl := range d.Files {
		v, err := exec(tmpl)
		if err != nil {
			return rendered{}, fmt.Errorf("file %s: %w", path, err)
		}
		r.files[path] = v
	}
	return r, nil
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 2000 {
		s = s[len(s)-2000:]
	}
	return s
}
