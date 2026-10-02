package harness

import (
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// dsh is DeepSeek Harness (https://github.com/deepseek-ai/deepseek-harness),
// run with its headless profile and a patch that adds the endpoint as a
// custom provider and makes it the default model.
type dsh struct{}

func (dsh) Tools() []string {
	return []string{"node@22", "npm:@deepseek-ai/dsh@0.2.0-rc.2"}
}

func (dsh) Command(p Params) (Invocation, error) {
	model := map[string]any{"id": p.Model, "maxTokens": maxOutputTokens}
	if p.Context > 0 {
		model["contextWindow"] = p.Context
	}
	patch, err := yaml.Marshal([]any{
		map[string]any{"id": "llm-pi-ai", "config": map[string]any{"providers": map[string]any{"llmbench": map[string]any{
			"apiKeyEnv": "LLMBENCH_API_KEY",
			"api":       "openai-completions",
			"baseURL":   p.BaseURL,
			"models":    []any{model},
		}}}},
		map[string]any{"id": "agent-default-model", "config": map[string]string{"provider": "llmbench", "model": p.Model}},
	})
	if err != nil {
		return Invocation{}, err
	}
	return Invocation{
		Argv:  []string{"dsh", "--profile", "headless", "--patch", filepath.Join(p.Home, "llmbench.patch.yml"), p.Prompt},
		Env:   map[string]string{"DSH_HOME": filepath.Join(p.Home, ".dsh"), "LLMBENCH_API_KEY": apiKey},
		Files: map[string][]byte{"llmbench.patch.yml": patch},
	}, nil
}
