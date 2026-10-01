package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// gpuFields are the per-GPU values read from nvidia-smi on every sample, in
// query order. Values the GPU reports as unsupported are skipped.
var gpuFields = []struct {
	query, name string
	parse       func(string) (float64, error)
}{
	{"memory.used", "vram_used_mib", parseNumber},
	{"utilization.gpu", "gpu_util_percent", parseNumber},
	{"utilization.memory", "mem_util_percent", parseNumber},
	{"power.draw", "power_w", parseNumber},
	{"temperature.gpu", "temperature_c", parseNumber},
	{"clocks.sm", "sm_clock_mhz", parseNumber},
	{"clocks_event_reasons.active", "throttled", parseThrottled},
}

// throttleReasons are the clock event reasons that mean the GPU slowed down
// for power or heat: SW power cap, HW slowdown, SW thermal, HW thermal and HW
// power brake. Idle and application or display clock settings are not counted.
const throttleReasons = 0x04 | 0x08 | 0x20 | 0x40 | 0x80

type gpuReading struct {
	Driver string
	GPUs   []gpuState
}

type gpuState struct {
	Name   string
	Values map[string]float64
}

func readGPU(ctx context.Context) (gpuReading, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := []string{"name", "driver_version"}
	for _, f := range gpuFields {
		query = append(query, f.query)
	}
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu="+strings.Join(query, ","),
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpuReading{}, fmt.Errorf("nvidia-smi: %w", err)
	}
	return parseNvidiaSMI(out)
}

func parseNvidiaSMI(out []byte) (gpuReading, error) {
	var r gpuReading
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ",")
		if len(fields) != 2+len(gpuFields) {
			return gpuReading{}, fmt.Errorf("nvidia-smi: unexpected line %q", scanner.Text())
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		r.Driver = fields[1]
		g := gpuState{Name: fields[0], Values: map[string]float64{}}
		for i, f := range gpuFields {
			if v, err := f.parse(fields[2+i]); err == nil {
				g.Values[f.name] = v
			}
		}
		r.GPUs = append(r.GPUs, g)
	}
	if len(r.GPUs) == 0 {
		return gpuReading{}, fmt.Errorf("nvidia-smi: no gpu reported")
	}
	return r, nil
}

func parseNumber(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

func parseThrottled(s string) (float64, error) {
	mask, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
	if err != nil {
		return 0, err
	}
	if mask&throttleReasons != 0 {
		return 1, nil
	}
	return 0, nil
}

type pcieReading struct {
	GPU    int
	RxMBps float64
	TxMBps float64
}

// streamPCIe runs `nvidia-smi dmon -s t` until ctx is done. PCIe throughput is
// only available from dmon, which reports it as a rate over its own interval.
func streamPCIe(ctx context.Context, interval time.Duration, emit func(pcieReading)) error {
	seconds := max(1, int(interval.Round(time.Second)/time.Second))
	cmd := exec.CommandContext(ctx, "nvidia-smi", "dmon", "-s", "t", "-d", strconv.Itoa(seconds))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	readDmon(out, emit)
	return cmd.Wait()
}

func readDmon(r io.Reader, emit func(pcieReading)) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		gpu, err1 := strconv.Atoi(fields[0])
		rx, err2 := strconv.ParseFloat(fields[1], 64)
		tx, err3 := strconv.ParseFloat(fields[2], 64)
		if err1 == nil && err2 == nil && err3 == nil {
			emit(pcieReading{GPU: gpu, RxMBps: rx, TxMBps: tx})
		}
	}
}
