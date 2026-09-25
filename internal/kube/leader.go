package kube

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// LeaderConfig configures leader election for the dispatcher.
type LeaderConfig struct {
	Client        kubernetes.Interface
	Namespace     string
	LeaseName     string
	Identity      string // defaults to hostname-pid
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
	Log           logr.Logger
}

// RunWithLeadership runs fn while this process holds the lease. When
// leadership is lost the context passed to fn is cancelled, so the
// dispatcher and its workers stop making external effects. RunWithLeadership
// returns when ctx is cancelled or leadership is lost.
func RunWithLeadership(ctx context.Context, cfg LeaderConfig, fn func(context.Context) error) error {
	identity := cfg.Identity
	if identity == "" {
		host, _ := os.Hostname()
		identity = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	if cfg.LeaseDuration == 0 {
		cfg.LeaseDuration = 15 * time.Second
	}
	if cfg.RenewDeadline == 0 {
		cfg.RenewDeadline = 10 * time.Second
	}
	if cfg.RetryPeriod == 0 {
		cfg.RetryPeriod = 2 * time.Second
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{Name: cfg.LeaseName, Namespace: cfg.Namespace},
		Client:    cfg.Client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	}

	leaderCtx, stopLeading := context.WithCancel(ctx)
	defer stopLeading()

	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: cfg.LeaseDuration,
		RenewDeadline: cfg.RenewDeadline,
		RetryPeriod:   cfg.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) {
				if err := fn(leaderCtx); err != nil && cfg.Log.GetSink() != nil {
					cfg.Log.Error(err, "leader function returned")
				}
			},
			OnStoppedLeading: func() {
				// Cancelling the leader context stops every worker this
				// process started; its external effects must not continue.
				stopLeading()
			},
		},
	})
	if err != nil {
		return fmt.Errorf("kube: leader elector: %w", err)
	}
	elector.Run(leaderCtx)
	return nil
}
