package main

import (
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
	"github.com/nomanoma121/llm-bench/internal/job"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		switch {
		case errors.Is(err, job.ErrInvalid):
			os.Exit(2)
		case errors.Is(err, benchmark.ErrNoResult):
			os.Exit(10)
		}
		os.Exit(1)
	}
}

func restConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		if cfg, err := rest.InClusterConfig(); err == nil {
			return cfg, nil
		}
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, nil).ClientConfig()
}
