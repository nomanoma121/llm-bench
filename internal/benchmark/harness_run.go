package benchmark

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nomanoma121/llm-bench/internal/harness"
	"github.com/nomanoma121/llm-bench/internal/job"
)

type harnessRunner struct {
	mise      harness.Mise
	proxy     *proxy
	installed map[string]string
}

func (r *run) harnessRunner(sampling job.Sampling) (*harnessRunner, error) {
	if r.harnesses != nil {
		return r.harnesses, nil
	}
	p, err := startProxy(r.adapter.BaseURL(), sampling)
	if err != nil {
		return nil, err
	}
	r.harnesses = &harnessRunner{
		mise:      harness.Mise{DataDir: filepath.Join(os.TempDir(), "llmbench-mise")},
		proxy:     p,
		installed: map[string]string{},
	}
	return r.harnesses, nil
}

func (r *run) runHarness(ctx context.Context, c job.Case, repeat int, prompt string, sampling job.Sampling) error {
	h, err := r.harnessRunner(sampling)
	if err != nil {
		return err
	}
	agent, err := harness.New(c.Harness.Name)
	if err != nil {
		return err
	}
	if _, ok := h.installed[c.Harness.Name]; !ok {
		r.cfg.Logf("installing harness %s", c.Harness.Name)
		if err := h.mise.Install(ctx, agent); err != nil {
			return err
		}
		h.installed[c.Harness.Name] = strings.Join(agent.Tools(), " ")
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

	params := harness.Params{Prompt: prompt, BaseURL: h.proxy.URL + "/v1", Model: r.model.ID, Context: r.contextSize(ctx), Home: home, Args: c.Harness.Args, Env: c.Harness.Env}
	runCtx, cancel := context.WithTimeout(ctx, c.Timeout())
	defer cancel()
	mark, start := h.proxy.mark(), time.Now()
	runErr := h.mise.Run(runCtx, agent, params, work, log)
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
	// A harness may answer with the page instead of writing it.
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
		return fmt.Errorf("harness %s made no requests: %v", c.Harness.Name, runErr)
	}
	return nil
}

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
