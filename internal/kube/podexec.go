package kube

import (
	"context"
	"fmt"
	"io"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// PodExec opens a duplex stream to a command in a Pod. The harness speaks ACP
// over stdio, so a stream like this is the only transport it needs; there is
// no harness HTTP API to call instead.
type PodExec struct {
	Client    kubernetes.Interface
	REST      *rest.Config
	Namespace string
	// Selector finds the harness Pod. The deployment is long-lived, so the
	// controller resolves it by label rather than by Pod name.
	Selector string
	// Container is optional when the Pod has one container.
	Container string
	// Command is the argv started in the Pod, for example
	// ["dsh", "--profile", "acp"].
	Command []string
	// Stderr, when set, receives the command's stderr (diagnostics).
	Stderr io.Writer
}

// Open finds the harness Pod and attaches to it.
func (p *PodExec) Open(ctx context.Context) (io.ReadWriteCloser, error) {
	if p.Client == nil || p.REST == nil {
		return nil, fmt.Errorf("kube: pod exec needs a client and a rest config")
	}
	if p.Selector == "" {
		return nil, fmt.Errorf("kube: pod exec needs a label selector for the harness Pod")
	}
	pods, err := p.Client.CoreV1().Pods(p.Namespace).List(ctx, metav1.ListOptions{LabelSelector: p.Selector})
	if err != nil {
		return nil, fmt.Errorf("kube: list harness pods: %w", err)
	}
	var pod *corev1.Pod
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodRunning {
			pod = &pods.Items[i]
			break
		}
	}
	if pod == nil {
		return nil, fmt.Errorf("kube: no running harness Pod matches %q in %s", p.Selector, p.Namespace)
	}
	req := p.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(p.Namespace).
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: p.Container,
			Command:   p.Command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    p.Stderr != nil,
			TTY:       false,
		}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(p.REST, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("kube: exec into %s: %w", pod.Name, err)
	}
	return newExecStream(ctx, executor, p.Stderr), nil
}

// execStream adapts the exec's two unidirectional pipes into one duplex stream.
type execStream struct {
	reader   *io.PipeReader
	writer   *io.PipeWriter
	outClose *io.PipeWriter
	once     sync.Once
	cancel   context.CancelFunc
}

func newExecStream(parent context.Context, executor remotecommand.Executor, stderr io.Writer) *execStream {
	// Writes go to the Pod's stdin; reads come from its stdout.
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(parent)
	stream := &execStream{reader: outR, writer: inW, outClose: outW, cancel: cancel}
	go func() {
		opts := remotecommand.StreamOptions{Stdin: inR, Stdout: outW, Stderr: stderr}
		err := executor.StreamWithContext(ctx, opts)
		if err != nil {
			// Propagate the failure to both directions so a caller blocked on
			// a read or a write learns about it.
			_ = outW.CloseWithError(err)
			_ = inW.CloseWithError(err)
			return
		}
		_ = outW.Close()
		_ = inW.Close()
	}()
	return stream
}

func (s *execStream) Read(p []byte) (int, error)  { return s.reader.Read(p) }
func (s *execStream) Write(p []byte) (int, error) { return s.writer.Write(p) }

func (s *execStream) Close() error {
	s.once.Do(func() {
		s.cancel()
		_ = s.writer.Close()
		_ = s.reader.Close()
	})
	return nil
}
