package benchmark

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// cpuTimes are cumulative jiffies of one /proc/stat line.
type cpuTimes struct {
	busy, total uint64
}

// readCPU returns the aggregate CPU times followed by those of each core.
func readCPU() ([]cpuTimes, error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}
	return parseProcStat(b)
}

func parseProcStat(b []byte) ([]cpuTimes, error) {
	var out []cpuTimes
	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 9 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		// user nice system idle iowait irq softirq steal; guest time is
		// already counted in user.
		var t cpuTimes
		for i, f := range fields[1:9] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("/proc/stat: %q: %w", scanner.Text(), err)
			}
			t.total += v
			if i != 3 && i != 4 {
				t.busy += v
			}
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("/proc/stat: no cpu lines")
	}
	return out, nil
}

// cpuUtil returns the utilization of all cores together and of the busiest
// core between two readings.
func cpuUtil(prev, cur []cpuTimes) (all, busiest float64, ok bool) {
	if len(prev) != len(cur) || len(cur) == 0 {
		return 0, 0, false
	}
	percent := func(a, b cpuTimes) float64 {
		if b.total <= a.total {
			return 0
		}
		return 100 * float64(b.busy-a.busy) / float64(b.total-a.total)
	}
	all = percent(prev[0], cur[0])
	for i := 1; i < len(cur); i++ {
		busiest = max(busiest, percent(prev[i], cur[i]))
	}
	return all, busiest, true
}

func readMemUsedMiB() (float64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	return parseMeminfo(b)
}

// parseMeminfo returns memory in use as MemTotal minus MemAvailable, in MiB.
func parseMeminfo(b []byte) (float64, error) {
	kb := map[string]float64{}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 {
			if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
				kb[strings.TrimSuffix(fields[0], ":")] = v
			}
		}
	}
	total, ok1 := kb["MemTotal"]
	available, ok2 := kb["MemAvailable"]
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("/proc/meminfo: MemTotal or MemAvailable missing")
	}
	return (total - available) / 1024, nil
}
