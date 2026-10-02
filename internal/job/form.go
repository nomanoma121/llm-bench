package job

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nomanoma121/llm-bench/internal/harness"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

// The issue body GitHub writes for a form has a "### <label>" section per
// field, so these labels are what FromIssue reads.
const (
	fieldModel        = "Model"
	fieldRuntime      = "Runtime"
	fieldGPUs         = "GPUs"
	fieldContext      = "Context"
	fieldMTP          = "MTP"
	fieldKVCache      = "KV cache"
	fieldExpertsOnCPU = "Experts on CPU"
	fieldRuntimeArgs  = "Extra runtime args"
	fieldBenchmark    = "Benchmark"
	fieldHarness      = "Harness"
	fieldHarnessArgs  = "Extra harness args"
	fieldEffort       = "Reasoning effort"
	fieldMaxTokens    = "Max tokens"
	fieldRepo         = "Source repository"
	fieldRef          = "Source ref"
	fieldMaxRounds    = "Max rounds"
	fieldOverride     = "Spec override"
	fieldNotes        = "Notes"

	optDefault = "default"
	optOn      = "on"
	optOff     = "off"
	// GitHub rejects "None" as a dropdown option.
	noHarness   = "direct"
	noReasoning = "no thinking"
	noResponse  = "_No response_"
)

var (
	efforts  = []string{optDefault, noReasoning, "low", "medium", "high"}
	toggles  = []string{optDefault, optOn, optOff}
	contexts = []string{optDefault, "32K", "64K", "128K"}
)

type issueForm struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Title       string      `yaml:"title"`
	Labels      []string    `yaml:"labels"`
	Body        []formField `yaml:"body"`
}

type formField struct {
	Type        string          `yaml:"type"`
	ID          string          `yaml:"id"`
	Attributes  fieldAttributes `yaml:"attributes"`
	Validations *validations    `yaml:"validations,omitempty"`
}

type fieldAttributes struct {
	Label       string `yaml:"label"`
	Description string `yaml:"description,omitempty"`
	Options     []any  `yaml:"options,omitempty"`
	Multiple    bool   `yaml:"multiple,omitempty"`
	Default     *int   `yaml:"default,omitempty"`
	Value       string `yaml:"value,omitempty"`
	Render      string `yaml:"render,omitempty"`
}

type validations struct {
	Required bool `yaml:"required"`
}

// Benchmarks lists the directories under dir that hold a prompt.md.
func Benchmarks(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "prompt.md")); e.IsDir() && err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Form is the GitHub issue form for kind.
func Form(kind Kind, benchmarks []string) ([]byte, error) {
	required := &validations{Required: true}
	dropdown := func(id, label, description string, options []string, def string) formField {
		i := max(slices.Index(options, def), 0)
		f := formField{Type: "dropdown", ID: id, Validations: required,
			Attributes: fieldAttributes{Label: label, Description: description, Default: &i}}
		for _, o := range options {
			f.Attributes.Options = append(f.Attributes.Options, o)
		}
		return f
	}
	multiple := func(f formField) formField {
		f.Attributes.Multiple = true
		return f
	}
	input := func(id, label, description, value string) formField {
		return formField{Type: "input", ID: id, Attributes: fieldAttributes{Label: label, Description: description, Value: value}}
	}
	var models []string
	for _, m := range Models {
		models = append(models, m.Name)
	}
	var gpus []string
	for _, g := range runtime.NodeGPUs {
		gpus = append(gpus, g.String())
	}

	body := []formField{
		dropdown("model", fieldModel, "", models, ""),
		dropdown("runtime", fieldRuntime, "", runtime.Engines, ""),
		multiple(dropdown("gpus", fieldGPUs, "", gpus, "")),
		dropdown("context", fieldContext, "", contexts, "32K"),
		dropdown("mtp", fieldMTP, "Speculative decoding with the model's multi-token prediction head.", toggles, ""),
		dropdown("kv_cache", fieldKVCache, "", append([]string{optDefault}, runtime.KVCacheTypes...), ""),
		dropdown("experts_on_cpu", fieldExpertsOnCPU, "Keep the MoE experts in host memory.", toggles, ""),
		input("runtime_args", fieldRuntimeArgs, "Passed to the engine as they are, split like a shell does.", ""),
		dropdown("benchmark", fieldBenchmark, "", benchmarks, "visual"),
		dropdown("harness", fieldHarness, "Run the prompt through a coding agent instead of a direct request.", append([]string{noHarness}, harness.Names...), ""),
		input("harness_args", fieldHarnessArgs, "Passed to the harness before the task.", ""),
		dropdown("effort", fieldEffort, "", efforts, "low"),
		input("max_tokens", fieldMaxTokens, "Caps a direct request; unused with a harness.", "28672"),
	}
	f := issueForm{Name: "Benchmark", Description: "Measure a model and runtime once and open a PR with the result",
		Title: "[benchmark] ", Labels: []string{"llmbench:benchmark"}}
	if kind == Optimize {
		f = issueForm{Name: "Optimize", Description: "Let the agent optimize a runtime on the GPU sandbox and open a PR",
			Title: "[optimize] ", Labels: []string{"llmbench:optimize"}}
		body = append(body,
			input("repo", fieldRepo, "", "ggml-org/llama.cpp"),
			input("ref", fieldRef, "", "master"),
			input("max_rounds", fieldMaxRounds, "", "20"))
	}
	f.Body = append(body,
		formField{Type: "textarea", ID: "override", Attributes: fieldAttributes{Label: fieldOverride, Render: "yaml",
			Description: "Job spec YAML laid over what the fields above build."}},
		formField{Type: "textarea", ID: "notes", Attributes: fieldAttributes{Label: fieldNotes}})

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Generated by `llmbench job form %s > .github/ISSUE_TEMPLATE/%s.yml`.\n", kind, kind)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// FromIssue builds a spec from the body GitHub writes for an issue form.
func FromIssue(kind Kind, body string) (Spec, error) {
	f := sections(body)
	var errs []error
	fail := func(field string, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", field, fmt.Sprintf(format, args...)))
	}
	number := func(field string) int {
		if f[field] == "" {
			return 0
		}
		n, err := strconv.Atoi(f[field])
		if err != nil {
			fail(field, "%q is not a number", f[field])
		}
		return n
	}
	toggle := func(field string) *bool {
		switch f[field] {
		case optOn:
			return new(true)
		case optOff:
			return new(false)
		}
		return nil
	}
	args := func(field string) []string {
		a, err := splitArgs(f[field])
		if err != nil {
			fail(field, "%v", err)
		}
		return a
	}

	s := Spec{Kind: kind, Sampling: &Sampling{Temperature: new(0.0), Seed: new(int64(1))}}
	s.Runtime.Engine = f[fieldRuntime]
	if m, ok := modelNamed(f[fieldModel]); !ok {
		fail(fieldModel, "unknown %q", f[fieldModel])
	} else if s.Model, ok = m.Files[s.Runtime.Engine]; !ok {
		fail(fieldModel, "%s has no files for %s", m.Name, s.Runtime.Engine)
	}
	for _, name := range strings.Split(f[fieldGPUs], ", ") {
		if name == "" {
			continue
		}
		i := slices.IndexFunc(runtime.NodeGPUs, func(g runtime.GPU) bool { return g.String() == name })
		if i < 0 {
			fail(fieldGPUs, "unknown %q", name)
			continue
		}
		s.Runtime.GPUs = append(s.Runtime.GPUs, runtime.NodeGPUs[i].Index)
	}
	slices.Sort(s.Runtime.GPUs)
	if c := f[fieldContext]; c != "" && c != optDefault {
		k, err := strconv.Atoi(strings.TrimSuffix(c, "K"))
		if err != nil {
			fail(fieldContext, "%q is not like 32K", c)
		}
		s.Runtime.Context = k * 1024
	}
	s.Runtime.MTP = toggle(fieldMTP)
	if kv := f[fieldKVCache]; kv != optDefault {
		s.Runtime.KVCache = kv
	}
	s.Runtime.ExpertsOnCPU = toggle(fieldExpertsOnCPU)
	s.Runtime.Args = args(fieldRuntimeArgs)

	c := Case{Name: f[fieldBenchmark], Prompt: "benchmarks/" + f[fieldBenchmark] + "/prompt.md"}
	if h := f[fieldHarness]; h != "" && h != noHarness {
		c.Harness = &Harness{Name: h, Args: args(fieldHarnessArgs)}
	} else {
		c.MaxTokens = number(fieldMaxTokens)
	}
	s.Workload = []Case{c}
	switch e := f[fieldEffort]; e {
	case optDefault:
	case noReasoning:
		s.Sampling.ReasoningEffort = "none"
	default:
		s.Sampling.ReasoningEffort = e
	}
	if kind == Optimize {
		s.Source = &Source{Repo: f[fieldRepo], Ref: f[fieldRef]}
		s.Budget = &Budget{MaxRounds: number(fieldMaxRounds)}
	}
	if len(errs) > 0 {
		return Spec{}, fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
	}

	o := f[fieldOverride]
	if block, ok := yamlBlock(o); ok {
		o = block
	}
	// GitHub writes an empty fenced block for an empty YAML field.
	if strings.TrimSpace(o) != "" {
		dec := yaml.NewDecoder(strings.NewReader(o))
		dec.KnownFields(true)
		if err := dec.Decode(&s); err != nil {
			return Spec{}, fmt.Errorf("%w: %s: %w", ErrInvalid, fieldOverride, err)
		}
	}
	return s, s.Validate()
}

// sections maps each "### <label>" heading outside code blocks to the text
// under it.
func sections(body string) map[string]string {
	out := map[string]string{}
	var label string
	var lines []string
	flush := func() {
		if label != "" {
			v := strings.TrimSpace(strings.Join(lines, "\n"))
			if v == noResponse {
				v = ""
			}
			out[label] = v
		}
	}
	fenced := false
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		}
		if h, ok := strings.CutPrefix(line, "### "); ok && !fenced {
			flush()
			label, lines = strings.TrimSpace(h), nil
			continue
		}
		lines = append(lines, line)
	}
	flush()
	return out
}

// splitArgs splits s into words the way a POSIX shell does, without
// expansions.
func splitArgs(s string) ([]string, error) {
	var args []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == ' ' || ch == '\t' || ch == '\n':
			if inWord {
				args, inWord = append(args, word.String()), false
				word.Reset()
			}
		case ch == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated '")
			}
			word.WriteString(s[i+1 : i+1+end])
			i, inWord = i+1+end, true
		case ch == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(`"\$`+"`", s[i+1]) >= 0 {
					i++
				}
				word.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New(`unterminated "`)
			}
			inWord = true
		case ch == '\\' && i+1 < len(s):
			i++
			word.WriteByte(s[i])
			inWord = true
		default:
			word.WriteByte(ch)
			inWord = true
		}
	}
	if inWord {
		args = append(args, word.String())
	}
	return args, nil
}
