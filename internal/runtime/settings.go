package runtime

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Settings are the runtime options every engine understands. Each adapter
// turns them into its own arguments; zero values leave the engine's default.
type Settings struct {
	// GPUs are indices into NodeGPUs; none leaves the engine's default.
	GPUs         []int  `yaml:"gpus,flow,omitempty" json:"gpus,omitempty"`
	Context      int    `yaml:"context,omitempty" json:"context,omitempty"`
	MTP          *bool  `yaml:"mtp,omitempty" json:"mtp,omitempty"`
	KVCache      string `yaml:"kv_cache,omitempty" json:"kv_cache,omitempty"`
	ExpertsOnCPU *bool  `yaml:"experts_on_cpu,omitempty" json:"experts_on_cpu,omitempty"`
}

type GPU struct {
	Index int
	Name  string
}

// NodeGPUs are the GPU node's cards, numbered as nvidia-smi numbers them.
var NodeGPUs = []GPU{{0, "RTX 3060 12GB"}, {1, "RTX 3060 12GB"}}

func (g GPU) String() string { return fmt.Sprintf("GPU %d · %s", g.Index, g.Name) }

var KVCacheTypes = []string{"f16", "q8_0", "q4_0"}

func (s Settings) check() error {
	seen := map[int]bool{}
	for _, g := range s.GPUs {
		if g < 0 || g >= len(NodeGPUs) || seen[g] {
			return fmt.Errorf("gpus %v must be distinct GPUs of the node, 0 to %d", s.GPUs, len(NodeGPUs)-1)
		}
		seen[g] = true
	}
	if s.Context < 0 {
		return fmt.Errorf("context must be positive, got %d", s.Context)
	}
	if s.KVCache != "" && !slices.Contains(KVCacheTypes, s.KVCache) {
		return fmt.Errorf("kv_cache %q must be one of %v", s.KVCache, KVCacheTypes)
	}
	return nil
}

// devices joins gpus as "0,1", each with prefix.
func devices(gpus []int, prefix string) string {
	s := make([]string, len(gpus))
	for i, g := range gpus {
		s[i] = prefix + strconv.Itoa(g)
	}
	return strings.Join(s, ",")
}

func unsupported(engine, setting string) error {
	return fmt.Errorf("%s does not support %s", engine, setting)
}
