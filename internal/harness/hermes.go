package harness

import "gopkg.in/yaml.v3"

// hermes is Nous Research's Hermes Agent (https://github.com/NousResearch/hermes-agent),
// with the endpoint as its custom provider in the config.yaml of its own home.
type hermes struct{}

func (hermes) Tools() []string {
	return []string{"uv@0.12.22", "pipx:hermes-agent@0.19.0"}
}

func (hermes) Command(p Params) (Invocation, error) {
	model := map[string]any{"provider": "custom", "default": p.Model, "base_url": p.BaseURL, "api_key": apiKey}
	if p.Context > 0 {
		model["context_length"] = p.Context
	}
	config, err := yaml.Marshal(map[string]any{"model": model})
	if err != nil {
		return Invocation{}, err
	}
	return Invocation{
		// -Q prints only the answer; --yolo approves every command; rules
		// such as AGENTS.md are not injected.
		Argv:  []string{"hermes", "chat", "-Q", "--yolo", "--ignore-rules", "-m", p.Model, "-q", p.Prompt},
		Files: map[string][]byte{".hermes/config.yaml": config},
	}, nil
}
