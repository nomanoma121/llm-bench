package hook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/run"
)

func plan(name, acquire, release string) operator.PlannedHook {
	return operator.PlannedHook{
		PlanVersion: operator.PlanVersion,
		Kind:        operator.KindCommand,
		Name:        name,
		Command:     []string{acquire},
		Release:     []string{release},
	}
}

func script(t *testing.T, code string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte(code), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFromPlanRejectsWrongKind(t *testing.T) {
	p := plan("x", "true", "true")
	p.Kind = operator.KindGitOps
	if _, err := FromPlan(p, ""); err == nil {
		t.Fatal("expected kind error")
	}
}

func TestAcquirePendingExit75(t *testing.T) {
	h, err := FromPlan(plan("lease", script(t, "#!/bin/sh\nexit 75\n"), "true"), "")
	if err != nil {
		t.Fatal(err)
	}
	err = h.Acquire(context.Background())
	if !errors.Is(err, run.ErrPending) {
		t.Fatalf("want ErrPending, got %v", err)
	}
}

func TestAcquireFailureCarriesOutput(t *testing.T) {
	h, err := FromPlan(plan("lease", script(t, "#!/bin/sh\necho boom >&2\nexit 1\n"), "true"), "")
	if err != nil {
		t.Fatal(err)
	}
	err = h.Acquire(context.Background())
	if err == nil || errors.Is(err, run.ErrPending) {
		t.Fatalf("want plain failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should contain command output: %v", err)
	}
}

func TestReleaseUsesReleaseArgvFromPlan(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	h, err := FromPlan(operator.PlannedHook{
		PlanVersion: operator.PlanVersion,
		Kind:        operator.KindCommand,
		Name:        "lease",
		Command:     []string{"true"},
		Release:     []string{"/bin/sh", "-c", "echo released >> \"$LOG\""},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOG", log)
	if err := h.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(b), "released") {
		t.Fatalf("release script did not run: %v (%s)", err, b)
	}
}
