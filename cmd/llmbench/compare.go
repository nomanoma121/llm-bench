package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/compare"
)

func newCompareCmd() *cobra.Command {
	var (
		kind   string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "compare <baseline-dir> <candidate-dir>",
		Short: "Report the factual difference between two measured results",
		Long: "Both directories are verified first (result.json and its sidecar digests),\n" +
			"then the metrics are paired by identity and compared. The output is facts\n" +
			"only: metric deltas, whether the two runs are comparable at all, and the\n" +
			"reasons when they are not. It never says which side is better — that\n" +
			"decision belongs to the Agent (docs/mvp.md §5).\n\n" +
			"--kind selects which difference is expected: model comparison allows the\n" +
			"model to differ, runtime comparison allows the runtime build to differ.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := compare.EnsureDir(args[0]); err != nil {
				return err
			}
			if err := compare.EnsureDir(args[1]); err != nil {
				return err
			}
			got, err := compare.Compare(args[0], args[1], compare.Kind(kind))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(got)
			}
			fmt.Fprintf(out, "baseline:  %s %s\n", got.Baseline.JobID, shortDigest(got.Baseline.ResultDigest))
			fmt.Fprintf(out, "candidate: %s %s\n", got.Candidate.JobID, shortDigest(got.Candidate.ResultDigest))
			fmt.Fprintf(out, "measurement_valid: %t\n", got.MeasurementValid)
			fmt.Fprintf(out, "comparable: %t\n", got.Comparable)
			for _, reason := range got.Reasons {
				fmt.Fprintf(out, "  not comparable: %s\n", reason)
			}
			if len(got.Metrics) > 0 {
				fmt.Fprintf(out, "\n%-14s %-26s %-10s %12s %12s %10s\n", "case", "metric", "unit", "baseline", "candidate", "change")
				for _, d := range got.Metrics {
					// A missing side and an undefined relative change are
					// printed as "-" rather than as a zero the reader would
					// take for a measurement.
					change := string(d.Status)
					if d.Status == compare.StatusCompared && d.AbsChange != nil {
						if d.RelChange != nil {
							change = fmt.Sprintf("%+.4g (%+.1f%%)", *d.AbsChange, *d.RelChange*100)
						} else {
							change = fmt.Sprintf("%+.4g (n/a)", *d.AbsChange)
						}
					}
					fmt.Fprintf(out, "%-14s %-26s %-10s %12s %12s %10s\n",
						d.Case, d.Name, d.Unit, formatValue(d.Before), formatValue(d.After), change)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", string(compare.KindRuntime), "model or runtime: which difference the comparison expects")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the comparison as JSON")
	return cmd
}

// formatValue renders a metric value, or "-" when that side has no value.
func formatValue(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.6g", *v)
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
