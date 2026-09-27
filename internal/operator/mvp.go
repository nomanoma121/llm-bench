package operator

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nomanoma121/llm-bench/internal/job"
)

// MVP is the operator-owned configuration of the MVP controller
// (docs/mvp.md §9). Everything a job spec may not decide lives here: which
// repositories and branches the controller touches, which models exist and
// what they resolve to, which runtime images and engines are allowed, the GPU
// lease, the GitOps pause plan, and the sandbox pool.
type MVP struct {
	// Repository is the "owner/name" the controller polls and opens PRs in.
	Repository string `yaml:"repository"`
	// DefaultBranch is the branch a job's PR targets.
	DefaultBranch string `yaml:"default_branch"`
	// Labels are the issue labels that mark a request and its state.
	Labels Labels `yaml:"labels,omitempty"`
	// Lease is the single global GPU lease.
	Lease Lease `yaml:"lease"`
	// GitOps describes the inference workload the controller pauses.
	GitOps *GitOps `yaml:"gitops,omitempty"`
	// Sandbox selects the pool and the command that runs the CLI inside it.
	Sandbox MVPSandbox `yaml:"sandbox"`
	// Models map a job's model id to the weights it resolves to.
	Models []MVPModel `yaml:"models"`
	// Engines and Images are the runtime allowlists a job may choose from.
	Engines     []string `yaml:"engines"`
	Images      []string `yaml:"images"`
	OutputRoots []string `yaml:"output_roots,omitempty"`
	// MaxRounds bounds an optimization job's budget.
	MaxRounds int `yaml:"max_rounds,omitempty"`
	// GitHubApp authenticates the controller.
	GitHubApp MVPGitHubApp `yaml:"github_app"`
	// PollInterval is how often the controller looks for a new request.
	PollIntervalSeconds int `yaml:"poll_interval_seconds,omitempty"`
	// RecoveryIntervalSeconds is how often unfinished work is reconciled.
	RecoveryIntervalSeconds int `yaml:"recovery_interval_seconds,omitempty"`
}

// Labels are the issue labels the controller reads and writes.
type Labels struct {
	Benchmark string `yaml:"benchmark"`
	Optimize  string `yaml:"optimize"`
	Claimed   string `yaml:"claimed"`
	Done      string `yaml:"done"`
	Failed    string `yaml:"failed"`
}

// Lease names the global GPU lease.
type Lease struct {
	Namespace       string `yaml:"namespace"`
	Name            string `yaml:"name"`
	DurationSeconds int    `yaml:"duration_seconds,omitempty"`
}

// MVPSandbox names the Agent Sandbox pool and how to run the benchmark CLI in
// it. It is separate from the v1.6 Sandbox selection, which belongs to the
// frozen visual path.
type MVPSandbox struct {
	Namespace string `yaml:"namespace"`
	WarmPool  string `yaml:"warm_pool"`
	// LLMBench is the argv prefix that runs the CLI inside the sandbox image.
	LLMBench []string `yaml:"llmbench"`
}

// MVPModel is one model a job may ask for.
type MVPModel struct {
	ID string `yaml:"id"`
	// Path is the weights path or repository id inside the sandbox.
	Path string `yaml:"path"`
	// Digest pins the weights. Without it a result cannot prove which weights
	// it measured, so the benchmark marks the run invalid.
	Digest string `yaml:"digest,omitempty"`
}

// MVPGitHubApp authenticates as a GitHub App installation.
type MVPGitHubApp struct {
	AppID          int64  `yaml:"app_id"`
	InstallationID int64  `yaml:"installation_id"`
	PrivateKeyFile string `yaml:"private_key_file"`
	// BaseURL points at a GitHub Enterprise API when it is not github.com.
	BaseURL string `yaml:"base_url,omitempty"`
}

// LoadMVP reads the MVP controller configuration.
func LoadMVP(path string) (MVP, error) {
	f, err := os.Open(path)
	if err != nil {
		return MVP{}, fmt.Errorf("operator: load mvp config: %w", err)
	}
	defer f.Close()
	var m MVP
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return MVP{}, fmt.Errorf("operator: parse mvp config: %w", err)
	}
	m.applyDefaults()
	if err := m.Validate(); err != nil {
		return MVP{}, err
	}
	return m, nil
}

func (m *MVP) applyDefaults() {
	if m.DefaultBranch == "" {
		m.DefaultBranch = "main"
	}
	if m.Labels.Benchmark == "" {
		m.Labels.Benchmark = "llmbench:benchmark"
	}
	if m.Labels.Optimize == "" {
		m.Labels.Optimize = "llmbench:optimize"
	}
	if m.Labels.Claimed == "" {
		m.Labels.Claimed = "llmbench:claimed"
	}
	if m.Labels.Done == "" {
		m.Labels.Done = "llmbench:done"
	}
	if m.Labels.Failed == "" {
		m.Labels.Failed = "llmbench:failed"
	}
	if m.Lease.DurationSeconds <= 0 {
		m.Lease.DurationSeconds = 1800
	}
	if m.PollIntervalSeconds <= 0 {
		m.PollIntervalSeconds = 15
	}
	if m.RecoveryIntervalSeconds <= 0 {
		m.RecoveryIntervalSeconds = 60
	}
	if len(m.OutputRoots) == 0 {
		m.OutputRoots = []string{job.OutputRoot}
	}
}

// Validate checks the configuration is complete enough to run.
func (m MVP) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if parts := strings.Split(m.Repository, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		add("repository must look like owner/name, got %q", m.Repository)
	}
	if m.DefaultBranch == "" {
		add("default_branch is required")
	}
	if m.Lease.Namespace == "" || m.Lease.Name == "" {
		add("lease.namespace and lease.name are required")
	}
	if m.Sandbox.Namespace == "" || m.Sandbox.WarmPool == "" {
		add("sandbox.namespace and sandbox.warm_pool are required")
	}
	if len(m.Sandbox.LLMBench) == 0 {
		add("sandbox.llmbench is required: the argv prefix that runs the CLI in the sandbox")
	}
	if m.GitHubApp.AppID == 0 || m.GitHubApp.InstallationID == 0 {
		add("github_app.app_id and github_app.installation_id are required")
	}
	if m.GitHubApp.PrivateKeyFile == "" {
		add("github_app.private_key_file is required")
	}
	if len(m.Models) == 0 {
		add("at least one model is required")
	}
	seen := map[string]bool{}
	for i, model := range m.Models {
		if model.ID == "" || model.Path == "" {
			add("models[%d] needs id and path", i)
		}
		if seen[model.ID] {
			add("models[%d] duplicates id %q", i, model.ID)
		}
		seen[model.ID] = true
	}
	if len(m.Engines) == 0 {
		add("engines must not be empty: a job may only choose from the operator list")
	}
	if len(m.Images) == 0 {
		add("images must not be empty: a job may only choose from the operator list")
	}
	if m.GitOps != nil {
		g := m.GitOps
		if g.Owner == "" || g.Repository == "" || g.BaseBranch == "" || g.FilePath == "" || len(g.YAMLPath) == 0 {
			add("gitops needs owner, repository, base_branch, file_path and yaml_path")
		}
		if g.ActiveValue == "" || g.PausedValue == "" {
			add("gitops needs active_value and paused_value")
		}
		if g.Application.Namespace == "" || g.Application.Name == "" {
			add("gitops.application needs namespace and name")
		}
		if g.Workload.Namespace == "" || g.Workload.Deployment == "" {
			add("gitops.workload needs namespace and deployment")
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("operator: invalid mvp config: %w", joinErrors(errs))
}

// Constraints are the job-spec constraints the operator owns.
func (m MVP) Constraints(reservedArgs []string) job.Constraints {
	ids := make([]string, 0, len(m.Models))
	for _, model := range m.Models {
		ids = append(ids, model.ID)
	}
	return job.Constraints{
		Engines:      m.Engines,
		Images:       m.Images,
		Models:       ids,
		OutputRoots:  m.OutputRoots,
		MaxRounds:    m.MaxRounds,
		ReservedArgs: reservedArgs,
	}
}

// Model resolves a job's model id to the weights it names.
func (m MVP) Model(id string) (MVPModel, bool) {
	for _, model := range m.Models {
		if model.ID == id {
			return model, true
		}
	}
	return MVPModel{}, false
}

// RepoParts splits the repository into owner and name.
func (m MVP) RepoParts() (string, string) {
	parts := strings.SplitN(m.Repository, "/", 2)
	if len(parts) != 2 {
		return m.Repository, ""
	}
	return parts[0], parts[1]
}

// GitOpsPlanFor binds the operator's GitOps configuration to one job, so the
// pause and restore branches are deterministic per job.
func (m MVP) GitOpsPlanFor(jobID string) (*GitOpsPlan, error) {
	if m.GitOps == nil {
		return nil, fmt.Errorf("operator: gitops is not configured; the controller cannot pause the inference workload")
	}
	g := m.GitOps
	return &GitOpsPlan{
		Owner: g.Owner, Repository: g.Repository, BaseBranch: g.BaseBranch,
		FilePath: g.FilePath, YAMLPath: append([]string(nil), g.YAMLPath...),
		ActiveValue: g.ActiveValue, PausedValue: g.PausedValue,
		PauseBranch: "llmbench/pause-" + jobID, RestoreBranch: "llmbench/restore-" + jobID,
		AppNamespace: g.Application.Namespace, AppName: g.Application.Name,
		WorkloadNamespace: g.Workload.Namespace, Deployment: g.Workload.Deployment,
		ActiveReplicas: g.Workload.ActiveReplicas,
	}, nil
}

// joinErrors renders a list of validation errors on one line each.
func joinErrors(errs []error) error {
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		msgs = append(msgs, err.Error())
	}
	return fmt.Errorf("%s", strings.Join(msgs, "; "))
}
