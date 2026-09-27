package gitops

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var argoApplications = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

type KubeCluster struct {
	Dynamic dynamic.Interface
	Client  kubernetes.Interface
}

func (k KubeCluster) ArgoSynced(ctx context.Context, namespace, name, revision string) (bool, error) {
	app, err := k.Dynamic.Resource(argoApplications).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	sync := nested(app.Object, "status", "sync")
	return sync["status"] == "Synced" && sync["revision"] == revision, nil
}

func (k KubeCluster) DeploymentStopped(ctx context.Context, namespace, name string) (bool, error) {
	pods, err := k.pods(ctx, namespace, name)
	return err == nil && len(pods) == 0, err
}

func (k KubeCluster) DeploymentReady(ctx context.Context, namespace, name string, replicas int32) (bool, error) {
	pods, err := k.pods(ctx, namespace, name)
	if err != nil {
		return false, err
	}
	var ready int32
	for _, p := range pods {
		for _, c := range p.Status.Conditions {
			if p.DeletionTimestamp == nil && c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready++
			}
		}
	}
	return ready >= replicas, nil
}

func (k KubeCluster) pods(ctx context.Context, namespace, name string) ([]corev1.Pod, error) {
	d, err := k.Client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, err
	}
	list, err := k.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func nested(obj map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		obj, _ = obj[k].(map[string]any)
	}
	return obj
}
