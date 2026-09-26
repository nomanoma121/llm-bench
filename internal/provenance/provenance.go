// Package provenance derives and verifies content identity for runs: input
// and output hashes, the benchmark fingerprint that gates A/B comparability
// and (in later milestones) git snapshot verification and model tree digests.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// SHA256Hex returns the hex-encoded sha256 of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// FingerprintInput carries everything that makes two runs A/B comparable.
// Model-specific identity is deliberately excluded: comparing different
// models is the point of the benchmark. See docs/architecture.md §4.8.
type FingerprintInput struct {
	Prompt                 []byte `json:"prompt"`
	BenchmarkSchemaVersion string `json:"benchmark_schema_version"`
	ContextSize            int    `json:"context_size"`
	RuntimeSignature       string `json:"runtime_signature"`
	// Generation is the canonical JSON of the recipe's sampling conditions;
	// runs that differ only in temperature or seed must not compare equal.
	Generation        string `json:"generation"`
	TargetKind        string `json:"target_kind"` // local | sandbox
	ControllerVersion string `json:"controller_version"`
}

// Fingerprint derives the canonical fingerprint of the execution conditions.
// The serialization is Go struct order, which is stable for this fixed type.
func Fingerprint(in FingerprintInput) (string, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return "", fmt.Errorf("provenance: fingerprint: %w", err)
	}
	return SHA256Hex(b), nil
}
