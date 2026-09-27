package runner

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/operator"
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

// protocolSnapshot decodes the protocol frozen at submit time. It is the
// source of truth for the driver and for the sources a measurement must have.
func protocolSnapshot(r run.Run) (operator.MeasurementProtocol, error) {
	var p operator.MeasurementProtocol
	if r.MeasurementProtocolJSON == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(r.MeasurementProtocolJSON), &p); err != nil {
		return p, fmt.Errorf("runner: decode protocol snapshot: %w", err)
	}
	return p, nil
}

// driverArgv returns the frozen driver command of the run's protocol.
func driverArgv(r run.Run) ([]string, error) {
	p, err := protocolSnapshot(r)
	if err != nil {
		return nil, err
	}
	return p.DriverArgv, nil
}

// sealEvidence builds and seals the measurement evidence for one execution.
//
// The harness is the authoritative writer: the driver is executed by the
// harness and its stdout is parsed here, so identity, validity and environment
// always come from the trusted side and the candidate cannot substitute the
// numbers (docs/optimization.md §5.4/§5.5).
func sealEvidence(r run.Run, artifactDir string, wallClock time.Duration, driverStdout []byte, driverErr error) (run.Evidence, error) {
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

	switch {
	case driverErr != nil:
		// The driver itself failed: the measurement cannot be trusted, so the
		// evidence is sealed as invalid with the reason instead of pretending
		// the run produced numbers.
		e.MeasurementValid = false
		e.InvalidReasons = []string{"driver failed: " + driverErr.Error()}
	case len(driverStdout) > 0:
		parsed, err := measurement.ParseRaw(driverStdout)
		if err != nil {
			e.MeasurementValid = false
			e.InvalidReasons = []string{"unreadable driver output: " + err.Error()}
		} else {
			e = e.AppendRaw(parsed, measurement.SourceDriver)
		}
	}

	// The protocol decides which sources must be present for the measurement
	// to count; harness timing alone is not a driver measurement.
	protocol, err := protocolSnapshot(r)
	if err != nil {
		return run.Evidence{}, err
	}
	e = measurement.EnforceRequiredSources(e, protocol.RequiredSources)
	e = measurement.EnforceCollectorGaps(e)

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
