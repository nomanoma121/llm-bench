package benchmark

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func Push(ctx context.Context, root, branch, message string, paths ...string) (string, error) {
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out)), nil
	}
	steps := [][]string{
		{"switch", "-c", branch},
		append([]string{"add", "--"}, paths...),
		{"commit", "-m", message},
		{"push", "origin", "HEAD:refs/heads/" + branch},
	}
	for _, args := range steps {
		if _, err := git(args...); err != nil {
			return "", err
		}
	}
	return git("rev-parse", "HEAD")
}
