package main

import (
	"os"

	"github.com/spf13/cobra"
)

func osExit(code int) { os.Exit(code) }

// globalFlags are the persistent settings shared by all commands.
type globalFlags struct {
	root       string // repository root (experiments/, models/, benchmarks/)
	state      string // controller state directory (run records, leases)
	output     string // artifact root; artifacts live under <output>/runs/<run-id>
	kubeconfig string // optional kubeconfig for cluster integrations
}

func newRootCmd() *cobra.Command {
	var g globalFlags
	cmd := &cobra.Command{
		Use:           "llmbench",
		Short:         "Controller and CLI for the llm-bench visual benchmark",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&g.root, "root", ".", "repository root")
	cmd.PersistentFlags().StringVar(&g.state, "state", ".state", "controller state directory")
	cmd.PersistentFlags().StringVar(&g.output, "output", "runs", "artifact output root")
	cmd.PersistentFlags().StringVar(&g.kubeconfig, "kubeconfig", "", "kubeconfig path for cluster integrations (defaults to in-cluster, then ~/.kube/config)")
	cmd.AddCommand(
		newValidateCmd(&g),
		newSubmitCmd(&g),
		newStatusCmd(&g),
		newServeCmd(&g),
		newSandboxCmd(&g),
		newReviewCmd(&g),
	)
	return cmd
}
