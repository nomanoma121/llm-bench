package site

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

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
		"decode":     decodeOverTime(samples),
		"context":    runtimeSeries(samples, "llamacpp:context_tokens"),
		"vram":       combine(perGPU(samples, "vram_used_mib"), false),
		"util":       combine(perGPU(samples, "gpu_util_percent"), true),
		"acceptance": ratioOverTime(samples, "llamacpp:spec_decode_num_accepted_tokens_total", "llamacpp:spec_decode_num_draft_tokens_total"),
	}
}

func ratioOverTime(samples []benchmark.Sample, num, den string) [][2]float64 {
	nums, dens := runtimeSeries(samples, num), runtimeSeries(samples, den)
	byTime := map[float64]float64{}
	for _, p := range dens {
		byTime[p[0]] = p[1]
	}
	var out [][2]float64
	for _, p := range nums {
		if d := byTime[p[0]]; d > 0 {
			out = append(out, [2]float64{p[0], p[1] / d})
		}
	}
	return out
}

func acceptanceByPosition(samples []benchmark.Sample) []bar {
	last := map[string]float64{}
	var drafts float64
	for _, s := range samples {
		switch {
		case s.Source != "runtime":
		case s.Name == "llamacpp:spec_decode_num_accepted_tokens_per_pos_total":
			last[s.Labels["position"]] = s.Value
		case s.Name == "llamacpp:spec_decode_num_drafts_total":
			drafts = s.Value
		}
	}
	if drafts == 0 {
		return nil
	}
	var bars []bar
	for i := 0; ; i++ {
		v, ok := last[strconv.Itoa(i)]
		if !ok {
			return bars
		}
		bars = append(bars, bar{Label: fmt.Sprintf("position %d", i+1), Value: v / drafts})
	}
}

func memoryBars(info map[string]string) []bar {
	totals := map[string]float64{}
	for k, v := range info {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			continue
		}
		for _, kind := range []string{"model", "kv", "compute"} {
			if strings.HasPrefix(k, kind+"_buffer_mib.") {
				totals[kind] += f
			}
		}
	}
	var bars []bar
	for _, kind := range []string{"model", "kv", "compute"} {
		if totals[kind] > 0 {
			bars = append(bars, bar{Label: kind, Value: totals[kind]})
		}
	}
	return bars
}

func point(s benchmark.Sample) [2]float64 {
	return [2]float64{float64(s.AtMS) / 1000, s.Value}
}
