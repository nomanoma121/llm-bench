// Command llmbench is the controller CLI of llm-bench. It validates and
// submits experiments, drives them through the state machine and reports
// their status. Long-running operation (serve) and cluster integrations are
// added in later milestones; see docs/architecture.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
