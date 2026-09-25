package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/sandbox"
)

func newSandboxCmd(g *globalFlags) *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manual Agent Sandbox operations (claims attach by run ID)",
	}
	cmd.PersistentFlags().StringVar(&namespace, "namespace", "default", "namespace holding the SandboxClaims")
	cmd.AddCommand(
		sandboxAcquireCmd(g, &namespace),
		sandboxRunCmd(g, &namespace),
		sandboxPullCmd(g, &namespace),
		sandboxReleaseCmd(g, &namespace),
	)
	return cmd
}

func sandboxClient(g *globalFlags, namespace string) *sandbox.Client {
	return &sandbox.Client{Namespace: namespace, Kubeconfig: g.kubeconfig}
}

func sandboxAcquireCmd(g *globalFlags, namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "acquire <run-id> <warm-pool>",
		Short: "Create (or attach to) the deterministic claim of a run",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := sandboxClient(g, *namespace)
			if err := c.EnsureSandboxClaim(context.Background(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "claim %s ready\n", sandbox.ClaimName(args[0]))
			return nil
		},
	}
}

func sandboxRunCmd(g *globalFlags, namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "run <claim> '<command>'",
		Short: "Run a shell command on the claim (no automatic retry)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := sandboxClient(g, *namespace)
			out, err := c.ExecClaim(context.Background(), args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func sandboxPullCmd(g *globalFlags, namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "pull <claim> <remote-path> <local-path>",
		Short: "Copy a file out of the claim",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := sandboxClient(g, *namespace)
			b, err := c.PullClaim(context.Background(), args[0], args[1])
			if err != nil {
				return err
			}
			return os.WriteFile(args[2], b, 0o644)
		},
	}
}

func sandboxReleaseCmd(g *globalFlags, namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "release <run-id>",
		Short: "Delete the claim of a run (save artifacts first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return errors.New("release: run id required")
			}
			c := sandboxClient(g, *namespace)
			if err := c.ReleaseSandboxClaim(context.Background(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "claim %s released\n", sandbox.ClaimName(args[0]))
			return nil
		},
	}
}
