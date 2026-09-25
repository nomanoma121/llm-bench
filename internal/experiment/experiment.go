// Package experiment parses and validates experiment recipe files stored at
// experiments/<model-id>/<experiment-id>/config.yaml.
//
// An experiment recipe is intentionally restricted: it names the model, the
// benchmark prompt, an operator-allowlisted target ID, runtime settings and the
// invocation. It cannot express privileged settings such as GitOps paths, GPU
// pools or credentials; those live in operator configuration only.
package experiment

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Start describes how to build and launch the runtime under test. The runtime
// is expected to keep running in the foreground until stopped; Argv must
// therefore launch a long-lived process.
type Start struct {
	// Argv launches the runtime. Executed as-is without a shell.
	Argv []string `yaml:"argv" json:"argv"`
	// ReadyTimeoutSeconds bounds how long the runner polls ReadyArgv for a
	// zero exit status. Zero means "use the default" (300s); it is clamped to
	// the operator's MaxReadyDuration.
	ReadyTimeoutSeconds int `yaml:"ready_timeout_seconds" json:"ready_timeout_seconds"`
	// ReadyArgv is executed repeatedly until it exits zero. Optional: when
	// empty, the runner does not wait for readiness.
	ReadyArgv []string `yaml:"ready_argv" json:"ready_argv"`
}

// Runtime groups the model-facing runtime settings of the recipe.
type Runtime struct {
	Engine      string `yaml:"engine" json:"engine"`
	Variant     string `yaml:"variant" json:"variant"`
	ContextSize int    `yaml:"context_size" json:"context_size"`
	Start       *Start `yaml:"start,omitempty" json:"start,omitempty"`
}

// Invoke is the command whose output is benchmarked.
type Invoke struct {
	// Argv is executed without a shell. The runner records stdout/stderr as
	// run artifacts.
	Argv []string `yaml:"argv" json:"argv"`
}

// Config is the parsed experiment recipe.
type Config struct {
	Model     string  `yaml:"model" json:"model"`
	Benchmark string  `yaml:"benchmark" json:"benchmark"`
	Target    string  `yaml:"target" json:"target"`
	Runtime   Runtime `yaml:"runtime" json:"runtime"`
	Invoke    Invoke  `yaml:"invoke" json:"invoke"`
}

// DefaultReadyTimeoutSeconds is used when Start.ReadyTimeoutSeconds is zero.
const DefaultReadyTimeoutSeconds = 300

// Parse reads an experiment recipe. Unknown fields are rejected so that
// typos fail loudly instead of silently changing the run.
func Parse(r io.Reader) (Config, error) {
	var c Config
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("experiment: parse: %w", err)
	}
	return c, nil
}

// Load reads and parses the recipe at path.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("experiment: load: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// Validate checks the recipe against files under root. It does not consult
// operator configuration; target allowlisting happens in the operator package.
func Validate(c Config, root string) error {
	var errs []error
	if c.Model == "" {
		errs = append(errs, errors.New("model is required"))
	}
	if c.Benchmark == "" {
		errs = append(errs, errors.New("benchmark is required"))
	} else if _, err := os.Stat(BenchmarkPath(c, root)); err != nil {
		errs = append(errs, fmt.Errorf("benchmark prompt %q is missing: %w", c.Benchmark, err))
	}
	if c.Target == "" {
		errs = append(errs, errors.New("target is required"))
	}
	if c.Runtime.Engine == "" {
		errs = append(errs, errors.New("runtime.engine is required"))
	}
	if c.Runtime.ContextSize <= 0 {
		errs = append(errs, errors.New("runtime.context_size must be positive"))
	}
	if len(c.Invoke.Argv) == 0 {
		errs = append(errs, errors.New("invoke.argv is required"))
	}
	if s := c.Runtime.Start; s != nil {
		if len(s.Argv) == 0 {
			errs = append(errs, errors.New("runtime.start.argv is required when start is set"))
		}
		if s.ReadyTimeoutSeconds < 0 {
			errs = append(errs, errors.New("runtime.start.ready_timeout_seconds must not be negative"))
		}
		if len(s.ReadyArgv) == 0 && s.ReadyTimeoutSeconds > 0 {
			errs = append(errs, errors.New("runtime.start.ready_argv is required when ready_timeout_seconds is set"))
		}
	}
	return errors.Join(errs...)
}

// BenchmarkPath resolves the benchmark prompt path against root.
func BenchmarkPath(c Config, root string) string {
	return filepath.Join(root, filepath.FromSlash(c.Benchmark))
}

// CanonicalJSON returns a deterministic serialization of the recipe. It is
// stored in the run record as the recipe snapshot, so run restarts never
// re-read the mutable file on disk.
func CanonicalJSON(c Config) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("experiment: canonical json: %w", err)
	}
	return b, nil
}

// ReadyTimeout resolves the effective readiness timeout in seconds.
func (s *Start) ReadyTimeout() int {
	if s.ReadyTimeoutSeconds <= 0 {
		return DefaultReadyTimeoutSeconds
	}
	return s.ReadyTimeoutSeconds
}
