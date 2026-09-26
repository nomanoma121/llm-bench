package operator

import "testing"

func validProtocol() MeasurementProtocol {
	return MeasurementProtocol{
		SchemaVersion: 1,
		Driver: ExecutionSpec{
			ContentDigest: "sha256:driver",
			ExecMode:      "sandbox-exec",
			OutputMode:    "stdout-transport",
		},
		DriverArgv:      []string{"/opt/bench/driver", "--json"},
		Workload:        MeasurementWorkload{Matrix: []WorkloadCase{{Name: "decode-64k", ContextDepth: 65536, DecodeSteps: 385}}},
		RequiredSources: []string{"driver"},
	}
}

func measurementConfig() Config {
	return Config{
		Targets: map[string]Target{
			"gpu":   {MeasurementProtocols: []string{"longctx"}},
			"local": {},
		},
		MeasurementProtocols: map[string]MeasurementProtocol{"longctx": validProtocol()},
		PromotionPolicies: map[string]PromotionPolicy{"latency": {
			SchemaVersion: 1, PrimaryMetric: "decode_step_ms", Direction: "min",
			AbsFloor: 0.3, AllowedSources: []string{"driver", "external_gpu"},
			Guards: PromotionGuards{MinVRAMHeadroomMiB: 512},
		}},
		SamplingPolicies: map[string]SamplingPolicy{"three-pairs": {
			SchemaVersion: 1, InitialPairs: 3, MaxPairs: 3, OrderRule: "balanced-randomized-pairs",
		}},
		OptimizationProfiles: map[string]OptimizationProfile{"v100": {
			Kind: KindMeasurement, Protocol: "longctx", Policy: "latency", Sampling: "three-pairs",
			MaxRounds: 20, MaxRuns: 160,
		}},
	}
}

func TestResolveSubmissionFreezesSnapshots(t *testing.T) {
	c := measurementConfig()
	sub, err := c.ResolveSubmission("gpu", "", KindMeasurement, "longctx")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Kind != KindMeasurement || sub.ProtocolID != "longctx" || sub.ProtocolDigest == "" || sub.ProtocolJSON == "" {
		t.Fatalf("submission = %+v", sub)
	}
	// The digest covers the whole canonical snapshot, so changing any typed
	// field changes the identity.
	p := validProtocol()
	p.KVFill = "to:64k"
	if _, digest, err := canonicalSnapshot(p); err != nil || digest == sub.ProtocolDigest {
		t.Fatalf("digest must change with the snapshot: %v", err)
	}

	// A profile fixes kind and protocol; the caller cannot override them.
	viaProfile, err := c.ResolveSubmission("gpu", "v100", KindVisual, "")
	if err != nil {
		t.Fatal(err)
	}
	if viaProfile.Kind != KindMeasurement || viaProfile.ProtocolID != "longctx" {
		t.Fatalf("profile must fix kind/protocol: %+v", viaProfile)
	}
	if viaProfile.PolicyDigest == "" || viaProfile.SamplingDigest == "" {
		t.Fatalf("profile must freeze policy and sampling: %+v", viaProfile)
	}
}

func TestResolveSubmissionRejectsInvalidRequests(t *testing.T) {
	type request struct {
		target, profile, kind, protocol string
	}
	cases := map[string]request{
		"unknown target":           {target: "nope", kind: KindMeasurement, protocol: "longctx"},
		"measurement no protocol":  {target: "gpu", kind: KindMeasurement},
		"protocol not allowlisted": {target: "local", kind: KindMeasurement, protocol: "longctx"},
		"unknown protocol":         {target: "gpu", kind: KindMeasurement, protocol: "other"},
		"unknown profile":          {target: "gpu", profile: "missing"},
		"bad kind":                 {target: "gpu", kind: "benchmark"},
	}
	for name, req := range cases {
		if _, err := measurementConfig().ResolveSubmission(req.target, req.profile, req.kind, req.protocol); err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
	}
	if err := measurementConfig().Validate(); err != nil {
		t.Fatalf("the reference configuration must validate: %v", err)
	}
}

func TestValidationEnforcesMVPConstraints(t *testing.T) {
	cases := map[string]func(*Config){
		"adaptive sampling cap": func(c *Config) {
			p := c.SamplingPolicies["three-pairs"]
			p.MaxPairs = 5
			c.SamplingPolicies["three-pairs"] = p
		},
		"gray zone beyond mvp": func(c *Config) {
			p := c.PromotionPolicies["latency"]
			p.GrayZone = PromotionGrayZone{Action: "needs-more-samples"}
			c.PromotionPolicies["latency"] = p
		},
		"driver without output boundary": func(c *Config) {
			p := c.MeasurementProtocols["longctx"]
			p.Driver.OutputMode = ""
			c.MeasurementProtocols["longctx"] = p
		},
		"unknown metric source": func(c *Config) {
			p := c.PromotionPolicies["latency"]
			p.AllowedSources = []string{"vibes"}
			c.PromotionPolicies["latency"] = p
		},
		"profile kind invalid": func(c *Config) {
			p := c.OptimizationProfiles["v100"]
			p.Kind = "benchmark"
			c.OptimizationProfiles["v100"] = p
		},
		"dangling profile protocol": func(c *Config) {
			p := c.OptimizationProfiles["v100"]
			p.Protocol = "gone"
			c.OptimizationProfiles["v100"] = p
		},
		"dangling profile policy": func(c *Config) {
			p := c.OptimizationProfiles["v100"]
			p.Policy = "gone"
			c.OptimizationProfiles["v100"] = p
		},
		"dangling profile sampling": func(c *Config) {
			p := c.OptimizationProfiles["v100"]
			p.Sampling = "gone"
			c.OptimizationProfiles["v100"] = p
		},
		"target allowlist dangling": func(c *Config) {
			t := c.Targets["gpu"]
			t.MeasurementProtocols = []string{"ghost"}
			c.Targets["gpu"] = t
		},
	}
	for name, mutate := range cases {
		c := measurementConfig()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: expected a validation error", name)
		}
	}
	// A visual run without any protocol stays legal.
	c := measurementConfig()
	c.MeasurementProtocols = nil
	c.PromotionPolicies = nil
	c.SamplingPolicies = nil
	c.OptimizationProfiles = nil
	for id, tgt := range c.Targets {
		tgt.MeasurementProtocols = nil
		c.Targets[id] = tgt
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("visual-only configuration must validate: %v", err)
	}
	if _, err := c.ResolveSubmission("local", "", "", ""); err != nil {
		t.Fatalf("plain visual submission must resolve: %v", err)
	}
}

func TestWorkloadDigestIsCanonical(t *testing.T) {
	matrix := []WorkloadCase{{Name: "a", DecodeSteps: 10}, {Name: "b", ContextDepth: 1024}}
	first, err := WorkloadDigest(matrix)
	if err != nil {
		t.Fatal(err)
	}
	second, err := WorkloadDigest(matrix)
	if err != nil || first != second {
		t.Fatalf("digest is not stable: %s %s %v", first, second, err)
	}
	changed, err := WorkloadDigest([]WorkloadCase{{Name: "a", DecodeSteps: 11}, {Name: "b", ContextDepth: 1024}})
	if err != nil || changed == first {
		t.Fatalf("digest must change with the matrix: %s %v", changed, err)
	}
}
