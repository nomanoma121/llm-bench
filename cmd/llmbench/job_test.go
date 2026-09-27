package main

import (
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

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
	}
}
