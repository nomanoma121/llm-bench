package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
)

func newCompareCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "compare <baseline-dir> <candidate-dir>",
		Short: "Report how a candidate result differs from a baseline",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := benchmark.Compare(args[0], args[1])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(c)
			}
			fmt.Fprintf(out, "measurement_valid=%t comparable=%t\n", c.MeasurementValid, c.Comparable)
			for _, r := range c.Reasons {
				fmt.Fprintf(out, "  %s\n", r)
			}
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "case\tmetric\tbaseline\tcandidate\tchange")
			for _, d := range c.Deltas {
				change := fmt.Sprintf("%+.2f %s", d.Change, d.Unit)
				if d.ChangePercent != nil {
					change = fmt.Sprintf("%+.1f%%", *d.ChangePercent)
				}
				fmt.Fprintf(w, "%s\t%s\t%.2f\t%.2f\t%s\n", d.Case, d.Name, d.Baseline, d.Candidate, change)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}
