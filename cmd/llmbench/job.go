package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/job"
)

func newJobCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Validate and render MVP job specs (the request carried by an Issue)",
	}
	cmd.AddCommand(newJobValidateCmd(), newJobInitCmd(), newJobDoneCmd(g))
	return cmd
}

func newJobValidateCmd() *cobra.Command {
	var (
		issue  bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "validate <file|->",
		Short: "Validate a job spec, or an Issue body with --issue",
		Long: "Parse and validate a job spec with the same code the controller runs.\n" +
			"With --issue, the input is a GitHub Issue body and the first ```yaml\n" +
			"block is validated. Exit code 2 means the spec is invalid.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := jobInput(args[0])
			if err != nil {
				return err
			}
			var spec job.Spec
			if issue {
				spec, err = job.FromIssueBody(string(data))
			} else {
				spec, err = job.Parse(bytes.NewReader(data))
			}
			if err != nil {
				return err
			}
			if err := spec.Validate(); err != nil {
				return err
			}
			digest, err := spec.Digest()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"valid":      true,
					"kind":       spec.Kind,
					"model":      spec.Model.ID,
					"engine":     spec.Runtime.Engine,
					"output_dir": spec.Output.Dir,
					"digest":     digest,
				})
			}
			fmt.Fprintf(out, "OK kind=%s model=%s engine=%s output=%s digest=%s\n",
				spec.Kind, spec.Model.ID, spec.Runtime.Engine, spec.Output.Dir, digest)
			return nil
		},
	}
	cmd.Flags().BoolVar(&issue, "issue", false, "treat the input as a GitHub Issue body and validate its first ```yaml block")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print a machine-readable summary")
	return cmd
}

func newJobInitCmd() *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Print a job spec template for an Issue",
		Long: "Print the same template the Issue forms pre-fill, so a spec can be\n" +
			"prepared and validated locally before opening the Issue.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := job.Template(job.Kind(kind))
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), text)
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", string(job.KindBenchmark), "benchmark or optimize")
	return cmd
}

// jobInput reads a job spec from a path, or from stdin for "-".
func jobInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
