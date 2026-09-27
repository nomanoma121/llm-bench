package job

import (
	"fmt"
	"strings"
)

// FromIssueBody extracts the job spec from an Issue body. GitHub Issue Forms
// submit the spec as the first ```yaml fenced block; everything outside the
// block is human context and is ignored.
//
// The controller reads specs this way, so the extraction lives here rather
// than in GitHub-specific code: `llmbench job validate --issue` and the
// controller must agree about what the request is.
func FromIssueBody(body string) (Spec, error) {
	block, ok := firstFencedBlock(body, "yaml", "yml")
	if !ok {
		return Spec{}, fmt.Errorf("%w: no ```yaml block in the Issue body", ErrInvalid)
	}
	return Parse(strings.NewReader(block))
}

// firstFencedBlock returns the content of the first fenced code block whose
// info string is one of langs (case-insensitive).
func firstFencedBlock(body string, langs ...string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i := range lines {
		info, isFence := fenceInfo(lines[i])
		if !isFence || !hasFold(langs, info) {
			continue
		}
		var b strings.Builder
		for j := i + 1; j < len(lines); j++ {
			if closing, isFence := fenceInfo(lines[j]); isFence && closing == "" {
				return b.String(), true
			}
			b.WriteString(lines[j])
			b.WriteString("\n")
		}
		return "", false // unterminated block
	}
	return "", false
}

// fenceInfo reports whether the line opens or closes a code fence and returns
// its info string (empty for a closing fence).
func fenceInfo(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "```") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimLeft(t, "`")), true
}

func hasFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// Template returns the JobSpec template printed by `llmbench job init` and
// pre-filled in the matching Issue form. Both paths must stay in step, so the
// controller and a human author see the same field set.
func Template(kind Kind) (string, error) {
	switch kind {
	case KindBenchmark:
		return benchmarkTemplate, nil
	case KindOptimize:
		return optimizeTemplate, nil
	}
	return "", fmt.Errorf("%w: template kind must be %q or %q, got %q", ErrInvalid, KindBenchmark, KindOptimize, kind)
}

const benchmarkTemplate = `kind: benchmark
model:
  id: REPLACE-model-id
runtime:
  engine: llamacpp
  image: REPLACE-image@sha256:REPLACE-digest
  args: []
  ready:
    port: 8080
    path: /health
    timeout_seconds: 300
workload:
  cases:
    - name: short
      prompt: prompts/short.txt
      max_tokens: 256
      repeats: 3
  concurrency: 1
  sampling:
    temperature: 0
    seed: 1
metrics:
  collectors: [harness, runtime, nvidia]
output:
  dir: experiments/REPLACE-model-id
`

const optimizeTemplate = `kind: optimize
model:
  id: REPLACE-model-id
runtime:
  engine: llamacpp
  image: REPLACE-image@sha256:REPLACE-digest
  args: []
  ready:
    port: 8080
    path: /health
    timeout_seconds: 300
workload:
  cases:
    - name: short
      prompt: prompts/short.txt
      max_tokens: 256
      repeats: 3
  concurrency: 1
  sampling:
    temperature: 0
    seed: 1
metrics:
  collectors: [harness, runtime, nvidia]
source:
  repo: REPLACE-owner/REPLACE-runtime-repo
  ref: main
budget:
  max_rounds: 20
output:
  dir: experiments/REPLACE-model-id
`
