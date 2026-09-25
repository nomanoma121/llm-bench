package provenance

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ExpectedFile pairs a repository-relative path with the sha256 the commit's
// blob must have. Defined here (not in runner) because provenance implements
// the Git interface and a runner-owned type would create an import cycle.
type ExpectedFile struct {
	Path   string
	SHA256 string
}

// Git verifies commits and exports snapshots through the git binary. The
// harness runs against a real checkout, so shelling out keeps full fidelity
// with git archive semantics.
type Git struct {
	// Root is the repository working directory.
	Root string
}

// VerifyCommit checks that commit exists, contains no gitlinks (submodules
// are not exportable via git archive) and that every expected file matches
// the commit content by sha256.
func (g Git) VerifyCommit(ctx context.Context, commit string, expected []ExpectedFile) error {
	if err := validateSHA(commit); err != nil {
		return err
	}
	out, err := g.git(ctx, "ls-tree", "-r", commit)
	if err != nil {
		return fmt.Errorf("provenance: ls-tree %s: %w", commit, err)
	}
	for _, line := range strings.Split(out, "\n") {
		// Mode entries: 160000 marks a gitlink (submodule) entry.
		if strings.HasPrefix(line, "160000") {
			return fmt.Errorf("provenance: commit %s contains submodules, which git archive cannot include", commit)
		}
	}
	for _, e := range expected {
		blob, err := g.git(ctx, "show", commit+":"+e.Path)
		if err != nil {
			return fmt.Errorf("provenance: commit %s misses %s: %w", commit, e.Path, err)
		}
		if got := SHA256Hex([]byte(blob)); got != e.SHA256 {
			return fmt.Errorf("provenance: %s at commit %s does not match the snapshot (have %s, want %s)", e.Path, commit, got, e.SHA256)
		}
	}
	return nil
}

// Archive returns the tar stream of `git archive <commit>`.
func (g Git) Archive(ctx context.Context, commit string) ([]byte, error) {
	if err := validateSHA(commit); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", commit)
	cmd.Dir = g.Root
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("provenance: git archive %s: %v: %s", commit, err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

func (g Git) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Root
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

func validateSHA(commit string) error {
	if len(commit) != 40 || strings.TrimSpace(commit) != commit {
		return fmt.Errorf("provenance: %q is not a full 40-hex commit", commit)
	}
	for _, r := range commit {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return fmt.Errorf("provenance: %q is not a lowercase hex commit", commit)
		}
	}
	return nil
}
