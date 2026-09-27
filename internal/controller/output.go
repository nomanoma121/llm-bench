package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// benchmarkOutput is what `llmbench benchmark --json` prints. The controller
// reads the pushed branch and the result identity from it, which is how the
// measurement and the PR that will carry it are tied together.
type benchmarkOutput struct {
	JobID            string   `json:"job_id"`
	OutputDir        string   `json:"output_dir"`
	ResultDigest     string   `json:"result_digest"`
	SeriesDigest     string   `json:"series_digest"`
	MeasurementValid bool     `json:"measurement_valid"`
	InvalidReasons   []string `json:"invalid_reasons"`
	Branch           string   `json:"branch"`
	Commit           string   `json:"commit"`
}

// parseBenchmarkOutput decodes the CLI's JSON summary. The sandbox prints
// progress on stderr, so stdout is expected to hold exactly one JSON object;
// anything else is a bug worth failing on rather than guessing at.
func parseBenchmarkOutput(out []byte) (benchmarkOutput, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return benchmarkOutput{}, fmt.Errorf("controller: the benchmark printed nothing; its log is in the sandbox output")
	}
	// Tolerate a trailing newline or a stray log line before the JSON by
	// decoding from the first '{'.
	if start := bytes.IndexByte(trimmed, '{'); start > 0 {
		trimmed = trimmed[start:]
	}
	var result benchmarkOutput
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&result); err != nil {
		return benchmarkOutput{}, fmt.Errorf("controller: the benchmark output is not JSON: %w", err)
	}
	if result.JobID == "" {
		return benchmarkOutput{}, fmt.Errorf("controller: the benchmark output has no job id")
	}
	if result.ResultDigest == "" {
		return benchmarkOutput{}, fmt.Errorf("controller: the benchmark output has no result digest, so the result is not identified")
	}
	return result, nil
}
