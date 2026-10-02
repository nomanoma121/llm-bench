package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every defined harness must render, and what looks like JSON must parse.
func TestDefinitionsRender(t *testing.T) {
	defs, err := Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"opencode", "pi"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("%s is not defined", name)
		}
	}
	for _, ctx := range []int{0, 32768} {
		p := Params{Prompt: "write \"index.html\"", BaseURL: "http://127.0.0.1:1/v1", Model: "m", Context: ctx, Home: "/h"}
		for name, d := range defs {
			r, err := d.render(p)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if r.argv[len(r.argv)-1] != p.Prompt {
				t.Errorf("%s: the prompt is not the last argument: %q", name, r.argv)
			}
			var docs []string
			for _, e := range r.env {
				docs = append(docs, e[strings.Index(e, "=")+1:])
			}
			for _, f := range r.files {
				docs = append(docs, f)
			}
			for _, doc := range docs {
				if strings.HasPrefix(doc, "{") && !json.Valid([]byte(doc)) {
					t.Errorf("%s (context %d): invalid JSON: %s", name, ctx, doc)
				}
			}
		}
	}
}
