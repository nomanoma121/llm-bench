package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/run"
)

func newSubmitCmd(g *globalFlags) *cobra.Command {
	var configPath, commit string
	cmd := &cobra.Command{
		Use:   "submit <experiment.yaml>",
		Short: "Submit an experiment and drive it to completion (explicit run request)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSubmit(cmd, g, args[0], configPath, commit)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "operator configuration (defaults to a local-only development config)")
	cmd.Flags().StringVar(&commit, "commit", "", "full input commit SHA (required for Sandbox targets)")
	return cmd
}

func runSubmit(cmd *cobra.Command, g *globalFlags, expPath, configPath, commit string) error {
	ctx := context.Background()
	opCfg, err := loadOperatorConfig(configPath)
	if err != nil {
		return err
	}
	r, inputs, err := prepareRun(g, opCfg, expPath, commit)
	if err != nil {
		return err
	}
	engine := buildEngine(g, opCfg, time.Second, newSandboxClients(g), newGitopsGateways(g))
	submitted, err := engine.Submit(ctx, r, inputs)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "submitted run %s (target=%s)\n", submitted.ID, submitted.Target)

	store, err := newFileStore(g)
	if err != nil {
		return err
	}
	if err := engine.Drain(ctx, submitted.ID); err != nil {
		return err
	}
	final, err := store.LoadRun(ctx, submitted.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "run %s finished: phase=%s result=%s\n", final.ID, final.Phase, final.ExecutionResult)
	if final.PublicURL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "published: %s\n", final.PublicURL)
	}
	if final.Phase != run.PhaseSucceeded {
		return fmt.Errorf("run %s did not succeed (phase=%s wait_reason=%q)", final.ID, final.Phase, final.WaitReason)
	}
	return nil
}
