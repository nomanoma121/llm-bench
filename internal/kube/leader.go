package kube

import (
	"context"
	"fmt"
	"os"
	"sync"
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
	// newElector is a test seam; nil builds the real elector.
	newElector func(leaderelection.LeaderElectionConfig) (elector, error)
}

// elector is the slice of leaderelection.LeaderElector RunWithLeadership uses.
type elector interface{ Run(ctx context.Context) }

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

	// Losing the lease must not end this process's participation in the
	// election: a transient API failure would otherwise leave the Pod
	// serving HTTP forever without ever dispatching again.
	for ctx.Err() == nil {
		if err := runOnce(ctx, cfg, lock, fn); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(cfg.RetryPeriod):
		}
	}
	return nil
}

func runOnce(ctx context.Context, cfg LeaderConfig, lock *resourcelock.LeaseLock, fn func(context.Context) error) error {
	leaderCtx, stopLeading := context.WithCancel(ctx)
	defer stopLeading()

	// Workers started by a leadership round must finish before a new round
	// begins or the lease is released. A round that never started leading
	// contributes nothing, so the registration is guarded by a flag that is
	// closed once the elector has returned.
	var (
		leadingMu sync.Mutex
		leading   sync.WaitGroup
		accepting = true
	)

	build := cfg.newElector
	if build == nil {
		build = func(lec leaderelection.LeaderElectionConfig) (elector, error) {
			return leaderelection.NewLeaderElector(lec)
		}
	}
	e, err := build(leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: cfg.LeaseDuration,
		RenewDeadline: cfg.RenewDeadline,
		RetryPeriod:   cfg.RetryPeriod,
		// ReleaseOnCancel stays false: the lease must expire instead of
		// being handed over while the previous leader's workers may still be
		// finishing effects. fn waits for its workers before returning.
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) {
				leadingMu.Lock()
				if !accepting {
					// The elector already returned and the next round may
					// start: a callback that arrives this late must not run
					// the leader function at all, otherwise two rounds would
					// execute effects concurrently.
					leadingMu.Unlock()
					return
				}
				leading.Add(1)
				leadingMu.Unlock()
				defer leading.Done()
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
	e.Run(leaderCtx)
	// The elector has returned: no further callbacks fire, so stop accepting
	// registrations and wait for the workers that did start.
	leadingMu.Lock()
	accepting = false
	leadingMu.Unlock()
	leading.Wait()
	return nil
}
