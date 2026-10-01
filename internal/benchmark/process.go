package benchmark

import (
	"context"
	"os"
	"os/exec"
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
