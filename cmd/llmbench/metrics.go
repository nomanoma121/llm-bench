package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/measurement"
)

func newMetricsCmd(g *globalFlags) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "metrics <run-id>",
		Short: "Show the sealed measurement evidence of a run",
		Long: "Print the evidence recorded for a run. The harness seals it during execution,\n" +
			"so the values are exactly what the promotion decision reads. The recorded\n" +
			"digest is re-checked before printing: a tampered file is an error.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := newFileStore(g)
			if err != nil {
				return err
			}
			r, err := store.LoadRun(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if r.MetricsDigest == "" {
				return fmt.Errorf("metrics: run %s has no sealed evidence", args[0])
			}
			path := filepath.Join(store.ArtifactsDir(r.ID), measurement.EvidenceDir, measurement.EvidenceFileName)
			evidence, err := measurement.Verify(path, r.MetricsDigest)
			if err != nil {
				return fmt.Errorf("metrics: %w", err)
			}
			if raw {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			b, err := json.MarshalIndent(evidence, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "print the sealed bytes instead of re-indenting them")
	return cmd
}
