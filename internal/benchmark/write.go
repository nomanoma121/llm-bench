package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomanoma121/llm-bench/internal/measurement"
)

// writeHumanOutputs writes the files that are not part of the result identity:
// a summary a human reads first, and the raw collector output. They are
// written last so a reader never sees a summary for a result that failed to
// seal.
func (s *runState) writeHumanOutputs(resultDigest, fileDigest string) error {
	s.mu.Lock()
	readme := s.readmeLocked(resultDigest, fileDigest)
	gpuRaw := strings.Join(s.gpuRaw, "\n")
	s.mu.Unlock()

	if err := os.WriteFile(filepath.Join(s.cfg.OutputDir, "README.md"), []byte(readme), 0o644); err != nil {
		return fmt.Errorf("benchmark: readme: %w", err)
	}
	if gpuRaw != "" {
		if err := os.WriteFile(filepath.Join(s.cfg.OutputDir, rawDir, nvidiaRawName), []byte(gpuRaw+"\n"), 0o644); err != nil {
			return fmt.Errorf("benchmark: nvidia raw: %w", err)
		}
	}
	return nil
}

// readmeLocked renders the summary. It is generated, never authoritative: the
// digests in it are the ones the result records. Callers hold the lock.
func (s *runState) readmeLocked(resultDigest, fileDigest string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", s.cfg.JobID)
	fmt.Fprintf(&b, "kind: %s\n\n", s.cfg.Spec.Kind)
	fmt.Fprintf(&b, "model: %s\n", s.cfg.Spec.Model.ID)
	fmt.Fprintf(&b, "runtime: %s (%s)\n", s.cfg.Spec.Runtime.Engine, s.cfg.Spec.Runtime.Image)
	fmt.Fprintf(&b, "result_digest: %s\n\n", resultDigest)

	fmt.Fprintf(&b, "## Measurement\n\n")
	if len(s.invalidReasons) == 0 {
		fmt.Fprintf(&b, "valid: true\n\n")
	} else {
		fmt.Fprintf(&b, "valid: false\n\n")
		for _, r := range s.invalidReasons {
			fmt.Fprintf(&b, "- %s\n", r)
		}
		fmt.Fprintln(&b)
	}

	metrics := s.metricsLocked()
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].Labels["case"] != metrics[j].Labels["case"] {
			return metrics[i].Labels["case"] < metrics[j].Labels["case"]
		}
		return metrics[i].Name < metrics[j].Name
	})
	if len(metrics) > 0 {
		fmt.Fprintf(&b, "| case | metric | value | unit | p50 | p90 | n | source |\n")
		fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
		for _, m := range metrics {
			p50, p90 := 0.0, 0.0
			if m.Stats != nil {
				p50, p90 = m.Stats.P50, m.Stats.P90
			}
			fmt.Fprintf(&b, "| %s | %s | %.4g | %s | %.4g | %.4g | %d | %s |\n",
				m.Labels["case"], m.Name, m.Value, m.Unit, p50, p90, m.Samples, m.Source)
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintf(&b, "## Files\n\n")
	fmt.Fprintf(&b, "- `jobspec.yaml`: the accepted job spec (frozen)\n")
	fmt.Fprintf(&b, "- `result.json`: the canonical result, including the digests below\n")
	fmt.Fprintf(&b, "- `series.jsonl`: raw samples in observation order\n")
	fmt.Fprintf(&b, "- `raw/`: runtime log and raw collector output\n\n")
	fmt.Fprintf(&b, "The result records these digests; a reader that checks them (the sidecar files included) is verifying the run:\n\n")
	fmt.Fprintf(&b, "- result_digest: %s\n", resultDigest)
	fmt.Fprintf(&b, "- result_file_digest: %s\n", fileDigest)
	fmt.Fprintf(&b, "- jobspec_digest: %s\n", s.specDigest)
	fmt.Fprintf(&b, "- jobspec_file_digest: %s\n", s.specFileDigest)
	fmt.Fprintf(&b, "- series_digest: %s\n", s.seriesDigest)
	return b.String()
}

// recordDigests keeps the digests the result recorded, so the generated README
// prints the same values the result carries instead of recomputing them.
func (s *runState) recordDigests(r measurement.Result) {
	s.specDigest = r.JobSpecDigest
	s.specFileDigest = r.JobSpecFileDigest
	s.seriesDigest = r.SeriesDigest
}
