package sandbox

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		`plain`:       `'plain'`,
		`a b`:         `'a b'`,
		`it's`:        `'it'"'"'s'`,
		`$(rm -rf /)`: `'$(rm -rf /)'`,
		`a'b'c`:       `'a'"'"'b'"'"'c'`,
	}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExecScript(t *testing.T) {
	got := execScript(
		[]string{"./run.sh", "--flag", "value with spaces"},
		map[string]string{"FOO": "bar baz", "BAD!": "dropped"},
		"/workspace/src",
	)
	for _, want := range []string{
		"set -e",
		"export FOO='bar baz'",
		"cd '/workspace/src'",
		`'./run.sh' '--flag' 'value with spaces'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BAD!") {
		t.Error("invalid env names must be dropped")
	}
}

func TestBackgroundScriptRecordsPID(t *testing.T) {
	got := backgroundScript([]string{"./server"}, nil, "/w", "/tmp/pid")
	for _, want := range []string{
		"rm -f '/tmp/pid'",
		"nohup './server' > /tmp/llmbench-runtime.log 2>&1 &",
		"echo $! > '/tmp/pid'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q:\n%s", want, got)
		}
	}
}

func TestClaimName(t *testing.T) {
	if ClaimName("abc") != "llmbench-abc" {
		t.Fatal("claim name derivation changed")
	}
	if runtimePIDFile("abc") != "/tmp/llmbench-runtime-abc.pid" {
		t.Fatal("pid file derivation changed")
	}
}
