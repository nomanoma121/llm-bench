package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	var kubeconfig string
	cmd := &cobra.Command{
		Use:           "llmbench",
		Short:         "Measure and optimize LLM inference runtimes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig (default: in-cluster, then ~/.kube/config)")
	cmd.AddCommand(
		newControllerCmd(&kubeconfig),
		newSandboxCmd(&kubeconfig),
		newBenchmarkCmd(),
		newCompareCmd(),
		newJobCmd(),
		newModelsCmd(),
		newSiteCmd(),
	)
	return cmd
}
