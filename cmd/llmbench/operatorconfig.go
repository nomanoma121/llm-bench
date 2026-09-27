package main

import (
	"github.com/nomanoma121/llm-bench/internal/operator"
)

// loadOperatorMVP reads the MVP operator configuration. The path is required:
// the namespace and the model allowlist live there, and guessing them would
// make the CLI act on a different cluster than the controller.
func loadOperatorMVP(path string) (operator.MVP, error) {
	if path == "" {
		return operator.MVP{}, errNoOperatorConfig
	}
	return operator.LoadMVP(path)
}

// errNoOperatorConfig marks a missing --operator-config, which is a usage
// error rather than an infrastructure failure.
var errNoOperatorConfig = errMissingOperatorConfig{}

type errMissingOperatorConfig struct{}

func (errMissingOperatorConfig) Error() string {
	return "no operator configuration: pass --operator-config <file> or set LLMBENCH_SANDBOX_NAMESPACE"
}
