package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
	"github.com/nomanoma121/llm-bench/internal/job"
)

func newBenchmarkCmd() *cobra.Command {
	var jobPath, jobID, root, out, bin string
	var model benchmark.Model
	var push bool
	cmd := &cobra.Command{
		Use:   "benchmark --job <spec.yaml> --model-path <path>",
		Short: "Start the runtime, run the workload and write the result directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := job.Load(jobPath)
			if err != nil {
				return err
			}
			if jobID == "" {
				jobID = "local-" + time.Now().UTC().Format("20060102-150405")
			}
			if out == "" {
				out = filepath.Join(root, "experiments", spec.Model, jobID)
			}
			result, err := benchmark.Run(cmd.Context(), benchmark.Config{
				Spec: spec, JobID: jobID, Root: root, OutDir: out, Model: model, Binary: bin,
				Logf: func(format string, args ...any) { fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...) },
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: measurement_valid=%t\n", out, result.MeasurementValid)
			for _, reason := range result.InvalidReasons {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", reason)
			}
			if !push {
				return nil
			}
			rel, err := filepath.Rel(root, out)
			if err != nil {
				return err
			}
			commit, err := benchmark.Push(cmd.Context(), root, "llmbench/"+jobID,
				fmt.Sprintf("%s: %s on %s (%s)", spec.Kind, spec.Model, spec.Runtime.Engine, jobID), rel)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pushed llmbench/%s at %s\n", jobID, commit)
			return nil
		},
	}
	cmd.Flags().StringVar(&jobPath, "job", "", "job spec file")
	cmd.Flags().StringVar(&jobID, "job-id", "", "job id (default: local-<timestamp>)")
	cmd.Flags().StringVar(&root, "root", ".", "llm-bench checkout")
	cmd.Flags().StringVar(&out, "out", "", "result directory (default: <root>/experiments/<model>/<job-id>)")
	cmd.Flags().StringVar(&bin, "bin", "", "runtime server binary (default: the engine's binary on PATH)")
	cmd.Flags().StringVar(&model.Path, "model-path", "", "model weights")
	cmd.Flags().StringVar(&model.Digest, "model-digest", "", "digest of the model weights")
	cmd.Flags().BoolVar(&push, "push", false, "commit the result and push it to llmbench/<job-id>")
	_ = cmd.MarkFlagRequired("job")
	_ = cmd.MarkFlagRequired("model-path")
	return cmd
}
