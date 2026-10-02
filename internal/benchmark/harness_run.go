package benchmark

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/nomanoma121/llm-bench/internal/harness"
	"github.com/nomanoma121/llm-bench/internal/job"
)

// harnessRunner holds what every harness session of a run shares: the
// definitions, the installed tools and the proxy they reach the runtime by.
type harnessRunner struct {
	defs     map[string]harness.Definition
	mise     harness.Mise
	proxy    *proxy
	versions map[string]string
}

func (r *run) harnessRunner(ctx context.Context, sampling job.Sampling) (*harnessRunner, error) {
	if r.harnesses != nil {
		return r.harnesses, nil
	}
	defs, err := harness.Load(r.cfg.Root)
	if err != nil {
		return nil, err
	}
	m := harness.Mise{Root: r.cfg.Root, DataDir: filepath.Join(os.TempDir(), "llmbench-mise")}
	r.cfg.Logf("installing harnesses")
	if err := m.Install(ctx); err != nil {
		return nil, err
	}
	p, err := startProxy(r.adapter.BaseURL(), sampling)
	if err != nil {
		return nil, err
	}
	r.harnesses = &harnessRunner{defs: defs, mise: m, proxy: p, versions: map[string]string{}}
	return r.harnesses, nil
}

// runHarness gives the prompt to a harness and measures every request it
// makes. The files it leaves in its working directory are the output.
func (r *run) runHarness(ctx context.Context, c job.Case, repeat int, prompt string, sampling job.Sampling) error {
	h, err := r.harnessRunner(ctx, sampling)
	if err != nil {
		return err
	}
	def, ok := h.defs[c.Harness]
	if !ok {
		return fmt.Errorf("harness %q is not defined in %s/harnesses.yaml", c.Harness, harness.Dir)
	}
	if _, ok := h.versions[c.Harness]; !ok {
		h.versions[c.Harness] = def.Tool + "@" + h.mise.Version(ctx, def.Tool)
	}
	dir, err := os.MkdirTemp("", "llmbench-harness-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	home, work := filepath.Join(dir, "home"), filepath.Join(dir, "work")
	for _, d := range []string{home, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	name := fmt.Sprintf("%s-%d", c.Name, repeat)
	logDir := filepath.Join(r.cfg.OutDir, "raw", "harness")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	log, err := os.Create(filepath.Join(logDir, name+".log"))
	if err != nil {
		return err
	}
	defer log.Close()

	params := harness.Params{Prompt: prompt, BaseURL: h.proxy.URL + "/v1", Model: r.model.ID, Context: r.contextSize(ctx), Home: home}
	runCtx, cancel := context.WithTimeout(ctx, c.Timeout())
	defer cancel()
	mark, start := h.proxy.mark(), time.Now()
	runErr := h.mise.Run(runCtx, def, params, work, log)
	elapsed := time.Since(start)
	reqs := h.proxy.since(mark)
	if runErr != nil {
		fmt.Fprintf(log, "\nllmbench: %v\n", runErr)
	}

	values := requestValues(reqs)
	values["latency_ms"] = ms(elapsed)
	values["completed"] = 0
	if runErr == nil {
		values["completed"] = 1
	}
	r.record(c.Name, repeat, values)
	// A harness may answer with the page instead of writing it, as a direct
	// request does; then the page in its reply is the output.
	if _, err := os.Stat(filepath.Join(work, "index.html")); errors.Is(err, fs.ErrNotExist) {
		if reply, err := os.ReadFile(log.Name()); err == nil {
			if html := extractHTML(string(reply)); html != "" {
				_ = os.WriteFile(filepath.Join(work, "index.html"), []byte(html), 0o644)
			}
		}
	}
	if err := copyTree(work, filepath.Join(r.cfg.OutDir, "output", name)); err != nil {
		return fmt.Errorf("output could not be saved: %w", err)
	}
	if len(reqs) == 0 {
		return fmt.Errorf("harness %s made no requests: %v", c.Harness, runErr)
	}
	return nil
}

// contextSize asks the runtime for its context length, which some harnesses
// need to know when to compact.
func (r *run) contextSize(ctx context.Context) int {
	metrics, err := r.adapter.Metrics(ctx)
	if err != nil {
		return 0
	}
	for _, m := range metrics {
		if m.Name == "context_size" {
			return int(m.Value)
		}
	}
	return 0
}

// copyTree copies the regular files under src, leaving out dependency and
// version control directories a harness may have created.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		return errors.Join(err, out.Close())
	})
}
