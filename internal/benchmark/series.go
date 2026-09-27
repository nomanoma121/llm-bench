package benchmark

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/nomanoma121/llm-bench/internal/measurement"
)

// Sample is one raw observation written to series.jsonl. It is deliberately
// flat: the file is meant to be readable line by line and to keep its order,
// because the digest covers the bytes as written.
type Sample struct {
	Name   string             `json:"name"`
	Source measurement.Source `json:"source"`
	Unit   string             `json:"unit,omitempty"`
	Labels map[string]string  `json:"labels,omitempty"`
	Value  float64            `json:"value"`
	// AtMS is the offset from the start of the run in milliseconds.
	AtMS int64 `json:"at_ms"`
	// Repeat and Step locate the sample inside a case (0 when not applicable).
	Repeat int `json:"repeat,omitempty"`
	Step   int `json:"step,omitempty"`
}

func (s Sample) marshal() ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// summarize computes the statistics recorded next to a metric's value. The
// median is the value the summary reports, and the spread is recorded so a
// reader can see whether a difference is larger than the noise without having
// to re-read series.jsonl.
func summarize(values []float64) measurement.Stats {
	if len(values) == 0 {
		return measurement.Stats{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	mean := sum / float64(len(sorted))
	median := quantile(sorted, 0.5)
	abs := make([]float64, 0, len(sorted))
	for _, v := range sorted {
		abs = append(abs, math.Abs(v-median))
	}
	sort.Float64s(abs)
	return measurement.Stats{
		Count:  len(sorted),
		Mean:   mean,
		Median: median,
		Min:    sorted[0],
		Max:    sorted[len(sorted)-1],
		P50:    median,
		P90:    quantile(sorted, 0.9),
		P99:    quantile(sorted, 0.99),
		MAD:    quantile(abs, 0.5),
		Sum:    sum,
	}
}

// quantile picks the value at q of a sorted slice by nearest rank, which is
// stable for small sample sizes: with three repeats the median is a measured
// value, never an average of two.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func medianOf(values []float64) float64 { return summarize(values).Median }
func meanOf(values []float64) float64   { return summarize(values).Mean }
func maxOf(values []float64) float64    { return summarize(values).Max }
