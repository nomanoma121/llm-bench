package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/sandbox"
)

func newSandboxCmd(kubeconfig *string) *cobra.Command {
	namespace := os.Getenv("LLMBENCH_SANDBOX_NAMESPACE")
	if namespace == "" {
		namespace = "llmbench"
	}
	client := func() (*sandbox.Client, error) {
		rest, err := restConfig(*kubeconfig)
		if err != nil {
			return nil, err
		}
		return &sandbox.Client{Namespace: namespace, REST: rest}, nil
	}
	cmd := &cobra.Command{Use: "sandbox", Short: "Work inside a job's GPU sandbox"}
	cmd.PersistentFlags().StringVar(&namespace, "namespace", namespace, "sandbox namespace ($LLMBENCH_SANDBOX_NAMESPACE)")
	cmd.AddCommand(
		&cobra.Command{
			Use:   "exec <job-id> -- <argv...>",
			Short: "Run a command in the sandbox",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := client()
				if err != nil {
					return err
				}
				out, err := c.Exec(cmd.Context(), args[0], args[1:], nil)
				if err != nil {
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), out.Stdout)
				fmt.Fprint(cmd.ErrOrStderr(), out.Stderr)
				if out.ExitCode != 0 {
					return fmt.Errorf("exit status %d", out.ExitCode)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "put <job-id> <local-file> <sandbox-path>",
			Short: "Copy a file into the sandbox",
			Args:  cobra.ExactArgs(3),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := client()
				if err != nil {
					return err
				}
				f, err := os.Open(args[1])
				if err != nil {
					return err
				}
				defer f.Close()
				return c.Put(cmd.Context(), args[0], args[2], f)
			},
		},
		&cobra.Command{
			Use:   "get <job-id> <sandbox-path>",
			Short: "Print a file from the sandbox",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := client()
				if err != nil {
					return err
				}
				b, err := c.Get(cmd.Context(), args[0], args[1])
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(b)
				return err
			},
		},
	)
	return cmd
}
