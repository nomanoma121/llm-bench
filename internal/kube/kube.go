// Package kube provides the cluster convergence checks the GitOps hook needs:
// Argo CD Application sync status and inference workload readiness. It also
// hosts the ConfigMap run store and leader election in a later change.
package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
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

// WorkloadStopped implements gitops.Kube: the deployment reports no ready
// replicas AND every pod selected by it has actually gone away. Zero ready
// replicas alone is not enough: terminating pods still hold the GPU.
func (c *Checker) WorkloadStopped(ctx context.Context, namespace, name string) (bool, error) {
	d, err := c.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("kube: get deployment %s/%s: %w", namespace, name, err)
	}
	if d.Status.ReadyReplicas != 0 {
		return false, nil
	}
	remaining, err := c.livePods(ctx, namespace, d.Spec.Selector)
	if err != nil {
		return false, err
	}
	return remaining == 0, nil
}

// WorkloadReady implements gitops.Kube: the deployment reports the wanted
// number of ready replicas and that many pods are ready.
func (c *Checker) WorkloadReady(ctx context.Context, namespace, name string, want int32) (bool, error) {
	d, err := c.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("kube: get deployment %s/%s: %w", namespace, name, err)
	}
	if d.Status.ReadyReplicas != want {
		return false, nil
	}
	ready, err := c.readyPods(ctx, namespace, d.Spec.Selector)
	if err != nil {
		return false, err
	}
	return ready >= int(want), nil
}

// livePods counts pods that still exist (not fully deleted) for a selector.
func (c *Checker) livePods(ctx context.Context, namespace string, selector *metav1.LabelSelector) (int, error) {
	pods, err := c.listPods(ctx, namespace, selector)
	if err != nil {
		return 0, err
	}
	return len(pods), nil
}

// readyPods counts pods that are running and ready (terminating pods do not
// count).
func (c *Checker) readyPods(ctx context.Context, namespace string, selector *metav1.LabelSelector) (int, error) {
	pods, err := c.listPods(ctx, namespace, selector)
	if err != nil {
		return 0, err
	}
	ready := 0
	for i := range pods {
		if pods[i].DeletionTimestamp != nil {
			continue
		}
		for _, cond := range pods[i].Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				ready++
				break
			}
		}
	}
	return ready, nil
}

func (c *Checker) listPods(ctx context.Context, namespace string, selector *metav1.LabelSelector) ([]corev1.Pod, error) {
	if selector == nil {
		return nil, nil
	}
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("kube: deployment selector: %w", err)
	}
	list, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return nil, fmt.Errorf("kube: list pods for %s: %w", sel, err)
	}
	return list.Items, nil
}
