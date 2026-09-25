package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/httpapi"
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
			b, err := json.MarshalIndent(httpapi.NewStatusView(r), "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
}

// keep run imported for the Run type in future command additions
var _ = run.PhasePending
