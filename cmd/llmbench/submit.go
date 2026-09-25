package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// benchSchemaVersion participates in the benchmark fingerprint. Bump it when
// the benchmark contract changes in a way that breaks A/B comparability.
const benchSchemaVersion = "1"

// controllerVersion is stamped into fingerprints and run records.
const controllerVersion = "0.1.0-dev"

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

	cfg, err := experiment.Load(expPath)
	if err != nil {
		return err
	}
	if err := experiment.Validate(cfg, g.root); err != nil {
		return fmt.Errorf("%s: %w", expPath, err)
	}
	opCfg, err := loadOperatorConfig(configPath)
	if err != nil {
		return err
	}
	ready := 0
	if cfg.Runtime.Start != nil {
		ready = cfg.Runtime.Start.ReadyTimeout()
	}
	if err := opCfg.ValidateRecipe(cfg.Target, ready); err != nil {
		return err
	}
	target, ok := opCfg.Targets[cfg.Target]
	if !ok {
		return fmt.Errorf("submit: target %q is not allowlisted", cfg.Target)
	}
	if target.Sandbox != nil && len(commit) != 40 {
		return fmt.Errorf("submit: sandbox target %q requires --commit with a full 40-hex SHA", cfg.Target)
	}

	promptBytes, err := os.ReadFile(experiment.BenchmarkPath(cfg, g.root))
	if err != nil {
		return err
	}
	recipeJSON, err := experiment.CanonicalJSON(cfg)
	if err != nil {
		return err
	}
	rawBytes, err := os.ReadFile(expPath)
	if err != nil {
		return err
	}

	runID, err := newRunID()
	if err != nil {
		return err
	}
	plan := operator.BuildHookPlan(target, runID)
	digest, err := operator.PlanDigest(plan)
	if err != nil {
		return err
	}

	store, err := newFileStore(g)
	if err != nil {
		return err
	}
	now := time.Now()
	fingerprint, err := provenance.Fingerprint(provenance.FingerprintInput{
		Prompt:                 promptBytes,
		BenchmarkSchemaVersion: benchSchemaVersion,
		ContextSize:            cfg.Runtime.ContextSize,
		RuntimeSignature:       cfg.Runtime.Engine + "/" + cfg.Runtime.Variant,
		TargetKind:             targetKind(target),
		ControllerVersion:      controllerVersion,
	})
	if err != nil {
		return err
	}
	r := run.Run{
		ID:                  runID,
		Target:              cfg.Target,
		Experiment:          filepath.ToSlash(expPath),
		InputCommit:         commit,
		Fingerprint:         fingerprint,
		RecipeSchemaVersion: 1,
		RecipeJSON:          string(recipeJSON),
		PromptSHA256:        provenance.SHA256Hex(promptBytes),
		HookPlan:            plan,
		HookPlanDigest:      digest,
		Artifacts:           run.Artifacts{Dir: store.ArtifactsDir(runID)},
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	engine := buildEngine(g, opCfg)
	submitted, err := engine.Submit(ctx, r, map[string][]byte{
		"config.yaml": rawBytes,
		"prompt.md":   promptBytes,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "submitted run %s (target=%s)\n", submitted.ID, submitted.Target)

	if err := engine.Drain(ctx, runID); err != nil {
		return err
	}
	final, err := store.LoadRun(ctx, runID)
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

func targetKind(t operator.Target) string {
	if t.Sandbox != nil {
		return "sandbox"
	}
	return "local"
}

func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("submit: generate run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
