// Command llmbench is the controller CLI of llm-bench. It validates and
// submits experiments, drives them through the state machine and reports
// their status. Long-running operation (serve) and cluster integrations are
// added in later milestones; see docs/architecture.md.
//
// The MVP paths (docs/mvp.md) are `llmbench job`, `llmbench benchmark`,
// `llmbench compare` and `llmbench sandbox`. Exit codes follow the CLI
// contract in docs/mvp.md §5.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
	"github.com/nomanoma121/llm-bench/internal/job"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps a command error to the CLI contract (docs/mvp.md §5): 0 on
// success, 2 for invalid input, 10 when a run produced no result, 11 on a
// timeout, and 1 for everything else (3, the lease conflict, is added with the
// controller).
func exitCode(err error) int {
	switch {
	case errors.Is(err, job.ErrInvalid):
		return 2
	case errors.Is(err, benchmark.ErrTimeout):
		return 11
	case errors.Is(err, benchmark.ErrNoResult), errors.Is(err, benchmark.ErrAborted):
		return 10
	default:
		return 1
	}
}
