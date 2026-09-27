package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

func newBenchmarkCmd(g *globalFlags) *cobra.Command {
	var (
		jobPath        string
		jobID          string
		modelPath      string
		outputDir      string
		asJSON         bool
		requestTimeout time.Duration
		push           bool
		gitToken       string
		branch         string
		remote         string
	)
	cmd := &cobra.Command{
		Use:   "benchmark --job <file|-> --model-path <path>",
		Short: "Measure one job inside the GPU sandbox",
		Long: "Start the runtime the job names, measure every workload case, collect the\n" +
			"configured metrics and write the result directory\n" +
			"(docs/mvp.md §4). The job spec is validated with the same code the\n" +
			"controller runs, and the runtime's reserved flags are rejected here too.\n\n" +
			"This command is meant to run inside the sandbox, where the runtime image and\n" +
			"the GPU are. Exit codes: 0 measured (even when the measurement is invalid),\n" +
			"2 invalid input, 10 the run produced no result, 11 timeout.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := jobInput(jobPath)
			if err != nil {
				return err
			}
			spec, err := job.Parse(bytes.NewReader(data))
			if err != nil {
				return err
			}
			reserved, err := runtime.ReservedArgs(spec.Runtime.Engine)
			if err != nil {
				return err
			}
			if err := spec.ValidateConstraints(job.Constraints{ReservedArgs: reserved}); err != nil {
				return err
			}
			if modelPath == "" {
				return fmt.Errorf("%w: --model-path is required: the model path is resolved by the operator, not by the job spec", job.ErrInvalid)
			}
			adapter, err := runtime.New(spec.Runtime.Engine, runtime.Options{
				ModelID:   spec.Model.ID,
				ModelPath: modelPath,
				Host:      "127.0.0.1",
				Port:      spec.Runtime.Ready.Port,
				Args:      spec.Runtime.Args,
			})
			if err != nil {
				return err
			}
			if jobID == "" {
				jobID = defaultJobID(spec, time.Now())
			}
			if outputDir == "" {
				outputDir = filepath.Join(g.root, spec.Output.Dir, jobID)
			}
			out := cmd.OutOrStdout()
			progress := func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
			}
			outcome, err := benchmark.Run(cmd.Context(), benchmark.Config{
				Spec:           spec,
				JobID:          jobID,
				RepoRoot:       g.root,
				OutputDir:      outputDir,
				ModelPath:      modelPath,
				RequestTimeout: requestTimeout,
				Logf:           progress,
			}, adapter, benchmark.DefaultSeams())
			if err != nil {
				return err
			}
			result := outcome.Result
			var pushed benchmark.PushCommits
			if push {
				rel, err := filepath.Rel(g.root, outcome.Dir)
				if err != nil {
					return err
				}
				if branch == "" {
					branch = "llmbench/" + jobID
				}
				pushed, err = benchmark.Push(cmd.Context(), benchmark.PushOptions{
					RepoRoot: g.root,
					Paths:    []string{rel},
					Branch:   branch,
					Message:  commitMessage(spec, result),
					Token:    gitToken,
					Remote:   remote,
					Logf:     progress,
				})
				if err != nil {
					return err
				}
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"job_id":            result.JobID,
					"output_dir":        outcome.Dir,
					"result_digest":     result.ResultDigest,
					"series_digest":     result.SeriesDigest,
					"measurement_valid": result.MeasurementValid,
					"invalid_reasons":   result.InvalidReasons,
					"branch":            pushed.Branch,
					"commit":            pushed.Commit,
				})
			}
			fmt.Fprintf(out, "%s: valid=%t result=%s\n", outcome.Dir, result.MeasurementValid, result.ResultDigest)
			for _, r := range result.InvalidReasons {
				fmt.Fprintf(out, "  invalid: %s\n", r)
			}
			if pushed.Branch != "" {
				fmt.Fprintf(out, "  pushed %s (%s)\n", pushed.Branch, pushed.Commit)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&jobPath, "job", "-", "job spec file, or - for stdin")
	cmd.Flags().StringVar(&jobID, "job-id", "", "job id (default: date and spec digest)")
	cmd.Flags().StringVar(&modelPath, "model-path", "", "resolved model path or repository id (operator-supplied)")
	cmd.Flags().StringVar(&outputDir, "out", "", "output directory (default: <root>/<output.dir>/<job-id>)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print a machine-readable summary")
	cmd.Flags().DurationVar(&requestTimeout, "request-timeout", 0, "timeout for one measured request (default 10m)")
	cmd.Flags().BoolVar(&push, "push", false, "commit the result and push a branch")
	cmd.Flags().StringVar(&gitToken, "git-token", os.Getenv("LLMBENCH_GIT_TOKEN"), "GitHub App installation token for --push (default $LLMBENCH_GIT_TOKEN)")
	cmd.Flags().StringVar(&branch, "branch", "", "branch to push (default llmbench/<job-id>)")
	cmd.Flags().StringVar(&remote, "remote", "origin", "git remote to push to")
	return cmd
}

// defaultJobID is used when the controller does not name the job: the date and
// a short spec digest, which is enough to tell two runs apart.
func defaultJobID(spec job.Spec, now time.Time) string {
	digest, err := spec.Digest()
	if err != nil || len(digest) < 8 {
		return now.UTC().Format("2006-01-02")
	}
	return now.UTC().Format("2006-01-02") + "-" + digest[:8]
}

// commitMessage names the request and the result so the commit is traceable
// from the PR back to the job that produced it.
func commitMessage(spec job.Spec, result measurement.Result) string {
	return fmt.Sprintf("benchmark: %s on %s\n\njob_id: %s\njobspec_digest: %s\nresult_digest: %s\nmeasurement_valid: %t\n",
		spec.Model.ID, spec.Runtime.Engine, result.JobID, result.JobSpecDigest, result.ResultDigest, result.MeasurementValid)
}
