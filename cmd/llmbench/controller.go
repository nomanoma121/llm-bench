package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/nomanoma121/llm-bench/internal/controller"
	"github.com/nomanoma121/llm-bench/internal/github"
	"github.com/nomanoma121/llm-bench/internal/gitops"
	"github.com/nomanoma121/llm-bench/internal/harness"
	"github.com/nomanoma121/llm-bench/internal/sandbox"
)

type config struct {
	Repository string `yaml:"repository"`
	Branch     string `yaml:"branch"`
	GitHubApp  struct {
		AppID          int64  `yaml:"app_id"`
		InstallationID int64  `yaml:"installation_id"`
		PrivateKeyFile string `yaml:"private_key_file"`
	} `yaml:"github_app"`
	Sandbox struct {
		Namespace string `yaml:"namespace"`
		WarmPool  string `yaml:"warm_pool"`
		// EngineWarmPools maps an engine to the warm pool whose image runs it.
		EngineWarmPools map[string]string `yaml:"engine_warm_pools"`
		Workdir         string            `yaml:"workdir"`
		LLMBench        []string          `yaml:"llmbench"`
	} `yaml:"sandbox"`
	GitOps  gitops.Target `yaml:"gitops"`
	Harness *struct {
		Namespace   string   `yaml:"namespace"`
		PodSelector string   `yaml:"pod_selector"`
		Container   string   `yaml:"container"`
		Command     []string `yaml:"command"`
		CWD         string   `yaml:"cwd"`
	} `yaml:"harness"`
	PollIntervalSeconds int `yaml:"poll_interval_seconds"`
	PauseTimeoutMinutes int `yaml:"pause_timeout_minutes"`
}

func loadConfig(path string) (config, error) {
	f, err := os.Open(path)
	if err != nil {
		return config{}, err
	}
	defer f.Close()
	cfg := config{Branch: "main", PollIntervalSeconds: 15, PauseTimeoutMinutes: 30}
	cfg.Sandbox.Workdir = "/workspace/llm-bench"
	cfg.Sandbox.LLMBench = []string{"llmbench"}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func newControllerCmd(kubeconfig *string) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Poll GitHub issues and run benchmark and optimize jobs on the GPU",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(configPath)
			if err != nil {
				return err
			}
			c, err := buildController(cfg, *kubeconfig)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			c.Logf("controller watching %s", cfg.Repository)
			return c.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/llmbench/config.yaml", "controller configuration")
	return cmd
}

func buildController(cfg config, kubeconfig string) (*controller.Controller, error) {
	app, err := github.NewApp(cfg.GitHubApp.AppID, cfg.GitHubApp.InstallationID, cfg.GitHubApp.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	gh, err := app.Client()
	if err != nil {
		return nil, err
	}
	repo, err := github.NewRepo(gh, cfg.Repository, cfg.Branch)
	if err != nil {
		return nil, err
	}
	manifests, err := github.NewRepo(gh, cfg.GitOps.Repository, cfg.GitOps.Branch)
	if err != nil {
		return nil, err
	}
	rest, err := restConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	kube, err := kubernetes.NewForConfig(rest)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(rest)
	if err != nil {
		return nil, err
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	c := &controller.Controller{
		GitHub:       repo,
		Sandbox:      &sandbox.Client{Namespace: cfg.Sandbox.Namespace, WarmPool: cfg.Sandbox.WarmPool, EngineWarmPools: cfg.Sandbox.EngineWarmPools, REST: rest},
		GitOps:       &gitops.GitOps{Target: cfg.GitOps, Manifests: manifests, Cluster: gitops.KubeCluster{Dynamic: dyn, Client: kube}},
		Repository:   cfg.Repository,
		Workdir:      cfg.Sandbox.Workdir,
		LLMBench:     cfg.Sandbox.LLMBench,
		GitToken:     app.Token,
		Interval:     time.Duration(cfg.PollIntervalSeconds) * time.Second,
		PauseTimeout: time.Duration(cfg.PauseTimeoutMinutes) * time.Minute,
		SandboxCheck: time.Minute,
		Logf:         logger.Printf,
	}
	if h := cfg.Harness; h != nil {
		c.Harness = &harness.Harness{
			Client: kube, REST: rest, Namespace: h.Namespace, Selector: h.PodSelector,
			Container: h.Container, Command: h.Command, CWD: h.CWD, Stderr: os.Stderr,
		}
	}
	return c, nil
}
