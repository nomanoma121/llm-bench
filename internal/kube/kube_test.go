package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
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

func TestReadyReplicas(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "inference", Namespace: "inference"},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 2},
	}
	c := fakeChecker(t, nil, dep)
	n, err := c.ReadyReplicas(context.Background(), "inference", "inference")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("ready replicas = %d", n)
	}
}
