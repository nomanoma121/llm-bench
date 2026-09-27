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

	"github.com/nomanoma121/llm-bench/internal/job"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps a command error to the CLI contract (docs/mvp.md §5): 0 on
// success, 2 for invalid input, 1 for everything else until the run-related
// codes (3, 10, 11) are implemented.
func exitCode(err error) int {
	if errors.Is(err, job.ErrInvalid) {
		return 2
	}
	return 1
}
