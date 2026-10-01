package site

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
)

type compareData struct {
	ID      string                  `json:"id"`
	Model   string                  `json:"model"`
	Runtime string                  `json:"runtime"`
	Args    string                  `json:"args"`
	Series  map[string][][2]float64 `json:"series"`
}

func perGPU(samples []benchmark.Sample, name string) []line {
	var lines []line
	seen := map[string]int{}
	for _, s := range samples {
		if s.Source != "gpu" || s.Name != name {
			continue
		}
		key := fmt.Sprintf("%d/%s", s.AtMS, s.Labels["gpu"])
		index := seen[key]
		seen[key]++
		if id, err := strconv.Atoi(s.Labels["gpu"]); err == nil {
			index = id
		}
		for len(lines) <= index {
			lines = append(lines, line{Name: fmt.Sprintf("GPU %d", len(lines))})
		}
		lines[index].Points = append(lines[index].Points, point(s))
	}
	return lines
}

func runtimeSeries(samples []benchmark.Sample, name string) [][2]float64 {
	var out [][2]float64
	for _, s := range samples {
		if s.Source == "runtime" && s.Name == name {
			out = append(out, point(s))
		}
	}
	return out
}

func decodeOverTime(samples []benchmark.Sample) [][2]float64 {
	decoded := runtimeSeries(samples, "llamacpp:slot_decoded_tokens")
	var out [][2]float64
	for i := 1; i < len(decoded); i++ {
		dt, dv := decoded[i][0]-decoded[i-1][0], decoded[i][1]-decoded[i-1][1]
		if dt > 0 && dv > 0 {
			out = append(out, [2]float64{decoded[i][0], dv / dt})
		}
	}
	return out
}

func combine(lines []line, mean bool) [][2]float64 {
	sums, counts := map[float64]float64{}, map[float64]int{}
	for _, l := range lines {
		for _, p := range l.Points {
			sums[p[0]] += p[1]
			counts[p[0]]++
		}
	}
	var out [][2]float64
	for t, v := range sums {
		if mean {
			v /= float64(counts[t])
		}
		out = append(out, [2]float64{t, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func compareSeries(samples []benchmark.Sample) map[string][][2]float64 {
	return map[string][][2]float64{
		"decode":  decodeOverTime(samples),
		"context": runtimeSeries(samples, "llamacpp:context_tokens"),
		"vram":    combine(perGPU(samples, "vram_used_mib"), false),
		"util":    combine(perGPU(samples, "gpu_util_percent"), true),
	}
}

func point(s benchmark.Sample) [2]float64 {
	return [2]float64{float64(s.AtMS) / 1000, s.Value}
}
