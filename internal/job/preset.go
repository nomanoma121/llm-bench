package job

// Preset is a model with the runtime and arguments it is known to run with on
// the GPU node. Issue forms offer these instead of a free-form model path.
type Preset struct {
	Name   string
	Model  string
	Engine string
	Args   []string
}

var flashNextOffload = []string{"--ctx-size", "32768", "-ngl", "99", "-fa", "on",
	"-ot", `ffn_(gate|up|down)_exps\.weight=CPU,per_layer_token_embd\.weight=CPU`}

var Presets = []Preset{
	{"llama.cpp · Qwen3.8-27B Q4_0", "Qwen3.8-27B/Qwen3.8-27B-Q4_0.gguf", "llamacpp", []string{"--ctx-size", "32768"}},
	{"llama.cpp · Flash-Next UD-Q4_K_XL · 1GPU", "UD-Q4_K_XL/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf", "llamacpp",
		append([]string{"--device", "CUDA0"}, flashNextOffload...)},
	{"llama.cpp · Flash-Next UD-Q4_K_XL · 2GPU", "UD-Q4_K_XL/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf", "llamacpp", flashNextOffload},
	{"strata · Flash-Next IQ2_XS · 1GPU", "strata/config/strata-iq2_xs.json", "strata", []string{"--gpu", "0"}},
	{"strata · Flash-Next IQ2_XS · 2GPU", "strata/config/strata-iq2_xs.json", "strata", []string{"--gpu", "0,1"}},
	{"strata · Flash-Next IQ2_XS 64K · 2GPU", "strata/config/strata-iq2_xs-64k.json", "strata", []string{"--gpu", "0,1"}},
	{"strata · Flash-Next UD-Q4_K_XL · 1GPU", "strata/config/strata-ud-q4_k_xl.json", "strata", []string{"--gpu", "0"}},
	{"freetoken · Flash-Next NVFP4 · 1GPU", "qwen38-flash-next-nvfp4", "freetoken", []string{"--gpu", "0", "--moe-strategy", "offload",
		"--text-model-only", "--max-running-requests", "1", "--kv-reserve-tokens", "32768", "--memory-ratio", "0.95"}},
}

func presetNamed(name string) (Preset, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}
