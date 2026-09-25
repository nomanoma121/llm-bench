package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/operator"
)

func newValidateCmd(g *globalFlags) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "validate <experiment.yaml>",
		Short: "Validate an experiment recipe against the repository and operator configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			cfg, err := experiment.Load(path)
			if err != nil {
				return err
			}
			if err := experiment.Validate(cfg, g.root); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if configPath != "" {
				op, err := operator.Load(configPath)
				if err != nil {
					return err
				}
				ready := 0
				if cfg.Runtime.Start != nil {
					ready = cfg.Runtime.Start.ReadyTimeout()
				}
				if err := op.ValidateRecipe(cfg.Target, ready); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "OK %s (model=%s target=%s)\n", path, cfg.Model, cfg.Target)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "operator configuration for target allowlisting and limits")
	return cmd
}

// loadOperatorConfig loads the operator configuration, falling back to a
// permissive local-only configuration for development without one.
func loadOperatorConfig(path string) (operator.Config, error) {
	if path == "" {
		return defaultOperatorConfig(), nil
	}
	return operator.Load(path)
}

func defaultOperatorConfig() operator.Config {
	return operator.Config{
		Targets: map[string]operator.Target{
			"local": {Hooks: []operator.CommandHook{}},
		},
	}
}
