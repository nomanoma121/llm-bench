package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type process struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

func startProcess(argv []string, logPath string) (*process, error) {
	log, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	p := &process{cmd: cmd, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		log.Close()
		close(p.exited)
	}()
	return p, nil
}

func (p *process) stop() {
	pgid := p.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-p.exited:
	case <-time.After(15 * time.Second):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-p.exited
	}
}

func sourceVersion(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	git := func(args ...string) string {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	hash := git("rev-parse", "--short=12", "HEAD")
	if hash == "" {
		return "unknown"
	}
	if git("status", "--porcelain", "--untracked-files=no") != "" {
		return hash + " dirty"
	}
	if git("branch", "--remotes", "--contains", "HEAD") == "" {
		return hash
	}
	if version := git("describe", "--tags", "--match", "v[0-9]*"); version != "" {
		return version
	}
	return hash
}

type gpuReading struct {
	Driver string
	GPUs   []gpuState
}

type gpuState struct {
	Name        string
	UsedMiB     float64
	UtilPercent float64
}

func readGPU(ctx context.Context) (gpuReading, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=name,memory.used,utilization.gpu,driver_version",
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
		if len(fields) != 4 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		used, err1 := strconv.ParseFloat(fields[1], 64)
		util, err2 := strconv.ParseFloat(fields[2], 64)
		if err1 != nil || err2 != nil {
			return gpuReading{}, fmt.Errorf("nvidia-smi: unexpected line %q", scanner.Text())
		}
		r.Driver = fields[3]
		r.GPUs = append(r.GPUs, gpuState{Name: fields[0], UsedMiB: used, UtilPercent: util})
	}
	if len(r.GPUs) == 0 {
		return gpuReading{}, fmt.Errorf("nvidia-smi: no gpu reported")
	}
	return r, nil
}
