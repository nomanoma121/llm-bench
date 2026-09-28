package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelsDownload(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	os.WriteFile(filepath.Join(dir, "hf"), []byte("#!/bin/sh\necho \"$@\" >> "+log+"\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	defs := filepath.Join(dir, "models.yaml")
	os.WriteFile(defs, []byte(`
b:
  repo: org/b
a:
  repo: org/a-GGUF
  revision: v1
  include: ["*Q4_0.gguf", "mmproj-*"]
`), 0o644)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"models", "download", "--file", defs, "--dir", "/m"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	want := "download org/a-GGUF --local-dir /m/a --revision v1 --include *Q4_0.gguf --include mmproj-*\n" +
		"download org/b --local-dir /m/b\n"
	if string(calls) != want {
		t.Fatalf("hf calls:\n%s", calls)
	}

	cmd = newRootCmd()
	cmd.SetArgs([]string{"models", "download", "--file", defs, "missing"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("got %v", err)
	}
}
