package provenance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "t")
	return dir
}

func TestVerifyCommitAndArchive(t *testing.T) {
	dir := initRepo(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: m"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "examples"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "examples", "prompt.md"), []byte("prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	commit := git(t, dir, "rev-parse", "HEAD")

	g := Git{Root: dir}
	expected := []ExpectedFile{
		{Path: "config.yaml", SHA256: SHA256Hex([]byte("model: m"))},
		{Path: "examples/prompt.md", SHA256: SHA256Hex([]byte("prompt"))},
	}
	if err := g.VerifyCommit(ctx, commit, expected); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// A tampered expectation must fail.
	bad := append([]ExpectedFile(nil), expected...)
	bad[0].SHA256 = strings.Repeat("0", 64)
	if err := g.VerifyCommit(ctx, commit, bad); err == nil {
		t.Fatal("tampered expectation must fail")
	}

	// Short / non-hex commits are rejected before running git.
	if err := g.VerifyCommit(ctx, "HEAD", expected); err == nil {
		t.Fatal("short commit must be rejected")
	}

	// Archive produces a tar containing the files.
	tar, err := g.Archive(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(tar) < 512 {
		t.Fatalf("archive too small: %d", len(tar))
	}
	if string(tar[257:262]) != "ustar" {
		t.Fatalf("archive is not a ustar tar: %q", tar[257:262])
	}
}

func TestVerifyCommitRejectsGitlinks(t *testing.T) {
	dir := initRepo(t)
	// Insert a gitlink entry without a real submodule.
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("a", 40)+",sub")
	git(t, dir, "commit", "-q", "-m", "with gitlink")
	commit := git(t, dir, "rev-parse", "HEAD")

	g := Git{Root: dir}
	err := g.VerifyCommit(context.Background(), commit, nil)
	if err == nil || !strings.Contains(err.Error(), "submodule") {
		t.Fatalf("want submodule rejection, got %v", err)
	}
}

func TestHashTreeLocal(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "weights.bin"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "tok.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := HashTreeLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(id.Files) != 2 {
		t.Fatalf("files = %+v", id.Files)
	}
	// Sorted by path: sub/tok.json before weights.bin.
	if id.Files[0].Path != "sub/tok.json" || id.Files[1].Path != "weights.bin" {
		t.Fatalf("paths = %q %q", id.Files[0].Path, id.Files[1].Path)
	}
	d1 := id.TreeDigest

	// Determinism.
	id2, _ := HashTreeLocal(dir)
	if id2.TreeDigest != d1 {
		t.Fatal("digest not deterministic")
	}

	// Content change must change the digest.
	if err := os.WriteFile(filepath.Join(dir, "weights.bin"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	id3, _ := HashTreeLocal(dir)
	if id3.TreeDigest == d1 {
		t.Fatal("digest must change with content")
	}
}

func TestHashTreeLocalRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := HashTreeLocal(dir); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("want symlink rejection, got %v", err)
	}
}

func TestHashTreeScriptRejectsDirectorySymlinks(t *testing.T) {
	script := HashTreeScript("/models/m")
	// The script must inspect os.walk's dirnames for symlinks: a symlinked
	// directory would otherwise be silently skipped (followlinks=False) and
	// the digest would not cover it.
	if !strings.Contains(script, "list(dirnames)") || !strings.Contains(script, "S_ISLNK") {
		t.Fatalf("manifest script does not reject directory symlinks:\n%s", script)
	}
	if !strings.Contains(script, "import hashlib, json, os, stat, sys") {
		t.Fatal("manifest script lost its imports")
	}
	// The model root itself must be checked too: os.walk happily descends
	// into a root that is a directory symlink.
	if !strings.Contains(script, "os.lstat(root)") || !strings.Contains(script, "model root is not a directory") {
		t.Fatalf("manifest script does not reject a symlinked model root:\n%s", script)
	}
}

func TestValidateCommitSHA(t *testing.T) {
	if err := ValidateCommitSHA(strings.Repeat("a", 40)); err != nil {
		t.Fatalf("valid sha rejected: %v", err)
	}
	for _, bad := range []string{"HEAD", strings.Repeat("a", 39), strings.Repeat("A", 40), strings.Repeat("z", 40), strings.Repeat("a", 40) + " "} {
		if err := ValidateCommitSHA(bad); err == nil {
			t.Errorf("invalid commit %q accepted", bad)
		}
	}
}

func TestParseModelIdentityRecomputesDigest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, _ := HashTreeLocal(dir)
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseModelIdentity(b)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TreeDigest != id.TreeDigest {
		t.Fatalf("digest mismatch after round-trip: %s vs %s", parsed.TreeDigest, id.TreeDigest)
	}
	// A forged digest is overwritten on parse.
	var forged map[string]any
	if err := json.Unmarshal(b, &forged); err != nil {
		t.Fatal(err)
	}
	forged["tree_digest"] = strings.Repeat("f", 64)
	b2, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	parsed2, err := ParseModelIdentity(b2)
	if err != nil {
		t.Fatal(err)
	}
	if parsed2.TreeDigest != id.TreeDigest {
		t.Fatal("parse must recompute the tree digest")
	}
}
