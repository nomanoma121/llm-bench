package harness

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every harness builds a run that carries the task and the model and whose
// settings parse.
func TestCommands(t *testing.T) {
	for _, name := range []string{"opencode", "pi", "dsh", "hermes"} {
		h, err := New(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(h.Tools()) == 0 || !strings.Contains(h.Tools()[len(h.Tools())-1], "@") {
			t.Errorf("%s: tools must be pinned: %v", name, h.Tools())
		}
		for _, ctx := range []int{0, 65536} {
			p := Params{Prompt: "write \"index.html\"", BaseURL: "http://127.0.0.1:1/v1", Model: "m", Context: ctx, Home: "/h"}
			inv, err := h.Command(p)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			everything := strings.Join(inv.Argv, " ") + fmtMap(inv.Env) + fmtFiles(inv.Files)
			if !slices.Contains(inv.Argv, p.Prompt) || !strings.Contains(everything, p.BaseURL) || !strings.Contains(everything, p.Model) {
				t.Errorf("%s: the run does not carry the task, endpoint and model: %q %v", name, inv.Argv, inv.Env)
			}
			for path, b := range inv.Files {
				var v any
				if strings.HasSuffix(path, ".json") && json.Unmarshal(b, &v) != nil || strings.HasSuffix(path, ".yml") && yaml.Unmarshal(b, &v) != nil {
					t.Errorf("%s: %s does not parse: %s", name, path, b)
				}
			}
			for k, v := range inv.Env {
				if strings.HasPrefix(v, "{") && !json.Valid([]byte(v)) {
					t.Errorf("%s: %s is not JSON: %s", name, k, v)
				}
			}
		}
	}
	if _, err := New("nope"); err == nil {
		t.Fatal("an unknown harness was accepted")
	}
}

func fmtMap(m map[string]string) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(" " + k + "=" + v)
	}
	return b.String()
}

func fmtFiles(m map[string][]byte) string {
	var b strings.Builder
	for _, v := range m {
		b.Write(v)
	}
	return b.String()
}
