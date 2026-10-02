package harness

import "encoding/json"

// The npm package fetches the binary in a postinstall script mise skips.
type openCode struct{}

func (openCode) Tools() []string {
	return []string{"github:anomalyco/opencode[asset_pattern=opencode-linux-x64.tar.gz]@1.18.34"}
}

func (openCode) Command(p Params) (Invocation, error) {
	model := map[string]any{"name": p.Model, "tool_call": true}
	if p.Context > 0 {
		model["limit"] = map[string]int{"context": p.Context, "output": maxOutputTokens}
	}
	config, err := json.Marshal(map[string]any{
		"autoupdate":  false,
		"share":       "disabled",
		"permission":  "allow",
		"model":       "llmbench/" + p.Model,
		"small_model": "llmbench/" + p.Model,
		"provider": map[string]any{"llmbench": map[string]any{
			"npm":     "@ai-sdk/openai-compatible",
			"name":    "llmbench",
			"options": map[string]string{"baseURL": p.BaseURL, "apiKey": apiKey},
			"models":  map[string]any{p.Model: model},
		}},
	})
	if err != nil {
		return Invocation{}, err
	}
	return Invocation{
		Argv: []string{"opencode", "run", "--auto", "--model", "llmbench/" + p.Model},
		Task: []string{p.Prompt},
		Env:  map[string]string{"OPENCODE_CONFIG_CONTENT": string(config), "OPENCODE_DISABLE_AUTOUPDATE": "1"},
	}, nil
}
