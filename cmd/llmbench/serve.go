package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/nomanoma121/llm-bench/internal/httpapi"
	"github.com/nomanoma121/llm-bench/internal/kube"
	"github.com/nomanoma121/llm-bench/internal/measurement"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/preview"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// kubeClient builds a clientset from the kubeconfig or the in-cluster config.
func kubeClient(kubeconfig string) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
		if err != nil {
			cfg, err = clientcmd.BuildConfigFromFlags("", "")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("serve: kubernetes config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("serve: kubernetes client: %w", err)
	}
	return client, nil
}

// apiService adapts the controller internals to the HTTP API surface. It is
// intentionally thin: preparation logic lives in prepareRun, execution in the
// engine.
type apiService struct {
	g      *globalFlags
	opCfg  operator.Config
	engine *run.Engine
	store  run.RunStore
	// artifactsDir resolves the run artifact root; evidence lives beside the
	// visual payload on the harness volume.
	artifactsDir func(runID string) string
	// authenticated reports whether the API is protected by a token. Local
	// targets may only run over HTTP when the API is authenticated AND the
	// operator opted the target in (design §4.11).
	authenticated bool
}

// Submit implements httpapi.RunService.
func (s *apiService) Submit(ctx context.Context, experimentPath, inputCommit string) (run.Run, error) {
	r, inputs, err := prepareRun(s.g, s.opCfg, experimentPath, inputCommit)
	if err != nil {
		return run.Run{}, err
	}
	target := s.opCfg.Targets[r.Target]
	if target.Sandbox == nil { // the local executor runs on the harness host
		if !s.authenticated || !target.AllowHTTPLocal {
			return run.Run{}, fmt.Errorf("%w: target %q (requires an API token and allow_http_local)", httpapi.ErrLocalForbidden, r.Target)
		}
	}
	return s.engine.Submit(ctx, r, inputs)
}

// Status implements httpapi.RunService.
func (s *apiService) Status(ctx context.Context, id string) (run.Run, error) {
	return s.store.LoadRun(ctx, id)
}

// Metrics implements httpapi.RunService: sealed evidence is served only after
// the recorded digest has been re-checked against the bytes on disk, so a
// tampered or truncated file is refused instead of published.
func (s *apiService) Metrics(ctx context.Context, id string) ([]byte, error) {
	r, err := s.store.LoadRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.MetricsDigest == "" {
		return nil, httpapi.ErrNoEvidence
	}
	path := filepath.Join(s.artifactsDir(r.ID), measurement.EvidenceDir, measurement.EvidenceFileName)
	if _, err := measurement.Verify(path, r.MetricsDigest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, httpapi.ErrNoEvidence
		}
		return nil, err
	}
	return os.ReadFile(path)
}

func newServeCmd(g *globalFlags) *cobra.Command {
	var configPath, addr string
	var previewAddr string
	var previewPublic bool
	var retryInterval time.Duration
	var coordNamespace, leaseName string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller dispatcher and HTTP API",
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath == "" {
				return errors.New("serve: --config is required")
			}
			if (coordNamespace == "") != (leaseName == "") {
				return errors.New("serve: --coordination-namespace and --lease-name must be set together")
			}
			if previewAddr != "" && !loopbackBind(previewAddr) && !previewPublic {
				// The preview listener has no bearer auth of its own: it is
				// meant to sit behind an Ingress that terminates cluster
				// authentication. Binding it anywhere else requires the
				// operator to assert that upstream authentication exists.
				return errors.New("serve: refusing to bind --preview-addr to a non-loopback address without --preview-public")
			}
			if previewPublic && previewAddr == "" {
				return errors.New("serve: --preview-public requires --preview-addr")
			}
			opCfg, err := operator.Load(configPath)
			if err != nil {
				return err
			}
			if previewAddr != "" && opCfg.Preview == nil {
				return errors.New("serve: --preview-addr requires preview.base_url in the operator configuration")
			}
			token := os.Getenv("LLMBENCH_API_TOKEN")
			if token == "" && !loopbackBind(addr) {
				return errors.New("serve: refusing to serve without LLMBENCH_API_TOKEN on a non-loopback address")
			}

			// Recipe snapshots and artifacts always live on the harness
			// persistent volume; run records and target leases move to
			// Kubernetes only when coordination is configured.
			fs := mustFileStore(g)
			engine, err := buildEngineWithStores(g, opCfg, retryInterval,
				newSandboxClients(g), newGitopsGateways(g),
				engineStores{Runs: fs, Leases: fs, Snapshots: fs},
			)
			if err != nil {
				return err
			}
			dispatch := func(ctx context.Context) error { return engine.Run(ctx) }

			if coordNamespace != "" {
				client, err := kubeClient(g.kubeconfig)
				if err != nil {
					return err
				}
				engine, err = buildEngineWithStores(g, opCfg, retryInterval,
					newSandboxClients(g), newGitopsGateways(g),
					engineStores{
						Runs:      kube.NewRunStore(client, coordNamespace),
						Leases:    kube.NewLeaseStore(client, coordNamespace),
						Snapshots: fs,
					},
				)
				if err != nil {
					return err
				}
				// Only the leader dispatches; losing the lease cancels the
				// leader context and with it every worker it started.
				dispatch = func(ctx context.Context) error {
					return kube.RunWithLeadership(ctx, kube.LeaderConfig{
						Client:    client,
						Namespace: coordNamespace,
						LeaseName: leaseName,
						Log:       controllerLogger(),
					}, engine.Run)
				}
			}

			svc := &apiService{
				g:             g,
				opCfg:         opCfg,
				engine:        engine,
				store:         engine.Store,
				artifactsDir:  fs.ArtifactsDir,
				authenticated: token != "",
			}
			handler := (&httpapi.Server{Service: svc, Token: token, Log: controllerLogger()}).Handler()
			srv := &http.Server{
				Addr:              addr,
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			}
			// The preview listener shares only the run store and the artifact
			// directory with the controller: no token, no control routes and
			// no sandbox client (docs/architecture.md §4.9).
			var previewSrv *http.Server
			if previewAddr != "" {
				previewSrv = &http.Server{
					Addr: previewAddr,
					Handler: &preview.Handler{
						Store:        engine.Store,
						ArtifactsDir: fs.ArtifactsDir,
					},
					ReadHeaderTimeout: 5 * time.Second,
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			// The dispatcher waits for its workers before returning; serve
			// waits for the dispatcher so the process never exits while an
			// effect is still running (and so the leader lease is only
			// released after the workers have stopped).
			dispatchDone := make(chan struct{})
			go func() {
				defer close(dispatchDone)
				if err := dispatch(ctx); err != nil && ctx.Err() == nil {
					controllerLogger().Error(err, "dispatcher stopped")
				}
			}()
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutdownCtx)
				if previewSrv != nil {
					_ = previewSrv.Shutdown(shutdownCtx)
				}
			}()

			if previewSrv != nil {
				go func() {
					fmt.Fprintf(cmd.OutOrStdout(), "preview listening on %s\n", previewAddr)
					if err := previewSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
						controllerLogger().Error(err, "preview listener stopped")
						stop()
					}
				}()
			}
			fmt.Fprintf(cmd.OutOrStdout(), "listening on %s\n", addr)
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			<-dispatchDone
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "operator configuration (required)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "HTTP listen address")
	cmd.Flags().StringVar(&previewAddr, "preview-addr", "", "candidate artifact preview listen address (disabled when empty)")
	cmd.Flags().BoolVar(&previewPublic, "preview-public", false, "assert that an upstream proxy (Ingress + cluster auth) authenticates --preview-addr")
	cmd.Flags().DurationVar(&retryInterval, "retry-interval", 30*time.Second, "interval for pending acquire/release retries and dispatch scans")
	cmd.Flags().StringVar(&coordNamespace, "coordination-namespace", "", "kubernetes namespace for run records, target leases and leader election")
	cmd.Flags().StringVar(&leaseName, "lease-name", "", "leader election lease name (reserved)")
	return cmd
}

// loopbackBind reports whether the address binds only to loopback.
// loopbackBind reports whether the address binds only to loopback. An empty
// host (":8080") binds every interface and is NOT loopback.
func loopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
