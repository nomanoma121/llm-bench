package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/nomanoma121/llm-bench/internal/filestore"
	"github.com/nomanoma121/llm-bench/internal/httpapi"
	"github.com/nomanoma121/llm-bench/internal/operator"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// apiService adapts the controller internals to the HTTP API surface. It is
// intentionally thin: preparation logic lives in prepareRun, execution in the
// engine.
type apiService struct {
	g      *globalFlags
	opCfg  operator.Config
	engine *run.Engine
	store  *filestore.Store
}

// Submit implements httpapi.RunService.
func (s *apiService) Submit(ctx context.Context, experimentPath, inputCommit string) (run.Run, error) {
	r, inputs, err := prepareRun(s.g, s.opCfg, experimentPath, inputCommit)
	if err != nil {
		return run.Run{}, err
	}
	target := s.opCfg.Targets[r.Target]
	if target.Sandbox == nil && !target.AllowHTTPLocal {
		return run.Run{}, fmt.Errorf("%w: target %q", httpapi.ErrLocalForbidden, r.Target)
	}
	return s.engine.Submit(ctx, r, inputs)
}

// Status implements httpapi.RunService.
func (s *apiService) Status(ctx context.Context, id string) (run.Run, error) {
	return s.store.LoadRun(ctx, id)
}

func newServeCmd(g *globalFlags) *cobra.Command {
	var configPath, addr string
	var retryInterval time.Duration
	var coordNamespace, leaseName string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller dispatcher and HTTP API",
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath == "" {
				return errors.New("serve: --config is required")
			}
			if coordNamespace != "" || leaseName != "" {
				return errors.New("serve: kubernetes coordination is not available yet")
			}
			opCfg, err := operator.Load(configPath)
			if err != nil {
				return err
			}
			token := os.Getenv("LLMBENCH_API_TOKEN")
			if token == "" && !loopbackBind(addr) {
				return errors.New("serve: refusing to serve without LLMBENCH_API_TOKEN on a non-loopback address")
			}

			engine := buildEngine(g, opCfg, retryInterval)
			svc := &apiService{
				g:      g,
				opCfg:  opCfg,
				engine: engine,
				store:  mustFileStore(g),
			}
			handler := (&httpapi.Server{Service: svc, Token: token, Log: controllerLogger()}).Handler()
			srv := &http.Server{
				Addr:              addr,
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			go engine.Run(ctx)
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutdownCtx)
			}()

			fmt.Fprintf(cmd.OutOrStdout(), "listening on %s\n", addr)
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "operator configuration (required)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "HTTP listen address")
	cmd.Flags().DurationVar(&retryInterval, "retry-interval", 30*time.Second, "interval for pending acquire/release retries and dispatch scans")
	cmd.Flags().StringVar(&coordNamespace, "coordination-namespace", "", "kubernetes namespace for run coordination (reserved)")
	cmd.Flags().StringVar(&leaseName, "lease-name", "", "leader election lease name (reserved)")
	return cmd
}

// loopbackBind reports whether the address binds only to loopback.
func loopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
