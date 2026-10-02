package harness

import "encoding/json"

// pi is the pi coding agent (https://pi.dev), which reads custom endpoints
// from models.json in its agent directory.
type pi struct{}

func (pi) Tools() []string {
	return []string{"node@22", "npm:@earendil-works/pi-coding-agent@1.0.0"}
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
		Argv:  []string{"pi", "--print", "--provider", "llmbench", "--model", p.Model, p.Prompt},
		Files: map[string][]byte{".pi/agent/models.json": models},
	}, nil
}
