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
	cmd.AddCommand(newSandboxJobCmd(g))
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
			ready, err := c.EnsureSandboxClaim(context.Background(), sandbox.ClaimName(args[0]), args[1])
			if err != nil {
				return err
			}
			if !ready {
				return fmt.Errorf("claim %s exists but is not ready yet", sandbox.ClaimName(args[0]))
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
			// Manual operations take an explicit shell command from the
			// operator; the controller's automated paths never build one.
			stdout, stderr, code, err := c.Exec(context.Background(), args[0], []string{"/bin/sh", "-c", args[1]}, nil, "")
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), string(stdout))
			fmt.Fprint(cmd.ErrOrStderr(), string(stderr))
			if code != 0 {
				return fmt.Errorf("sandbox run: exit %d", code)
			}
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
			b, err := c.Pull(context.Background(), args[0], args[1])
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
			released, err := c.ReleaseSandboxClaim(context.Background(), sandbox.ClaimName(args[0]))
			if err != nil {
				return err
			}
			if !released {
				return fmt.Errorf("claim %s is still terminating", sandbox.ClaimName(args[0]))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "claim %s released\n", sandbox.ClaimName(args[0]))
			return nil
		},
	}
}
