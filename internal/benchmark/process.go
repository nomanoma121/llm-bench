package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Process is a started runtime.
type Process interface {
	// Wait blocks until the process exits and returns its exit error.
	Wait() error
	// Stop asks the process to exit and escalates after a grace period.
	Stop(ctx context.Context) error
}

// Starter launches the runtime command. Tests substitute a starter that does
// not spawn a process; production uses ExecStarter.
type Starter interface {
	Start(ctx context.Context, argv []string, logPath string) (Process, error)
}

// ExecStarter runs the command in its own process group and captures its
// stdout and stderr into the log file, so the harness can stop the runtime and
// everything it spawned.
type ExecStarter struct{}

func (ExecStarter) Start(ctx context.Context, argv []string, logPath string) (Process, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("benchmark: empty runtime command")
	}
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("benchmark: runtime log: %w", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Stdin = nil
	// The runtime is the candidate's code. It inherits the harness
	// environment, which holds the short-lived git token the result is pushed
	// with, so that token is removed here: the runtime has no business
	// publishing anything, and a runtime that logs its environment would leak
	// a write credential into the measurement's raw output.
	cmd.Env = runtimeEnv(os.Environ())
	// A new process group lets Stop signal every descendant: runtimes spawn
	// worker threads and helper processes, and leaving them behind would hold
	// the GPU after the run reports itself finished.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, fmt.Errorf("benchmark: start %s: %w", argv[0], err)
	}
	return &execProcess{cmd: cmd, log: log}, nil
}

// runtimeEnv removes the publishing credential from the environment the
// runtime child process receives. Everything else is inherited: the runtime
// needs the usual machine environment (CUDA paths, model directories).
func runtimeEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if strings.HasPrefix(kv, publishingEnvPrefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// publishingEnvPrefix is the environment prefix the benchmark CLI reads its
// git token from; the runner must not hand it to the runtime.
const publishingEnvPrefix = "LLMBENCH_GIT_TOKEN"

type execProcess struct {
	cmd  *exec.Cmd
	log  *os.File
	done bool
}

func (p *execProcess) Wait() error {
	if p.done {
		return nil
	}
	p.done = true
	err := p.cmd.Wait()
	p.log.Close()
	if err == nil {
		return nil
	}
	if exit, ok := err.(*exec.ExitError); ok {
		// A process we killed on purpose is not a runtime failure, and the
		// caller only logs this value.
		return fmt.Errorf("exit status %d: %w", exit.ExitCode(), err)
	}
	return err
}

func (p *execProcess) Stop(ctx context.Context) error {
	if p.cmd.Process == nil {
		return nil
	}
	pgid := p.cmd.Process.Pid
	// Negative pid targets the whole group.
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}
	deadline := time.After(stopGrace)
	waited := make(chan error, 1)
	go func() { waited <- p.Wait() }()
	select {
	case err := <-waited:
		return err
	case <-deadline:
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			_ = p.cmd.Process.Kill()
		}
		<-waited
		return fmt.Errorf("runtime did not exit within %s; killed", stopGrace)
	case <-ctx.Done():
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-waited
		return ctx.Err()
	}
}

// GPUReader samples the GPU. The nvidia implementation shells out to
// nvidia-smi; a test substitutes a fake so the measurement path is testable
// without a GPU.
type GPUReader interface {
	Sample(ctx context.Context) (GPUSample, []string, error)
}

// GPUSample is one reading of every GPU in the machine.
type GPUSample struct {
	Driver string
	GPUs   []GPUState
}

// GPUState is one device's state at the moment of the sample.
type GPUState struct {
	Index       int
	Model       string
	UsedMiB     int
	TotalMiB    int
	UtilPercent int
}

// NvidiaSMI reads the GPU state with nvidia-smi. The raw output is returned so
// the run can keep what it saw.
type NvidiaSMI struct {
	// Command overrides the binary, mostly for tests.
	Command string
}

func (n NvidiaSMI) Sample(ctx context.Context) (GPUSample, []string, error) {
	bin := n.Command
	if bin == "" {
		bin = "nvidia-smi"
	}
	query := "index,name,memory.used,memory.total,utilization.gpu,driver_version"
	cmd := exec.CommandContext(ctx, bin, "--query-gpu="+query, "--format=csv,noheader,nounits")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return GPUSample{}, nil, fmt.Errorf("%s: %v: %s", bin, err, strings.TrimSpace(stderr.String()))
	}
	sample, err := parseNvidiaSMI(out)
	if err != nil {
		return GPUSample{}, nil, err
	}
	raw := []string{fmt.Sprintf("$ %s --query-gpu=%s --format=csv,noheader,nounits", bin, query)}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		raw = append(raw, line)
	}
	return sample, raw, nil
}

// parseNvidiaSMI reads `index, name, used, total, util, driver` rows.
func parseNvidiaSMI(out []byte) (GPUSample, error) {
	sample := GPUSample{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) != 6 {
			return GPUSample{}, fmt.Errorf("benchmark: nvidia-smi returned %d fields, want 6: %q", len(fields), line)
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil {
			return GPUSample{}, fmt.Errorf("benchmark: nvidia-smi index %q: %w", fields[0], err)
		}
		used, err := strconv.Atoi(fields[2])
		if err != nil {
			return GPUSample{}, fmt.Errorf("benchmark: nvidia-smi memory.used %q: %w", fields[2], err)
		}
		total, err := strconv.Atoi(fields[3])
		if err != nil {
			return GPUSample{}, fmt.Errorf("benchmark: nvidia-smi memory.total %q: %w", fields[3], err)
		}
		util, err := strconv.Atoi(fields[4])
		if err != nil {
			return GPUSample{}, fmt.Errorf("benchmark: nvidia-smi utilization.gpu %q: %w", fields[4], err)
		}
		if sample.Driver == "" {
			sample.Driver = fields[5]
		}
		sample.GPUs = append(sample.GPUs, GPUState{
			Index: index, Model: fields[1], UsedMiB: used, TotalMiB: total, UtilPercent: util,
		})
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return GPUSample{}, fmt.Errorf("benchmark: nvidia-smi: %w", err)
	}
	if len(sample.GPUs) == 0 {
		return GPUSample{}, fmt.Errorf("benchmark: nvidia-smi reported no GPU")
	}
	return sample, nil
}
