package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// evidenceDir returns the directory that holds a run's measurement evidence.
// It is a sibling of output/ and input/, never inside the visual payload
// (docs/optimization.md §5.1).
func evidenceDir(artifactDir string) string {
	return filepath.Join(artifactDir, measurement.EvidenceDir)
}

// runKind resolves the persisted kind, mapping legacy records to visual.
func runKind(r run.Run) run.RunKind { return run.KindOrDefault(r.Kind) }

// needsEvidence reports whether the run must end with sealed evidence: every
// measurement run needs it, and a visual run carries one only when a protocol
// was frozen at submit time.
func needsEvidence(r run.Run) bool {
	return runKind(r) == run.RunKindMeasurement || r.MeasurementProtocolDigest != ""
}

// sealEvidence builds and seals the measurement evidence for one execution.
//
// The harness is the authoritative writer: raw numbers produced inside the
// Sandbox are parsed here, tagged with their source and combined with harness
// measurements, so identity, validity and environment always come from the
// trusted side (docs/optimization.md §5.4/§5.5).
func sealEvidence(r run.Run, artifactDir string, wallClock time.Duration, raw []byte) (run.Evidence, error) {
	e := measurement.Evidence{
		SchemaVersion: measurement.SchemaVersion,
		RunID:         r.ID,
		Kind:          string(runKind(r)),
		Protocol:      measurement.Ref{ID: r.MeasurementProtocolID, Digest: r.MeasurementProtocolDigest},
		Environment:   measurement.Environment{Digest: r.EnvironmentDigest},
		Runtime: measurement.RuntimeRef{
			SpecDigest:  r.RuntimeSpecDigest,
			BuildDigest: r.RuntimeBuildDigest,
		},
		MeasurementValid: true,
	}
	e = measurement.WithHarnessMetric(e, "wall_clock_ms", float64(wallClock.Milliseconds()), "ms", nil)

	if len(raw) > 0 {
		parsed, err := measurement.ParseRaw(raw)
		if err != nil {
			// A driver whose output cannot be trusted is an invalid
			// measurement, not a silent success: the run fails on the
			// validity gate and the reason is recorded.
			e.MeasurementValid = false
			e.InvalidReasons = []string{"unreadable raw measurement: " + err.Error()}
		} else {
			e = e.AppendRaw(parsed, measurement.SourceDriver)
		}
	}

	sealed, err := measurement.Seal(evidenceDir(artifactDir), e)
	if err != nil {
		return run.Evidence{}, err
	}
	return run.Evidence{
		Path:           sealed.Path,
		Digest:         sealed.Digest,
		Valid:          e.MeasurementValid,
		InvalidReasons: e.InvalidReasons,
	}, nil
}

// readRawMeasurement reads the optional raw measurement a driver wrote into
// the run's evidence directory. A missing file is normal.
func readRawMeasurement(artifactDir string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(evidenceDir(artifactDir), measurement.RawMeasurementFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runner: read raw measurement: %w", err)
	}
	if len(b) > measurement.MaxEvidenceBytes {
		return nil, fmt.Errorf("runner: raw measurement is %d bytes, over the %d byte limit", len(b), measurement.MaxEvidenceBytes)
	}
	return b, nil
}
