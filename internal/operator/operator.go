// Package operator parses and validates the operator-owned controller
// configuration. Everything privileged lives here and only here: target
// allowlisting, hook commands, GitOps repository paths, Sandbox pools,
// publication site, review settings and execution time limits. Experiment
// recipes can only reference a target by ID.
package operator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Hook kinds used by PlannedHook.
const (
	KindCommand      = "command"
	KindGitOps       = "gitops"
	KindSandboxClaim = "sandbox-claim"
)

// PlanVersion is the schema version of the hook plan snapshot.
const PlanVersion = 1

// DefaultLimits apply when neither Defaults nor the target override them.
const (
	DefaultMaxReadyDuration     = 10 * time.Minute
	DefaultMaxExecutionDuration = 2 * time.Hour
)

// CommandHook is an operator-provided acquire/release pair of argv commands.
// Exit code 75 from acquire means "not ready yet". Both commands must be safe
// to retry.
type CommandHook struct {
	Name    string   `yaml:"name" json:"name"`
	Acquire []string `yaml:"acquire" json:"acquire"`
	Release []string `yaml:"release" json:"release"`
}

// GitOps describes the manifest repository that controls the inference
// workload. The controller opens pause/restore PRs against it; it never edits
// the benchmark repository.
type GitOps struct {
	Owner       string   `yaml:"owner" json:"owner"`
	Repository  string   `yaml:"repository" json:"repository"`
	BaseBranch  string   `yaml:"base_branch" json:"base_branch"`
	FilePath    string   `yaml:"file_path" json:"file_path"`
	YAMLPath    []string `yaml:"yaml_path" json:"yaml_path"`
	ActiveValue string   `yaml:"active_value" json:"active_value"`
	PausedValue string   `yaml:"paused_value" json:"paused_value"`
	Application struct {
		Namespace string `yaml:"namespace" json:"namespace"`
		Name      string `yaml:"name" json:"name"`
	} `yaml:"application" json:"application"`
	Workload struct {
		Namespace      string `yaml:"namespace" json:"namespace"`
		Deployment     string `yaml:"deployment" json:"deployment"`
		ActiveReplicas int    `yaml:"active_replicas" json:"active_replicas"`
	} `yaml:"workload" json:"workload"`
}

// Sandbox selects the Agent Sandbox pool used for GPU execution.
type Sandbox struct {
	Namespace   string            `yaml:"namespace" json:"namespace"`
	WarmPool    string            `yaml:"warm_pool" json:"warm_pool"`
	ModelSHA256 map[string]string `yaml:"model_sha256,omitempty" json:"model_sha256,omitempty"`
}

// Limits are operator-enforced ceilings the experiment recipe cannot raise.
type Limits struct {
	MaxReadyDuration     time.Duration `yaml:"max_ready_duration" json:"max_ready_duration"`
	MaxExecutionDuration time.Duration `yaml:"max_execution_duration" json:"max_execution_duration"`
}

// Target is one operator-approved execution destination.
type Target struct {
	Hooks          []CommandHook `yaml:"hooks" json:"hooks"`
	GitOps         *GitOps       `yaml:"gitops,omitempty" json:"gitops,omitempty"`
	Sandbox        *Sandbox      `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
	Limits         *Limits       `yaml:"limits,omitempty" json:"limits,omitempty"`
	AllowHTTPLocal bool          `yaml:"allow_http_local" json:"allow_http_local"`
}

// Site is the static hosting configuration used to publish run outputs.
type Site struct {
	Owner      string            `yaml:"owner" json:"owner"`
	Repository string            `yaml:"repository" json:"repository"`
	Branch     string            `yaml:"branch" json:"branch"`
	BaseURL    string            `yaml:"base_url" json:"base_url"`
	ExtraFiles map[string]string `yaml:"extra_files,omitempty" json:"extra_files,omitempty"`
}

// Review configures the Issue-based A/B review record.
type Review struct {
	Owner             string `yaml:"owner" json:"owner"`
	Repository        string `yaml:"repository" json:"repository"`
	BotLogin          string `yaml:"bot_login" json:"bot_login"`
	DiscordWebhookEnv string `yaml:"discord_webhook_env" json:"discord_webhook_env"`
}

// Config is the parsed operator configuration.
type Config struct {
	Targets  map[string]Target `yaml:"targets" json:"targets"`
	Site     *Site             `yaml:"site,omitempty" json:"site,omitempty"`
	Review   *Review           `yaml:"review,omitempty" json:"review,omitempty"`
	Defaults Limits            `yaml:"defaults,omitempty" json:"defaults,omitempty"`
}

// PlannedHook is one element of the frozen hook plan stored on a run. The
// types live in operator (not run) because BuildHookPlan returns them; run
// imports operator, never the reverse. Planned hooks carry no credentials.
type PlannedHook struct {
	PlanVersion int          `json:"plan_version"`
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	Command     []string     `json:"command,omitempty"`
	Release     []string     `json:"release,omitempty"`
	GitOps      *GitOpsPlan  `json:"gitops,omitempty"`
	Sandbox     *SandboxPlan `json:"sandbox,omitempty"`
}

// GitOpsPlan is the sanitized, run-bound snapshot of a GitOps hook. Branch
// names are deterministic derivatives of the run ID.
type GitOpsPlan struct {
	Owner, Repository, BaseBranch, FilePath string
	YAMLPath                                []string
	ActiveValue, PausedValue                string
	PauseBranch, RestoreBranch              string
	AppNamespace, AppName                   string
	WorkloadNamespace, Deployment           string
	ActiveReplicas                          int
}

// YAMLPathString renders the manifest path for error messages.
func (p GitOpsPlan) YAMLPathString() string { return strings.Join(p.YAMLPath, ".") }

// SandboxPlan is the sanitized, run-bound snapshot of a sandbox claim hook.
type SandboxPlan struct {
	Namespace, WarmPool, SandboxClaimName string
}

// BuildHookPlan is the single place that decides hook ordering:
// command hooks (declared order), then GitOps pause/restore, then the sandbox
// claim. Release ordering is the exact reverse and is enforced by the engine.
// runID binds deterministic external names (branches, claim names) to the run.
func BuildHookPlan(t Target, runID string) []PlannedHook {
	plan := make([]PlannedHook, 0, len(t.Hooks)+2)
	for _, h := range t.Hooks {
		plan = append(plan, PlannedHook{
			PlanVersion: PlanVersion,
			Kind:        KindCommand,
			Name:        h.Name,
			Command:     append([]string(nil), h.Acquire...),
			Release:     append([]string(nil), h.Release...),
		})
	}
	if t.GitOps != nil {
		g := t.GitOps
		plan = append(plan, PlannedHook{
			PlanVersion: PlanVersion,
			Kind:        KindGitOps,
			Name:        "gitops",
			GitOps: &GitOpsPlan{
				Owner: g.Owner, Repository: g.Repository, BaseBranch: g.BaseBranch,
				FilePath: g.FilePath, YAMLPath: append([]string(nil), g.YAMLPath...),
				ActiveValue: g.ActiveValue, PausedValue: g.PausedValue,
				PauseBranch: "llmbench/pause-" + runID, RestoreBranch: "llmbench/restore-" + runID,
				AppNamespace: g.Application.Namespace, AppName: g.Application.Name,
				WorkloadNamespace: g.Workload.Namespace, Deployment: g.Workload.Deployment,
				ActiveReplicas: g.Workload.ActiveReplicas,
			},
		})
	}
	if t.Sandbox != nil {
		plan = append(plan, PlannedHook{
			PlanVersion: PlanVersion,
			Kind:        KindSandboxClaim,
			Name:        "sandbox-claim",
			Sandbox: &SandboxPlan{
				Namespace: t.Sandbox.Namespace, WarmPool: t.Sandbox.WarmPool,
				SandboxClaimName: "llmbench-" + runID,
			},
		})
	}
	return plan
}

// PlanDigest returns the canonical-JSON sha256 of a hook plan. It is stored on
// the run and re-verified by the HookSource before every rebuild.
func PlanDigest(plan []PlannedHook) (string, error) {
	b, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("operator: plan digest: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// EffectiveLimits resolves the limits for a target, falling back to Defaults
// and then to the package defaults.
func (c Config) EffectiveLimits(t Target) Limits {
	lim := Limits{
		MaxReadyDuration:     DefaultMaxReadyDuration,
		MaxExecutionDuration: DefaultMaxExecutionDuration,
	}
	if c.Defaults.MaxReadyDuration > 0 {
		lim.MaxReadyDuration = c.Defaults.MaxReadyDuration
	}
	if c.Defaults.MaxExecutionDuration > 0 {
		lim.MaxExecutionDuration = c.Defaults.MaxExecutionDuration
	}
	if t.Limits != nil {
		if t.Limits.MaxReadyDuration > 0 {
			lim.MaxReadyDuration = t.Limits.MaxReadyDuration
		}
		if t.Limits.MaxExecutionDuration > 0 {
			lim.MaxExecutionDuration = t.Limits.MaxExecutionDuration
		}
	}
	return lim
}

// ValidateRecipe checks an experiment recipe against this configuration: the
// selected target must be allowlisted and recipe timeouts must fit within the
// operator-enforced ceilings. It takes plain values so that this package does
// not need to import experiment.
func (c Config) ValidateRecipe(target string, readyTimeoutSeconds int) error {
	t, ok := c.Targets[target]
	if !ok {
		return fmt.Errorf("operator: target %q is not allowlisted", target)
	}
	lim := c.EffectiveLimits(t)
	if rt := time.Duration(readyTimeoutSeconds) * time.Second; rt > lim.MaxReadyDuration {
		return fmt.Errorf("operator: ready_timeout_seconds %d exceeds max_ready_duration %s", readyTimeoutSeconds, lim.MaxReadyDuration)
	}
	return nil
}

// Load reads and validates the operator configuration at path.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("operator: load: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads and validates operator configuration. Unknown fields are
// rejected.
func Parse(r io.Reader) (Config, error) {
	var c Config
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("operator: parse: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate enforces the structural invariants of the configuration,
// including the hook ordering invariant over BuildHookPlan output.
func (c Config) Validate() error {
	if len(c.Targets) == 0 {
		return errors.New("operator: at least one target is required")
	}
	seen := map[string]bool{}
	for id, t := range c.Targets {
		if id == "" {
			return errors.New("operator: target id must not be empty")
		}
		for _, h := range t.Hooks {
			if h.Name == "" {
				return fmt.Errorf("operator: target %q has a hook without a name", id)
			}
			if seen[id+"/"+h.Name] {
				return fmt.Errorf("operator: target %q has duplicate hook %q", id, h.Name)
			}
			seen[id+"/"+h.Name] = true
			if len(h.Acquire) == 0 || len(h.Release) == 0 {
				return fmt.Errorf("operator: hook %q on target %q requires both acquire and release argv", h.Name, id)
			}
		}
		if t.GitOps != nil {
			if err := validateGitOps(*t.GitOps); err != nil {
				return fmt.Errorf("operator: target %q gitops: %w", id, err)
			}
		}
		if t.Sandbox != nil {
			if t.Sandbox.Namespace == "" || t.Sandbox.WarmPool == "" {
				return fmt.Errorf("operator: target %q sandbox requires namespace and warm_pool", id)
			}
		}
		plan := BuildHookPlan(t, "planvalidation")
		gitopsIdx, sandboxIdx := -1, -1
		for i, p := range plan {
			switch p.Kind {
			case KindGitOps:
				gitopsIdx = i
			case KindSandboxClaim:
				sandboxIdx = i
			}
		}
		if gitopsIdx >= 0 && sandboxIdx >= 0 && gitopsIdx > sandboxIdx {
			return fmt.Errorf("operator: target %q violates hook ordering: gitops must precede sandbox-claim", id)
		}
	}
	if c.Site != nil {
		if c.Site.Owner == "" || c.Site.Repository == "" || c.Site.Branch == "" || c.Site.BaseURL == "" {
			return errors.New("operator: site requires owner, repository, branch and base_url")
		}
	}
	if c.Review != nil {
		if c.Review.Owner == "" || c.Review.Repository == "" {
			return errors.New("operator: review requires owner and repository")
		}
		if c.Review.BotLogin == "" {
			// Comments are only trusted when their author is known; an empty
			// login would let any participant forge controller records.
			return errors.New("operator: review requires bot_login")
		}
	}
	return nil
}

func validateGitOps(g GitOps) error {
	var errs []error
	for name, v := range map[string]string{
		"owner": g.Owner, "repository": g.Repository, "base_branch": g.BaseBranch,
		"file_path": g.FilePath, "active_value": g.ActiveValue, "paused_value": g.PausedValue,
		"application.namespace": g.Application.Namespace, "application.name": g.Application.Name,
		"workload.namespace": g.Workload.Namespace, "workload.deployment": g.Workload.Deployment,
	} {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	if len(g.YAMLPath) == 0 {
		errs = append(errs, errors.New("yaml_path is required"))
	}
	if g.ActiveValue == g.PausedValue {
		errs = append(errs, errors.New("active_value and paused_value must differ"))
	}
	if g.Workload.ActiveReplicas <= 0 {
		errs = append(errs, errors.New("workload.active_replicas must be positive"))
	}
	return errors.Join(errs...)
}
