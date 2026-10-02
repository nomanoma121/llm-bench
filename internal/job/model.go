package job

// Model is a model on the GPU node, with the file each engine loads it from
// under the models directory.
type Model struct {
	Name  string
	Files map[string]string
}

var Models = []Model{
	{"Qwen3.8-27B Q4_0", map[string]string{
		"llamacpp": "Qwen3.8-27B/Qwen3.8-27B-Q4_0.gguf",
	}},
	{"Flash-Next UD-Q4_K_XL", map[string]string{
		"llamacpp": "UD-Q4_K_XL/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf",
		"strata":   "strata/config/strata-ud-q4_k_xl.json",
	}},
	{"Flash-Next IQ2_XS", map[string]string{
		"strata": "strata/config/strata-iq2_xs.json",
	}},
	{"Flash-Next NVFP4", map[string]string{
		"freetoken": "qwen38-flash-next-nvfp4",
	}},
}

func modelNamed(name string) (Model, bool) {
	for _, m := range Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}
