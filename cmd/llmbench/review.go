package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/discord"
	"github.com/nomanoma121/llm-bench/internal/experiment"
	"github.com/nomanoma121/llm-bench/internal/filestore"
	"github.com/nomanoma121/llm-bench/internal/issues"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/review"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// reviewStore adapts the file store's JSON blobs to review.Store.
type reviewStore struct{ s *filestore.Store }

func (a reviewStore) SaveReview(ctx context.Context, r review.Review) error {
	return a.s.SaveJSON(ctx, "reviews", r.ID, r)
}

func (a reviewStore) LoadReview(ctx context.Context, id string) (review.Review, error) {
	var r review.Review
	if err := a.s.LoadJSON(ctx, "reviews", id, &r); err != nil {
		return review.Review{}, err
	}
	return r, nil
}

// buildReviewService builds the review service. It requires the operator
// review block, the GitHub token in the environment and, optionally, a
// Discord webhook (the env named by discord_webhook_env).
func buildReviewService(g *globalFlags, opCfg operator.Config) (*review.Service, error) {
	if opCfg.Review == nil {
		return nil, errors.New("review: operator configuration has no review block")
	}
	iss, err := issues.NewFromEnv(context.Background(), opCfg.Review.Owner, opCfg.Review.Repository, opCfg.Review.BotLogin)
	if err != nil {
		return nil, err
	}
	return &review.Service{
		Store:    reviewStore{s: mustFileStore(g)},
		Issues:   iss,
		Notifier: discord.New(os.Getenv(opCfg.Review.DiscordWebhookEnv)),
		// Preview URLs are derived from operator configuration: the listen
		// address cannot be turned into the Ingress URL.
		PreviewURL: previewURLFunc(opCfg),
		// Discord receives the Issue link only.
		IssueURL: func(issue int) string {
			return fmt.Sprintf("https://github.com/%s/%s/issues/%d", opCfg.Review.Owner, opCfg.Review.Repository, issue)
		},
	}, nil
}

// loadRunView projects a finished run for review validation.
func loadRunView(ctx context.Context, store *filestore.Store, id string) (review.RunView, error) {
	r, err := store.LoadRun(ctx, id)
	if err != nil {
		return review.RunView{}, err
	}
	if r.Phase != run.PhaseSucceeded {
		return review.RunView{}, fmt.Errorf("review: run %s is not succeeded (phase=%s)", id, r.Phase)
	}
	if r.Artifacts.ArtifactDigest == "" {
		return review.RunView{}, fmt.Errorf("review: run %s has no sealed artifact yet", id)
	}
	var cfg experiment.Config
	if err := json.Unmarshal([]byte(r.RecipeJSON), &cfg); err != nil {
		return review.RunView{}, fmt.Errorf("review: decode recipe snapshot of %s: %w", id, err)
	}
	return review.RunView{
		ID:             r.ID,
		ArtifactDigest: r.Artifacts.ArtifactDigest,
		Fingerprint:    r.Fingerprint,
		Model:          cfg.Model,
		ModelDigest:    r.Artifacts.ModelTreeDigest,
	}, nil
}

func newReviewCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Issue-based A/B review records (the Issue is the canonical history)",
	}
	cmd.AddCommand(
		newReviewRequestCmd(g),
		newReviewVoteCmd(g),
		newReviewStatusCmd(g),
	)
	return cmd
}

func newReviewRequestCmd(g *globalFlags) *cobra.Command {
	var configPath string
	var issue int
	cmd := &cobra.Command{
		Use:   "request <baseline-run-id> <candidate-run-id>",
		Short: "Post an A/B comparison comment on the Issue and record the review",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath == "" {
				return errors.New("review: --config is required")
			}
			opCfg, err := operator.Load(configPath)
			if err != nil {
				return err
			}
			svc, err := buildReviewService(g, opCfg)
			if err != nil {
				return err
			}
			store := mustFileStore(g)
			base, err := loadRunView(context.Background(), store, args[0])
			if err != nil {
				return err
			}
			cand, err := loadRunView(context.Background(), store, args[1])
			if err != nil {
				return err
			}
			rec, err := svc.Request(context.Background(), base, cand, issue)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "review %s recorded: A=%s B=%s (issue #%d)\n",
				rec.ID, rec.Baseline, rec.Candidate, rec.Issue)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "operator configuration (required)")
	cmd.Flags().IntVar(&issue, "issue", 0, "originating Issue number (required)")
	_ = cmd.MarkFlagRequired("issue")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

func newReviewVoteCmd(g *globalFlags) *cobra.Command {
	var configPath, choice, notes string
	cmd := &cobra.Command{
		Use:   "vote <review-id>",
		Short: "Record a vote (A | B | tie | invalid) on the review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath == "" {
				return errors.New("review: --config is required")
			}
			opCfg, err := operator.Load(configPath)
			if err != nil {
				return err
			}
			svc, err := buildReviewService(g, opCfg)
			if err != nil {
				return err
			}
			r, err := svc.Vote(context.Background(), args[0], choice, notes, "api-token")
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "vote recorded on review %s: %s\n", r.ID, choice)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "operator configuration (required)")
	cmd.Flags().StringVar(&choice, "choice", "", "A | B | tie | invalid (required)")
	cmd.Flags().StringVar(&notes, "notes", "", "optional notes")
	_ = cmd.MarkFlagRequired("choice")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

func newReviewStatusCmd(g *globalFlags) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "status <review-id>",
		Short: "Show the review with its votes rebuilt from the Issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath == "" {
				return errors.New("review: --config is required")
			}
			opCfg, err := operator.Load(configPath)
			if err != nil {
				return err
			}
			svc, err := buildReviewService(g, opCfg)
			if err != nil {
				return err
			}
			// The Service reads the Issue: a concurrent vote or a lost local
			// save is still reflected.
			r, err := svc.Status(context.Background(), args[0])
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(r, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "operator configuration (required)")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

// previewURLFunc renders the external preview URL of a run from operator
// configuration. It is injected into the review service so review comments
// link the previews without the URL ever being persisted on a run.
func previewURLFunc(opCfg operator.Config) func(runID string) string {
	base := ""
	if opCfg.Preview != nil {
		base = strings.TrimRight(opCfg.Preview.BaseURL, "/")
	}
	return func(runID string) string {
		if base == "" {
			return runID
		}
		return base + "/v1/runs/" + runID + "/artifacts/index.html"
	}
}
