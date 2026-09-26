package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/adopt"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/review"
	"github.com/nomanoma121/llm-bench/internal/sitebuild"
)

func newAdoptCmd(g *globalFlags) *cobra.Command {
	var into, reviewID, configPath string
	var write bool
	var coordNamespace string
	cmd := &cobra.Command{
		Use:   "adopt <run-id>",
		Short: "Materialize an accepted run artifact into experiments/<model>/<experiment>",
		Long: "Copy a sealed, human-accepted artifact into the repository together with a\n" +
			"manifest recording its digests and the Issue decision that authorized it.\n" +
			"Publishing happens later, when the change is merged to main. The command\n" +
			"never decides anything: without --write it only reports what it would do.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if into == "" {
				return errors.New("adopt: --into experiments/<model-id>/<experiment-id> is required")
			}
			runs, artifactsDir, err := runReader(g, coordNamespace)
			if err != nil {
				return err
			}
			opts := adopt.Options{
				Root:         g.root,
				RunID:        args[0],
				Into:         into,
				ReviewID:     reviewID,
				Write:        write,
				Store:        runs,
				ArtifactsDir: artifactsDir,
			}
			if reviewID != "" {
				if configPath == "" {
					return errors.New("adopt: --review requires --config so the Issue can be read")
				}
				opCfg, err := operator.Load(configPath)
				if err != nil {
					return err
				}
				svc, err := buildReviewService(g, opCfg)
				if err != nil {
					return err
				}
				opts.Decision = func(ctx context.Context, id string) (review.Decision, error) {
					return svc.Decision(ctx, id)
				}
			}
			res, err := adopt.Run(cmd.Context(), opts)
			if err != nil {
				return err
			}
			switch {
			case res.NoOp:
				fmt.Fprintf(cmd.OutOrStdout(), "%s already holds run %s with the same artifact (no change)\n", into, res.Manifest.RunID)
			case res.ProvenanceUpdated:
				fmt.Fprintf(cmd.OutOrStdout(), "%s holds identical bytes; provenance updated to run %s\n", into, res.Manifest.RunID)
			case !write:
				out, err := json.MarshalIndent(res.Manifest, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "dry run: would write %s/%s\n%s\n", into, filepath.Join("output", adopt.ManifestName), out)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "adopted run %s into %s (artifact_digest=%s)\n", res.Manifest.RunID, into, res.Manifest.ArtifactDigest)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&into, "into", "", "destination: experiments/<model-id>/<experiment-id> (required)")
	cmd.Flags().StringVar(&reviewID, "review", "", "review ID that authorized this artifact (required unless the model has no adoption yet)")
	cmd.Flags().StringVar(&configPath, "config", "", "operator configuration, needed with --review to read the Issue")
	cmd.Flags().BoolVar(&write, "write", false, "write the files (default is a dry run)")
	cmd.Flags().StringVar(&coordNamespace, "coordination-namespace", "", "kubernetes namespace holding run records (defaults to the local file store)")
	return cmd
}

func newAdoptedCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adopted",
		Short: "Verification of adopted artifacts (used by CI)",
	}
	cmd.AddCommand(newAdoptedVerifyCmd())
	return cmd
}

func newAdoptedVerifyCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify every adopted manifest, payload digest and inventory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := adopt.Verify(root); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "adopted artifacts under %s verified\n", root)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "experiments", "experiments tree to verify")
	return cmd
}

func newSiteCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Static publication site operations (used by CI)",
	}
	cmd.AddCommand(newSiteBuildCmd())
	return cmd
}

func newSiteBuildCmd() *cobra.Command {
	var root, out string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Generate the static site from adopted artifacts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := sitebuild.Build(sitebuild.BuildOptions{ExperimentsRoot: root, Out: out}); err != nil {
				return err
			}
			if err := sitebuild.VerifyOutput(out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "site built from %s into %s\n", root, out)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "experiments", "experiments tree to publish")
	cmd.Flags().StringVar(&out, "out", "_site", "output directory")
	return cmd
}
