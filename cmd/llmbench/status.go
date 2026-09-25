package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/run"
)

func newStatusCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status <run-id>",
		Short: "Show the recorded state of a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := newFileStore(g)
			if err != nil {
				return err
			}
			r, err := store.LoadRun(context.Background(), args[0])
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(newStatusView(r), "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
}

// statusView is the user-facing projection of a run record. The labels
// awaiting_acquire and needs_restore are derived views: the design has no
// dedicated phases for them (acquiring with a wait reason; releasing with a
// hook error), but operators know them by those names.
type statusView struct {
	ID              string              `json:"id"`
	Target          string              `json:"target"`
	Phase           run.Phase           `json:"phase"`
	DerivedLabel    string              `json:"derived_label,omitempty"`
	ExecutionResult run.ExecutionResult `json:"execution_result"`
	ExecutionState  run.ExecutionState  `json:"execution_state"`
	LeaseState      run.LeaseState      `json:"lease_state"`
	WaitReason      string              `json:"wait_reason,omitempty"`
	PublishError    string              `json:"publish_error,omitempty"`
	PublicURL       string              `json:"public_url,omitempty"`
	Hooks           []run.HookState     `json:"hooks"`
	Artifacts       run.Artifacts       `json:"artifacts"`
}

func newStatusView(r run.Run) statusView {
	v := statusView{
		ID:              r.ID,
		Target:          r.Target,
		Phase:           r.Phase,
		ExecutionResult: r.ExecutionResult,
		ExecutionState:  r.ExecutionState,
		LeaseState:      r.LeaseState,
		WaitReason:      r.WaitReason,
		PublishError:    r.PublishError,
		PublicURL:       r.PublicURL,
		Hooks:           r.Hooks,
		Artifacts:       r.Artifacts,
	}
	switch {
	case r.Phase == run.PhaseAcquiring && r.WaitReason != "":
		v.DerivedLabel = "awaiting_acquire"
	case r.Phase == run.PhaseReleasing && hasHookError(r):
		v.DerivedLabel = "needs_restore"
	}
	return v
}

func hasHookError(r run.Run) bool {
	for _, h := range r.Hooks {
		if h.Error != "" {
			return true
		}
	}
	return false
}
