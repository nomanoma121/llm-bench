package runtime

import (
	"fmt"
	"slices"
	"strconv"
)

// Settings are the runtime options every engine understands. Each adapter
// turns them into its own arguments; zero values leave the engine's default.
type Settings struct {
	GPUs         int    `yaml:"gpus,omitempty" json:"gpus,omitempty"`
	Context      int    `yaml:"context,omitempty" json:"context,omitempty"`
	MTP          *bool  `yaml:"mtp,omitempty" json:"mtp,omitempty"`
	KVCache      string `yaml:"kv_cache,omitempty" json:"kv_cache,omitempty"`
	ExpertsOnCPU *bool  `yaml:"experts_on_cpu,omitempty" json:"experts_on_cpu,omitempty"`
}

const MaxGPUs = 2

var KVCacheTypes = []string{"f16", "q8_0", "q4_0"}

func (s Settings) check() error {
	if s.GPUs < 0 || s.GPUs > MaxGPUs {
		return fmt.Errorf("gpus must be between 1 and %d, got %d", MaxGPUs, s.GPUs)
	}
	if s.Context < 0 {
		return fmt.Errorf("context must be positive, got %d", s.Context)
	}
	if s.KVCache != "" && !slices.Contains(KVCacheTypes, s.KVCache) {
		return fmt.Errorf("kv_cache %q must be one of %v", s.KVCache, KVCacheTypes)
	}
	return nil
}

// devices is "0" or "0,1" for n GPUs.
func devices(n int, prefix string) string {
	s := ""
	for i := range n {
		if i > 0 {
			s += ","
		}
		s += prefix + strconv.Itoa(i)
	}
	return s
}

func unsupported(engine, setting string) error {
	return fmt.Errorf("%s does not support %s", engine, setting)
}
