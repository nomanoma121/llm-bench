package main

import (
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

// modelAliases are the model-selection flags upstream exposes for each engine.
// They are the flags the review of PR #22 found missing from the reserved
// lists, so the test checks them by name rather than only spot-checking --host.
var modelAliases = map[string][]string{
	"llamacpp":  {"-m", "--model", "-mu", "--model-url", "-dr", "--docker-repo", "-hf", "-hfr", "--hf-repo", "-hff", "--hf-file", "--lora", "--mmproj"},
	"freetoken": {"--model", "--model-path", "--model-source", "--dummy-weight", "--gpu"},
}

// TestRuntimeReservedArgsFeedJobValidation pins the wiring between the runtime
// adapters and job validation: the adapter owns the flags that decide what is
// measured, and the CLI and controller pass them to the validator as
// Constraints.ReservedArgs. Without this the privilege separation documented
// in docs/mvp.md §3.4 would only exist in prose.
func TestRuntimeReservedArgsFeedJobValidation(t *testing.T) {
	for _, engine := range []string{"llamacpp", "freetoken"} {
		reserved, err := runtime.ReservedArgs(engine)
		if err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
		if len(reserved) == 0 {
			t.Fatalf("%s has no reserved flags", engine)
		}
		spec := job.Spec{
			Kind:    job.KindBenchmark,
			Model:   job.Model{ID: "m"},
			Runtime: job.Runtime{Engine: engine, Image: "img", Ready: job.Ready{Port: 8080}, Args: []string{"--host", "0.0.0.0"}},
			Workload: job.Workload{Cases: []job.Case{
				{Name: "c", PromptText: "hi", MaxTokens: 8},
			}},
			Metrics: job.Metrics{Collectors: []job.Collector{job.CollectorHarness}},
			Output:  job.Output{Dir: "experiments/m"},
		}
		err = spec.ValidateConstraints(job.Constraints{ReservedArgs: reserved})
		if err == nil {
			t.Fatalf("%s: a spec setting --host was accepted", engine)
		}
		if !strings.Contains(err.Error(), "--host") {
			t.Fatalf("%s: error %q does not name the offending flag", engine, err)
		}
		// Tuning flags still pass.
		spec.Runtime.Args = []string{"-ngl", "99"}
		if err := spec.ValidateConstraints(job.Constraints{ReservedArgs: reserved}); err != nil {
			t.Fatalf("%s: tuning args were rejected: %v", engine, err)
		}
		// Every way of pointing the runtime at other weights must be closed.
		for _, alias := range modelAliases[engine] {
			spec.Runtime.Args = []string{alias, "/somewhere/else"}
			if err := spec.ValidateConstraints(job.Constraints{ReservedArgs: reserved}); err == nil {
				t.Errorf("%s: %s was accepted", engine, alias)
			}
		}
		spec.Runtime.Args = nil
	}
}
