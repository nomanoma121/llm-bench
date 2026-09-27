package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/site"
)

func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "job", Short: "Write and check job specs"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "init benchmark|optimize",
			Short: "Print a job spec template",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				kind := job.Kind(args[0])
				if kind != job.Benchmark && kind != job.Optimize {
					return fmt.Errorf("%w: kind must be benchmark or optimize", job.ErrInvalid)
				}
				fmt.Fprint(cmd.OutOrStdout(), job.Template(kind))
				return nil
			},
		},
		&cobra.Command{
			Use:   "validate <spec.yaml>",
			Short: "Check a job spec",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if _, err := job.Load(args[0]); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "ok")
				return nil
			},
		},
	)
	return cmd
}

func newSiteCmd() *cobra.Command {
	var root, out string
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Build the static results site from experiments/",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return site.Build(root, out)
		},
	}
	cmd.Flags().StringVar(&root, "root", "experiments", "experiments directory")
	cmd.Flags().StringVar(&out, "out", "_site", "output directory")
	return cmd
}
