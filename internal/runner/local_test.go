package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"encoding/json"
	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
)

func recipe(t *testing.T, invoke []string) string {
	t.Helper()
	cfg := experiment.Config{
		Model:     "example-model",
		Benchmark: "benchmarks/visual/prompt.md",
		Target:    "local",
		Runtime:   experiment.Runtime{Engine: "example", ContextSize: 4096},
		Invoke:    experiment.Invoke{Argv: invoke},
	}
	b, err := experiment.CanonicalJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func artifactRun(dir, recipeJSON string) run.Run {
	return run.Run{
		ID:           "r1",
		RecipeJSON:   recipeJSON,
		Artifacts:    run.Artifacts{Dir: dir},
		PromptSHA256: "x",
	}
}

func TestLocalExecuteWritesIndexAndHashes(t *testing.T) {
	// Controller credentials must never leak into the child environment.
	t.Setenv("LLMBENCH_GITHUB_TOKEN", "secret")
	t.Setenv("LLMBENCH_API_TOKEN", "secret")

	dir := t.TempDir()
	inputDir := filepath.Join(dir, "input")
	if err := os.MkdirAll(inputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := "Create a scene."
	if err := os.WriteFile(filepath.Join(inputDir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}

	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c",
		`printf '<html></html>' > "$LLMBENCH_OUTPUT_DIR/index.html"; env > env.txt`}))

	artifacts, err := l.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.Artifacts.Dir != dir {
		t.Fatalf("dir = %q", artifacts.Artifacts.Dir)
	}
	index := filepath.Join(dir, "output", "index.html")
	b, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256HexOf(b)
	if artifacts.Artifacts.IndexSHA256 != sum {
		t.Fatalf("index hash mismatch: %s vs %s", artifacts.Artifacts.IndexSHA256, sum)
	}
	if artifacts.Artifacts.LogSHA256 == "" {
		t.Fatal("log hash missing")
	}

	env, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	e := string(env)
	for _, want := range []string{
		"LLMBENCH_RUN_ID=r1",
		"LLMBENCH_CONTEXT_SIZE=4096",
		"LLMBENCH_MODEL_ID=example-model",
		"LLMBENCH_PROMPT_PATH=" + filepath.Join(dir, "input", "prompt.md"),
	} {
		if !strings.Contains(e, want) {
			t.Errorf("env missing %q:\n%s", want, e)
		}
	}
	if strings.Contains(e, "secret") {
		t.Fatal("controller credentials leaked into child environment")
	}
}

func sha256HexOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestLocalExecuteFailsWithoutIndex(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c", "echo done"}))
	if _, err := l.Execute(context.Background(), r); err == nil {
		t.Fatal("expected failure when index.html is missing")
	}
}

func TestLocalExecuteFailsOnNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c", "exit 3"}))
	if _, err := l.Execute(context.Background(), r); err == nil {
		t.Fatal("expected failure on non-zero exit")
	}
}

func TestLocalExecuteRequiresRecipeSnapshot(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)
	r := artifactRun(dir, "not-json")
	if _, err := l.Execute(context.Background(), r); err == nil {
		t.Fatal("expected recipe decode failure")
	}
}

func TestLocalExecuteAbsolutizesRelativeDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	input := filepath.Join("reldir", "input")
	if err := os.MkdirAll(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLocal(".") // relative models root
	r := artifactRun("reldir", recipe(t, []string{"/bin/sh", "-c",
		`printf 'x' > "$LLMBENCH_OUTPUT_DIR/index.html"; pwd > pwd.txt; echo "$LLMBENCH_PROMPT_PATH" > pp.txt`}))
	artifacts, err := l.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(artifacts.Artifacts.Dir) {
		t.Fatalf("artifact dir not absolute: %q", artifacts.Artifacts.Dir)
	}
	pwd, _ := os.ReadFile(filepath.Join(tmp, "reldir", "pwd.txt"))
	if got := strings.TrimSpace(string(pwd)); !filepath.IsAbs(got) {
		t.Fatalf("child did not run under the absolute artifact dir: %q", got)
	}
	pp, _ := os.ReadFile(filepath.Join(tmp, "reldir", "pp.txt"))
	if got := strings.TrimSpace(string(pp)); !filepath.IsAbs(got) {
		t.Fatalf("prompt path not absolute: %q", got)
	}
	if _, err := os.Stat(filepath.Join(tmp, "reldir", "reldir")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("artifact dir was doubled inside the child")
	}
}

func TestLocalExecuteRejectsExtraPayloadFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input", "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c",
		`printf 'x' > "$LLMBENCH_OUTPUT_DIR/index.html"; printf 'y' > "$LLMBENCH_OUTPUT_DIR/app.js"`}))
	if _, err := l.Execute(context.Background(), r); err == nil {
		t.Fatal("expected the single-file payload contract to reject app.js")
	}
	// Nothing is sealed when the contract fails.
	if _, err := os.Stat(filepath.Join(dir, "output")); !os.IsNotExist(err) {
		t.Fatalf("output must not be sealed: %v", err)
	}
}

func TestLocalExecuteRejectsReservedManifestName(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input", "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c",
		`printf 'x' > "$LLMBENCH_OUTPUT_DIR/index.html"; printf '{}' > "$LLMBENCH_OUTPUT_DIR/manifest.json"`}))
	if _, err := l.Execute(context.Background(), r); err == nil {
		t.Fatal("expected the reserved manifest name to be rejected")
	}
}

func TestLocalExecuteRecordsArtifactDigest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input", "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c", `printf '<html></html>' > "$LLMBENCH_OUTPUT_DIR/index.html"`}))
	artifacts, err := l.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.Artifacts.ArtifactDigest == "" {
		t.Fatal("artifact digest must be recorded at seal time")
	}
	digest, err := provenance.ArtifactDigest(filepath.Join(dir, "output"))
	if err != nil {
		t.Fatal(err)
	}
	if digest != artifacts.Artifacts.ArtifactDigest {
		t.Fatalf("digest = %s, want %s", artifacts.Artifacts.ArtifactDigest, digest)
	}
	if artifacts.Artifacts.IndexSHA256 == "" {
		t.Fatal("index hash must be recorded")
	}
}

// protocolRun freezes a driver-based protocol on a run: the driver prints the
// raw measurement to stdout, which is the harness's trusted channel.
func protocolRun(t *testing.T, r run.Run, driverArgv []string, required []string) run.Run {
	t.Helper()
	p := operator.MeasurementProtocol{
		SchemaVersion: 1,
		Driver: operator.ExecutionSpec{
			ContentDigest: "sha256:driver", ExecMode: "sandbox-exec", OutputMode: "stdout-transport",
		},
		DriverArgv:      driverArgv,
		Workload:        operator.MeasurementWorkload{Matrix: []operator.WorkloadCase{{Name: "decode", DecodeSteps: 10}}},
		RequiredSources: required,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	r.MeasurementProtocolID = "longctx"
	r.MeasurementProtocolJSON = string(b)
	r.MeasurementProtocolDigest = "pd"
	return r
}

func TestLocalMeasurementRunSealsDriverEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input", "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c", "exit 1"}))
	r.Kind = run.RunKindMeasurement
	r = protocolRun(t, r, []string{"/bin/sh", "-c", `printf '%s' '{"metrics":[{"name":"decode_step_ms","value":17.71,"unit":"ms/step","labels":{"depth":"64k"},"samples":385}]}'`}, []string{"driver"})

	// The recipe's invoke must not run for a measurement run: it exits 1 and
	// would fail the run if it did.
	out, err := l.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if out.Artifacts.ArtifactDigest != "" {
		t.Fatal("a measurement run must not seal a visual artifact")
	}
	if out.Evidence.Digest == "" || !out.Evidence.Valid {
		t.Fatalf("evidence = %+v", out.Evidence)
	}
	e, err := measurement.Verify(out.Evidence.Path, out.Evidence.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != "measurement" || e.Protocol.ID != "longctx" || e.Protocol.Digest != "pd" {
		t.Fatalf("identity = %+v", e)
	}
	var harnessWall, driverStep bool
	for _, m := range e.Metrics {
		switch m.Name {
		case "decode_step_ms":
			driverStep = true
			if m.Source != measurement.SourceDriver {
				t.Fatalf("driver metric source = %q", m.Source)
			}
		case "wall_clock_ms":
			harnessWall = true
			if m.Source != measurement.SourceHarness {
				t.Fatalf("harness metric source = %q", m.Source)
			}
		}
	}
	if !harnessWall || !driverStep {
		t.Fatalf("metrics = %+v", e.Metrics)
	}
}

func TestLocalUnusableDriverOutputSealsInvalidEvidence(t *testing.T) {
	cases := map[string]struct {
		driver   []string
		required []string
	}{
		"junk output":             {driver: []string{"/bin/sh", "-c", "printf nonsense"}, required: []string{"driver"}},
		"driver fails":            {driver: []string{"/bin/sh", "-c", "exit 3"}, required: []string{"driver"}},
		"required source missing": {driver: []string{"/bin/sh", "-c", "exit 0"}, required: []string{"driver"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "input", "prompt.md"), []byte("p"), 0o644); err != nil {
				t.Fatal(err)
			}
			l := NewLocal(dir)
			r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c", "exit 0"}))
			r.Kind = run.RunKindMeasurement
			r = protocolRun(t, r, tc.driver, tc.required)

			out, err := l.Execute(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if out.Evidence.Digest == "" || out.Evidence.Valid {
				t.Fatalf("expected sealed but invalid evidence: %+v", out.Evidence)
			}
			if len(out.Evidence.InvalidReasons) == 0 {
				t.Fatal("an invalid measurement must carry a reason")
			}
		})
	}
}

func TestLocalVisualRunWithProtocolAlsoSealsEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input", "prompt.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLocal(dir)
	r := artifactRun(dir, recipe(t, []string{"/bin/sh", "-c", `printf '<html/>' > "$LLMBENCH_OUTPUT_DIR/index.html"`}))
	r = protocolRun(t, r, []string{"/bin/sh", "-c", `printf '%s' '{"metrics":[{"name":"wall_clock_ms","value":1,"unit":"ms"}]}'`}, []string{"driver"})

	out, err := l.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if out.Artifacts.ArtifactDigest == "" {
		t.Fatal("a visual run must still seal its artifact")
	}
	if out.Evidence.Digest == "" || !out.Evidence.Valid {
		t.Fatalf("a visual run with a protocol must carry evidence: %+v", out.Evidence)
	}
	e, err := measurement.Verify(out.Evidence.Path, out.Evidence.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != "visual" {
		t.Fatalf("kind = %q", e.Kind)
	}
}
