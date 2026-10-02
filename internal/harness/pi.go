package harness

import (
	"encoding/json"
	"path/filepath"
)

type pi struct{}

func (pi) Tools() []string {
	return []string{"node@24", "npm:@earendil-works/pi-coding-agent@1.0.0"}
}

func (pi) Command(p Params) (Invocation, error) {
	model := map[string]any{"id": p.Model, "maxTokens": maxOutputTokens}
	if p.Context > 0 {
		model["contextWindow"] = p.Context
	}
	models, err := json.Marshal(map[string]any{"providers": map[string]any{"llmbench": map[string]any{
		"baseUrl": p.BaseURL,
		"api":     "openai-completions",
		"apiKey":  apiKey,
		"models":  []any{model},
	}}})
	if err != nil {
		return Invocation{}, err
	}
	return Invocation{
		Argv:  []string{"pi", "--print", "--provider", "llmbench", "--model", p.Model},
		Task:  []string{p.Prompt},
		Env:   map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(p.Home, "pi")},
		Files: map[string][]byte{"pi/models.json": models},
	}, nil
}
