package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/sandbox"
)

// The Agent's interface to a running job (docs/mvp.md §8.1). Every command
// resolves the sandbox from the job id, so a sandbox that was replaced after a
// node or GPU failure is picked up by the next call: "rebind" is nothing more
// than the next lookup, and the controller never has to push connection
// details into the Agent Pod.
func newSandboxJobCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Work inside the sandbox of a job (exec, push, pull, ls)",
		Long: "These commands are the Agent's interface to a running job. The sandbox is\n" +
			"found by the job id on every call, so a replacement sandbox is used\n" +
			"automatically and no connection details are stored anywhere.",
	}
	cmd.AddCommand(
		newSandboxJobExecCmd(g),
		newSandboxJobPushCmd(g),
		newSandboxJobPullCmd(g),
		newSandboxJobLsCmd(g),
	)
	return cmd
}

// client resolves the sandbox namespace from the operator configuration the
// controller uses, so the CLI and the controller agree without extra
// environment variables.
func sandboxJobClient(g *globalFlags, jobID string, namespaceOverride string) (*sandbox.Client, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("sandbox job: a job id is required")
	}
	namespace := namespaceOverride
	if namespace == "" {
		namespace = os.Getenv("LLMBENCH_SANDBOX_NAMESPACE")
	}
	if namespace == "" {
		cfg, err := loadOperatorMVP(g.operatorConfig)
		if err != nil {
			return nil, fmt.Errorf("sandbox job: cannot determine the sandbox namespace: %w", err)
		}
		namespace = cfg.Sandbox.Namespace
	}
	if namespace == "" {
		return nil, fmt.Errorf("sandbox job: the operator configuration has no sandbox namespace")
	}
	return &sandbox.Client{Namespace: namespace, Kubeconfig: g.kubeconfig}, nil
}

func newSandboxJobExecCmd(g *globalFlags) *cobra.Command {
	var namespace, cwd string
	cmd := &cobra.Command{
		Use:   "exec <job-id> -- <argv...>",
		Short: "Run a command in the job's sandbox",
		Long: "The command runs without a shell, so pass the argv explicitly, for example\n" +
			"`llmbench sandbox job exec 2026-09-27-issue42 -- ls -la /workspace`.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := sandboxJobClient(g, args[0], namespace)
			if err != nil {
				return err
			}
			stdout, stderr, code, err := client.JobExec(cmd.Context(), args[0], args[1:], nil, cwd)
			if err != nil {
				return err
			}
			if len(stdout) > 0 {
				fmt.Fprint(cmd.OutOrStdout(), string(stdout))
			}
			if len(stderr) > 0 {
				fmt.Fprint(cmd.ErrOrStderr(), string(stderr))
			}
			if code != 0 {
				return fmt.Errorf("sandbox job: the command exited with %d", code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace of the sandbox (defaults to the operator configuration)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory inside the sandbox")
	return cmd
}

func newSandboxJobPushCmd(g *globalFlags) *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "push <job-id> <local-path> <sandbox-path>",
		Short: "Copy a local file into the job's sandbox",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := sandboxJobClient(g, args[0], namespace)
			if err != nil {
				return err
			}
			f, err := os.Open(args[1])
			if err != nil {
				return err
			}
			defer f.Close()
			if err := client.JobPut(cmd.Context(), args[0], args[2], f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pushed %s -> %s\n", args[1], args[2])
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace of the sandbox (defaults to the operator configuration)")
	return cmd
}

func newSandboxJobPullCmd(g *globalFlags) *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "pull <job-id> <sandbox-path> [local-path]",
		Short: "Copy a file out of the job's sandbox (default: stdout)",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := sandboxJobClient(g, args[0], namespace)
			if err != nil {
				return err
			}
			var w io.Writer = cmd.OutOrStdout()
			var file *os.File
			if len(args) == 3 {
				file, err = os.Create(args[2])
				if err != nil {
					return err
				}
				defer file.Close()
				w = file
			}
			if err := client.JobPull(cmd.Context(), args[0], args[1], w); err != nil {
				return err
			}
			if file != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "pulled %s -> %s\n", args[1], args[2])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace of the sandbox (defaults to the operator configuration)")
	return cmd
}

func newSandboxJobLsCmd(g *globalFlags) *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "ls <job-id> [path]",
		Short: "List a directory in the job's sandbox",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := sandboxJobClient(g, args[0], namespace)
			if err != nil {
				return err
			}
			dir := "/"
			if len(args) == 2 {
				dir = args[1]
			}
			entries, err := client.JobList(cmd.Context(), args[0], dir)
			if err != nil {
				return err
			}
			for _, e := range entries {
				kind := "-"
				if e.IsDir {
					kind = "d"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %10d %s\n", kind, e.Size, path.Join(dir, e.Name))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace of the sandbox (defaults to the operator configuration)")
	return cmd
}

// newJobDoneCmd lets an Agent declare that it finished an optimization round.
// The controller reads the annotation instead of polling the DSH, and the
// pushed branch and commit travel with it so the controller can open the pull
// request without asking anything else.
func newJobDoneCmd(g *globalFlags) *cobra.Command {
	var (
		jobID     string
		status    string
		branch    string
		commit    string
		namespace string
	)
	cmd := &cobra.Command{
		Use:   "done --job <job-id> --status complete|failed",
		Short: "Record an Agent's result on the job's sandbox",
		Long: "Writes the Agent's outcome onto the SandboxClaim, which is how the\n" +
			"controller learns that an optimization round finished (docs/mvp.md §8.1).\n" +
			"A branch and commit are required for a completed round: without them there\n" +
			"is nothing for the controller to open a pull request for.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if status != "complete" && status != "failed" {
				return fmt.Errorf("%w: --status must be complete or failed", job.ErrInvalid)
			}
			if status == "complete" && (branch == "" || commit == "") {
				return fmt.Errorf("%w: --branch and --commit are required for a completed round", job.ErrInvalid)
			}
			client, err := sandboxJobClient(g, jobID, namespace)
			if err != nil {
				return err
			}
			annotations := map[string]string{sandbox.AgentResultAnnotation: status}
			if branch != "" {
				annotations[sandbox.AgentBranchAnnotation] = branch
			}
			if commit != "" {
				annotations[sandbox.AgentCommitAnnotation] = commit
			}
			if err := client.AnnotateJobClaim(cmd.Context(), jobID, annotations); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "recorded %s for job %s\n", status, jobID)
			return nil
		},
	}
	cmd.Flags().StringVar(&jobID, "job", "", "job id (required)")
	cmd.Flags().StringVar(&status, "status", "", "complete or failed (required)")
	cmd.Flags().StringVar(&branch, "branch", "", "pushed branch of the final result")
	cmd.Flags().StringVar(&commit, "commit", "", "pushed commit of the final result")
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace of the sandbox (defaults to the operator configuration)")
	_ = cmd.MarkFlagRequired("job")
	_ = cmd.MarkFlagRequired("status")
	return cmd
}
