package kube

// Kubernetes-backed coordination: run records live in ConfigMaps whose
// resourceVersion doubles as the opaque StoreVersion, so the compare-and-swap
// contract of run.RunStore is enforced by the API server. Artifacts and
// recipe snapshots stay on the harness's persistent volume (filestore), not
// in ConfigMaps.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/nomanoma121/llm-bench/internal/run"
)

const (
	runIDLabel   = "llmbench/run-id"
	targetLabel  = "llmbench/target"
	runRecordKey = "run"
)

// RunStore implements run.RunStore on top of ConfigMaps.
type RunStore struct {
	client    kubernetes.Interface
	namespace string
}

// NewRunStore builds the store. client is injectable for tests/fakes.
func NewRunStore(client kubernetes.Interface, namespace string) *RunStore {
	return &RunStore{client: client, namespace: namespace}
}

func runConfigMapName(runID string) string { return "llmbench-run-" + runID }

// SaveRun implements run.RunStore. StoreVersion carries the ConfigMap
// resourceVersion; a mismatch is a conflict.
func (s *RunStore) SaveRun(ctx context.Context, r *run.Run) error {
	cms := s.client.CoreV1().ConfigMaps(s.namespace)
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("kube: encode run: %w", err)
	}
	if r.StoreVersion == "" {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      runConfigMapName(r.ID),
				Namespace: s.namespace,
				Labels:    map[string]string{runIDLabel: r.ID},
			},
			Data: map[string]string{runRecordKey: string(payload)},
		}
		created, err := cms.Create(ctx, cm, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("kube: %w: run %s already exists", run.ErrVersionConflict, r.ID)
		}
		if err != nil {
			return fmt.Errorf("kube: create run %s: %w", r.ID, err)
		}
		r.StoreVersion = created.ResourceVersion
		return nil
	}

	existing, err := cms.Get(ctx, runConfigMapName(r.ID), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("kube: %w: run %s no longer exists", run.ErrVersionConflict, r.ID)
	}
	if err != nil {
		return fmt.Errorf("kube: get run %s: %w", r.ID, err)
	}
	if existing.ResourceVersion != r.StoreVersion {
		return fmt.Errorf("kube: %w: run %s (have %q, sent %q)", run.ErrVersionConflict, r.ID, existing.ResourceVersion, r.StoreVersion)
	}
	next := existing.DeepCopy()
	if next.Data == nil {
		next.Data = map[string]string{}
	}
	next.Data[runRecordKey] = string(payload)
	updated, err := cms.Update(ctx, next, metav1.UpdateOptions{})
	if apierrors.IsConflict(err) {
		return fmt.Errorf("kube: %w: run %s", run.ErrVersionConflict, r.ID)
	}
	if err != nil {
		return fmt.Errorf("kube: update run %s: %w", r.ID, err)
	}
	r.StoreVersion = updated.ResourceVersion
	return nil
}

// LoadRun implements run.RunStore.
func (s *RunStore) LoadRun(ctx context.Context, id string) (run.Run, error) {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, runConfigMapName(id), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return run.Run{}, fmt.Errorf("kube: %w: %s", run.ErrNotFound, id)
	}
	if err != nil {
		return run.Run{}, fmt.Errorf("kube: get run %s: %w", id, err)
	}
	return decodeRun(cm)
}

// ListUnfinished implements run.RunStore.
func (s *RunStore) ListUnfinished(ctx context.Context) ([]run.Run, error) {
	list, err := s.client.CoreV1().ConfigMaps(s.namespace).List(ctx, metav1.ListOptions{LabelSelector: runIDLabel})
	if err != nil {
		return nil, fmt.Errorf("kube: list runs: %w", err)
	}
	out := make([]run.Run, 0, len(list.Items))
	for i := range list.Items {
		r, err := decodeRun(&list.Items[i])
		if err != nil {
			continue // a malformed record must not block the dispatcher
		}
		if !r.Phase.Terminal() {
			out = append(out, r)
		}
	}
	return out, nil
}

func decodeRun(cm *corev1.ConfigMap) (run.Run, error) {
	raw, ok := cm.Data[runRecordKey]
	if !ok {
		return run.Run{}, errors.New("kube: run ConfigMap has no record")
	}
	var r run.Run
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return run.Run{}, fmt.Errorf("kube: decode run: %w", err)
	}
	r.StoreVersion = cm.ResourceVersion
	return r, nil
}

// LeaseStore implements run.LeaseStore with one ConfigMap per target.
type LeaseStore struct {
	client    kubernetes.Interface
	namespace string
}

// NewLeaseStore builds the target lease store.
func NewLeaseStore(client kubernetes.Interface, namespace string) *LeaseStore {
	return &LeaseStore{client: client, namespace: namespace}
}

func leaseConfigMapName(target string) string { return "llmbench-lease-" + target }

// AcquireTargetLease implements run.LeaseStore. The lease is a ConfigMap
// whose "owner" holds the run ID; create-or-adopt is idempotent for the
// owning run and ErrLeaseBusy for any other.
func (s *LeaseStore) AcquireTargetLease(ctx context.Context, target, runID string) error {
	cms := s.client.CoreV1().ConfigMaps(s.namespace)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseConfigMapName(target),
			Namespace: s.namespace,
			Labels:    map[string]string{targetLabel: target},
		},
		Data: map[string]string{"owner": runID},
	}
	if _, err := cms.Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("kube: create lease %s: %w", target, err)
		}
		existing, err := cms.Get(ctx, leaseConfigMapName(target), metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("kube: get lease %s: %w", target, err)
		}
		if existing.Data["owner"] != runID {
			return fmt.Errorf("kube: %w: target %s is held by another run", run.ErrLeaseBusy, target)
		}
		return nil
	}
	return nil
}

// ReleaseTargetLease implements run.LeaseStore. It deletes only the lease
// owned by runID (UID precondition) so a stale retry can never free the next
// run's lease. A missing lease is success.
func (s *LeaseStore) ReleaseTargetLease(ctx context.Context, target, runID string) error {
	cms := s.client.CoreV1().ConfigMaps(s.namespace)
	existing, err := cms.Get(ctx, leaseConfigMapName(target), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kube: get lease %s: %w", target, err)
	}
	if existing.Data["owner"] != runID {
		return fmt.Errorf("kube: %w: target %s is held by another run", run.ErrLeaseBusy, target)
	}
	uid := existing.UID
	err = cms.Delete(ctx, leaseConfigMapName(target), metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid},
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if apierrors.IsConflict(err) {
		return fmt.Errorf("kube: %w: lease %s changed during release", run.ErrLeaseBusy, target)
	}
	if err != nil {
		return fmt.Errorf("kube: delete lease %s: %w", target, err)
	}
	return nil
}
