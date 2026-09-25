// Package kube provides the cluster convergence checks the GitOps hook needs:
// Argo CD Application sync status and inference workload readiness. It also
// hosts the ConfigMap run store and leader election in a later change.
package kube

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// applicationsGVR is the Argo CD Application resource (single-source only).
var applicationsGVR = schema.GroupVersionResource{
	Group: "argoproj.io", Version: "v1alpha1", Resource: "applications",
}

// Checker implements gitops.Kube against a real cluster.
type Checker struct {
	dynamic dynamic.Interface
	client  kubernetes.Interface
}

// NewChecker builds a Checker. An empty kubeconfig loads the in-cluster
// config first and the default kubeconfig rules second.
func NewChecker(kubeconfig string) (*Checker, error) {
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
		return nil, fmt.Errorf("kube: config: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kube: dynamic client: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kube: clientset: %w", err)
	}
	return &Checker{dynamic: dyn, client: cs}, nil
}

// ArgoSynced implements gitops.Kube. A missing Application reports
// not-synced (the controller is waiting for Argo CD to create it).
func (c *Checker) ArgoSynced(ctx context.Context, namespace, name, revision string) (bool, error) {
	app, err := c.dynamic.Resource(applicationsGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil // missing application -> not synced
		}
		return false, fmt.Errorf("kube: get application %s/%s: %w", namespace, name, err)
	}
	status, ok := app.Object["status"].(map[string]any)
	if !ok {
		return false, nil
	}
	sync, ok := status["sync"].(map[string]any)
	if !ok {
		return false, nil
	}
	syncStatus, _ := sync["status"].(string)
	rev, _ := sync["revision"].(string)
	return syncStatus == "Synced" && (revision == "" || rev == revision), nil
}

// ReadyReplicas implements gitops.Kube.
func (c *Checker) ReadyReplicas(ctx context.Context, namespace, name string) (int32, error) {
	d, err := c.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("kube: get deployment %s/%s: %w", namespace, name, err)
	}
	return d.Status.ReadyReplicas, nil
}
