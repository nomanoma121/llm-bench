package agent

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type Harness struct {
	Client    kubernetes.Interface
	REST      *rest.Config
	Namespace string
	Selector  string
	Container string
	Command   []string
	CWD       string
	Stderr    io.Writer
}

func (h *Harness) Run(ctx context.Context, task string, onSession func(string)) (string, error) {
	rw, err := h.open(ctx)
	if err != nil {
		return "", err
	}
	return runSession(ctx, rw, h.CWD, task, onSession)
}

func (h *Harness) open(ctx context.Context) (io.ReadWriteCloser, error) {
	pods, err := h.Client.CoreV1().Pods(h.Namespace).List(ctx, metav1.ListOptions{LabelSelector: h.Selector})
	if err != nil {
		return nil, err
	}
	var pod string
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			pod = p.Name
			break
		}
	}
	if pod == "" {
		return nil, fmt.Errorf("harness: no running pod matches %q in %s", h.Selector, h.Namespace)
	}
	req := h.Client.CoreV1().RESTClient().Post().Resource("pods").Namespace(h.Namespace).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: h.Container, Command: h.Command, Stdin: true, Stdout: true, Stderr: h.Stderr != nil,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(h.REST, "POST", req.URL())
	if err != nil {
		return nil, err
	}
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdinR, Stdout: stdoutW, Stderr: h.Stderr})
		stdoutW.CloseWithError(err)
		stdinR.CloseWithError(err)
	}()
	return &stream{Reader: stdoutR, Writer: stdinW, close: func() error {
		cancel()
		stdinW.Close()
		return stdoutR.Close()
	}}, nil
}

type stream struct {
	io.Reader
	io.Writer
	close func() error
}

func (s *stream) Close() error { return s.close() }
