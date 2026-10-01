package sandbox

import "testing"

func TestWarmPool(t *testing.T) {
	c := &Client{WarmPool: "gpu", EngineWarmPools: map[string]string{"strata": "gpu-strata"}}
	if c.warmPool("strata") != "gpu-strata" || c.warmPool("llamacpp") != "gpu" {
		t.Fatal(c.warmPool("strata"), c.warmPool("llamacpp"))
	}
}
