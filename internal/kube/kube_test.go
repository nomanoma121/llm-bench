package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func fakeChecker(t *testing.T, app map[string]any, objects ...runtime.Object) *Checker {
	t.Helper()
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{applicationsGVR: "ApplicationList"})
	if app != nil {
		u := &unstructured.Unstructured{Object: app}
		if _, err := dyn.Resource(applicationsGVR).Namespace("inference").Create(context.Background(), u, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	var filtered []runtime.Object
	for _, o := range objects {
		if o != nil {
			filtered = append(filtered, o)
		}
	}
	return &Checker{dynamic: dyn, client: k8sfake.NewSimpleClientset(filtered...)}
}

func argoApp(syncStatus, revision string) map[string]any {
	return map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "inference", "namespace": "inference"},
		"status": map[string]any{
			"sync": map[string]any{"status": syncStatus, "revision": revision},
		},
	}
}

func TestArgoSynced(t *testing.T) {
	c := fakeChecker(t, argoApp("Synced", "abc123"))
	ok, err := c.ArgoSynced(context.Background(), "inference", "inference", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected synced")
	}
	ok, err = c.ArgoSynced(context.Background(), "inference", "inference", "other")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("revision mismatch must not report synced")
	}
}

func TestArgoSyncedMissingApplication(t *testing.T) {
	c := fakeChecker(t, nil)
	ok, err := c.ArgoSynced(context.Background(), "inference", "inference", "abc123")
	if err != nil {
		t.Fatalf("missing application should report not-synced, not error: %v", err)
	}
	if ok {
		t.Fatal("missing application cannot be synced")
	}
}

func readyPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "inference", Labels: map[string]string{"app": "inference"}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func terminatingPod(name string) *corev1.Pod {
	p := readyPod(name)
	now := metav1.Now()
	p.DeletionTimestamp = &now
	p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
	return p
}

func workloadDeployment(ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "inference", Namespace: "inference"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "inference"}},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func TestWorkloadStoppedRequiresPodsGone(t *testing.T) {
	// Zero ready replicas but a terminating pod still holds the GPU.
	c := fakeChecker(t, nil, workloadDeployment(0), terminatingPod("inference-0"))
	stopped, err := c.WorkloadStopped(context.Background(), "inference", "inference")
	if err != nil {
		t.Fatal(err)
	}
	if stopped {
		t.Fatal("a terminating pod must not count as stopped")
	}
	// No pods: stopped.
	c2 := fakeChecker(t, nil, workloadDeployment(0))
	stopped, err = c2.WorkloadStopped(context.Background(), "inference", "inference")
	if err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("workload without pods must be stopped")
	}
}

func TestWorkloadReadyRequiresReadyPods(t *testing.T) {
	c := fakeChecker(t, nil, workloadDeployment(1), readyPod("inference-1"))
	ready, err := c.WorkloadReady(context.Background(), "inference", "inference", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("ready deployment with a ready pod must report ready")
	}
	c2 := fakeChecker(t, nil, workloadDeployment(1), terminatingPod("inference-2"))
	ready, err = c2.WorkloadReady(context.Background(), "inference", "inference", 1)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("a terminating pod must not count as ready")
	}
}
