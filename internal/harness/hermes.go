package harness

import "gopkg.in/yaml.v3"

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
		Argv:  []string{"hermes", "chat", "-Q", "--yolo", "--ignore-rules", "-m", p.Model},
		Task:  []string{"-q", p.Prompt},
		Files: map[string][]byte{".hermes/config.yaml": config},
	}, nil
}
