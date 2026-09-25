package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nomanoma121/llm-bench/internal/run"
)

// Publisher publishes run outputs to a public location. Implemented by
// internal/pages.
type Publisher interface {
	Publish(ctx context.Context, runID string, html []byte) (publicURL string, err error)
}

// PublishFinalizer publishes the benchmark output after all resources have
// been released. It implements run.Finalizer: publish failures keep the run
// in the finalizing phase for retry and must never block GPU restoration.
type PublishFinalizer struct {
	Publisher Publisher
}

// Finalize implements run.Finalizer.
func (f *PublishFinalizer) Finalize(ctx context.Context, r run.Run, a run.Artifacts) (run.FinalizeResult, error) {
	if a.Dir == "" {
		return run.FinalizeResult{}, errors.New("runner: finalize without artifact directory")
	}
	html, err := os.ReadFile(filepath.Join(a.Dir, "output", "index.html"))
	if err != nil {
		return run.FinalizeResult{}, fmt.Errorf("runner: read artifact index: %w", err)
	}
	url, err := f.Publisher.Publish(ctx, r.ID, html)
	if err != nil {
		return run.FinalizeResult{}, fmt.Errorf("runner: publish: %w", err)
	}
	return run.FinalizeResult{PublicURL: url}, nil
}
