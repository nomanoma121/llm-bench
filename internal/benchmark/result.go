package benchmark

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Result struct {
	JobID            string    `json:"job_id"`
	Kind             string    `json:"kind"`
	StartedAt        time.Time `json:"started_at"`
	DurationSeconds  float64   `json:"duration_seconds"`
	Model            Model     `json:"model"`
	Runtime          Runtime   `json:"runtime"`
	GPUs             []string  `json:"gpus,omitempty"`
	Driver           string    `json:"driver,omitempty"`
	MeasurementValid bool      `json:"measurement_valid"`
	InvalidReasons   []string  `json:"invalid_reasons,omitempty"`
	Metrics          []Metric  `json:"metrics"`
	Digests          Digests   `json:"digests"`
}

type Model struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Digest string `json:"digest,omitempty"`
}

type Runtime struct {
	Engine  string            `json:"engine"`
	Binary  string            `json:"binary"`
	Version string            `json:"version,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Info    map[string]string `json:"info,omitempty"`
}

func (r Runtime) Label() string {
	return strings.TrimSpace(r.Engine + " " + r.Version)
}

type Metric struct {
	Name    string  `json:"name"`
	Case    string  `json:"case,omitempty"`
	Unit    string  `json:"unit"`
	Value   float64 `json:"value"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Samples int     `json:"samples"`
}

type Digests struct {
	JobSpec  string            `json:"jobspec"`
	Series   string            `json:"series"`
	Workload string            `json:"workload"`
	Prompts  map[string]string `json:"prompts"`
}

type Sample struct {
	AtMS   int64             `json:"at_ms"`
	Source string            `json:"source"`
	Name   string            `json:"name"`
	Case   string            `json:"case,omitempty"`
	Repeat int               `json:"repeat,omitempty"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (r Result) Metric(name, caseName string) (Metric, bool) {
	for _, m := range r.Metrics {
		if m.Name == name && m.Case == caseName {
			return m, true
		}
	}
	return Metric{}, false
}

func LoadResult(dir string) (Result, error) {
	b, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		return Result{}, err
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return Result{}, fmt.Errorf("%s: %w", dir, err)
	}
	return r, nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func summarize(name, caseName, unit string, values []float64, agg func([]float64) float64) (Metric, bool) {
	if len(values) == 0 {
		return Metric{}, false
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return Metric{
		Name: name, Case: caseName, Unit: unit,
		Value: agg(sorted), Min: sorted[0], Max: sorted[len(sorted)-1], Samples: len(sorted),
	}, true
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func mean(sorted []float64) float64 {
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	return sum / float64(len(sorted))
}

func maximum(sorted []float64) float64 { return sorted[len(sorted)-1] }

func (r Result) readme() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", r.JobID)
	fmt.Fprintf(&b, "- kind: %s\n- model: %s (`%s`)\n- runtime: %s\n- args: %s\n", r.Kind, r.Model.ID, r.Model.Digest, r.Runtime.Label(), strings.Join(r.Runtime.Args, " "))
	if len(r.GPUs) > 0 {
		fmt.Fprintf(&b, "- gpu: %s (driver %s)\n", strings.Join(r.GPUs, ", "), r.Driver)
	}
	fmt.Fprintf(&b, "- started: %s (%.0fs)\n- measurement valid: %t\n", r.StartedAt.UTC().Format(time.RFC3339), r.DurationSeconds, r.MeasurementValid)
	for _, reason := range r.InvalidReasons {
		fmt.Fprintf(&b, "  - %s\n", reason)
	}
	b.WriteString("\n| case | metric | value | min | max | n |\n|---|---|---|---|---|---|\n")
	for _, m := range r.Metrics {
		fmt.Fprintf(&b, "| %s | %s (%s) | %.2f | %.2f | %.2f | %d |\n", m.Case, m.Name, m.Unit, m.Value, m.Min, m.Max, m.Samples)
	}
	return b.String()
}
