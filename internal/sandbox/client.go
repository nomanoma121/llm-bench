// Package sandbox wraps the Agent Sandbox Go client behind the small
// interface the runner needs: deterministic (idempotent) claim management,
// command execution, file transfer and release. It is the only package that
// imports the SDK.
package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"
	extv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

// ClaimName derives the deterministic SandboxClaim name for a run. A fresh
// controller process can therefore reattach to a claim without process
// memory. SDK-created claims use GenerateName, so the deterministic claim is
// created through the extensions clientset directly and attached to via the
// SDK's GetSandbox.
func ClaimName(runID string) string { return "llmbench-" + runID }

// runtimePIDFile is the in-sandbox pid file of the detached runtime; it
// doubles as the deterministic process handle.
func runtimePIDFile(runID string) string { return "/tmp/llmbench-runtime-" + runID + ".pid" }

// Client implements runner.SandboxClient on top of the Agent Sandbox SDK.
type Client struct {
	// Namespace holds the SandboxClaims.
	Namespace string
	// Kubeconfig optionally points at a cluster; empty means in-cluster
	// config with the default kubeconfig fallback.
	Kubeconfig string
	Log        logr.Logger
}

// EnsureSandboxClaim creates the deterministic SandboxClaim if absent.
// AlreadyExists is success after verifying the warm pool reference matches,
// which makes the operation idempotent across retries and restarts.
func (c *Client) EnsureSandboxClaim(ctx context.Context, runID, warmPool string) error {
	helper, err := c.k8sHelper()
	if err != nil {
		return err
	}
	name := ClaimName(runID)
	claim := &extv1beta1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "llmbench"},
		},
		Spec: extv1beta1.SandboxClaimSpec{
			WarmPoolRef: extv1beta1.SandboxWarmPoolRef{Name: warmPool},
		},
	}
	if _, err := helper.ExtensionsClient.SandboxClaims(c.Namespace).Create(ctx, claim, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("sandbox: create claim %s: %w", name, err)
		}
		stored, getErr := helper.ExtensionsClient.SandboxClaims(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("sandbox: get claim %s: %w", name, getErr)
		}
		if stored.Spec.WarmPoolRef.Name != warmPool {
			return fmt.Errorf("sandbox: claim %s exists with warm pool %q, want %q", name, stored.Spec.WarmPoolRef.Name, warmPool)
		}
	}
	return nil
}

// ReleaseSandboxClaim deletes the claim (pod and workspace go with it).
// Artifacts must have been pulled beforehand. A missing claim is success.
func (c *Client) ReleaseSandboxClaim(ctx context.Context, runID string) error {
	helper, err := c.k8sHelper()
	if err != nil {
		return err
	}
	if err := helper.ExtensionsClient.SandboxClaims(c.Namespace).Delete(ctx, ClaimName(runID), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("sandbox: release claim %s: %w", ClaimName(runID), err)
	}
	return nil
}

// sandboxHandle attaches to a claim and returns the SDK handle with
// exec/file access over the port-forward transport.
func (c *Client) sandboxHandle(ctx context.Context, claim string) (*sandboxsdk.Sandbox, error) {
	client, err := sandboxsdk.NewClient(ctx, sandboxsdk.Options{
		Namespace:  c.Namespace,
		Runtime:    sandboxsdk.RuntimeSandboxd,
		RestConfig: c.restConfig(),
	})
	if err != nil {
		return nil, fmt.Errorf("sandbox: client: %w", err)
	}
	sb, err := client.GetSandbox(ctx, claim, c.Namespace)
	if err != nil {
		return nil, fmt.Errorf("sandbox: attach to claim %s: %w", claim, err)
	}
	return sb, nil
}

// Start launches the runtime detached inside the sandbox and records its pid
// at a deterministic in-sandbox path (the returned handle). Starting while a
// recorded pid is alive is a no-op, so retries never double-start a runtime.
func (c *Client) Start(ctx context.Context, runID string, argv []string, env map[string]string, cwd string) (string, error) {
	handle := runtimePIDFile(runID)
	sb, err := c.sandboxHandle(ctx, ClaimName(runID))
	if err != nil {
		return "", err
	}
	res, err := sb.Run(ctx, fmt.Sprintf("pid=$(cat %s 2>/dev/null); if [ -n \"$pid\" ] && kill -0 \"$pid\" 2>/dev/null; then echo alive; else echo dead; fi", quote(handle)))
	if err != nil {
		return "", fmt.Errorf("sandbox: probe runtime: %w", err)
	}
	if strings.TrimSpace(res.Stdout) == "alive" {
		return handle, nil
	}
	res, err = sb.Run(ctx, backgroundScript(argv, env, cwd, handle))
	if err != nil {
		return "", fmt.Errorf("sandbox: start runtime: %w: %s%s", err, res.Stdout, res.Stderr)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("sandbox: start runtime: exit %d: %s%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	return handle, nil
}

// Stop terminates the recorded runtime. Idempotent.
func (c *Client) Stop(ctx context.Context, runID, handle string) error {
	sb, err := c.sandboxHandle(ctx, ClaimName(runID))
	if err != nil {
		return err
	}
	res, err := sb.Run(ctx, fmt.Sprintf("pid=$(cat %s 2>/dev/null); if [ -n \"$pid\" ]; then kill \"$pid\" 2>/dev/null; fi; rm -f %s; true", quote(handle), quote(handle)))
	if err != nil || res.ExitCode != 0 {
		return fmt.Errorf("sandbox: stop runtime: %v: %s%s", err, res.Stdout, res.Stderr)
	}
	return nil
}

// Exec runs argv inside the sandbox and returns stdout/stderr and the exit
// code. sandboxd only accepts a shell string, so argv is quoted here at the
// boundary; results are never retried automatically because commands have
// side effects.
func (c *Client) Exec(ctx context.Context, runID string, argv []string, env map[string]string, cwd string) ([]byte, []byte, int, error) {
	sb, err := c.sandboxHandle(ctx, ClaimName(runID))
	if err != nil {
		return nil, nil, -1, err
	}
	res, err := sb.Run(ctx, execScript(argv, env, cwd))
	if err != nil {
		return nil, nil, -1, fmt.Errorf("sandbox: exec: %w", err)
	}
	return []byte(res.Stdout), []byte(res.Stderr), res.ExitCode, nil
}

// Put streams r into dest inside the sandbox.
func (c *Client) Put(ctx context.Context, runID string, r io.Reader, dest string) error {
	sb, err := c.sandboxHandle(ctx, ClaimName(runID))
	if err != nil {
		return err
	}
	if err := sb.WriteReader(ctx, dest, bufio.NewReader(r)); err != nil {
		return fmt.Errorf("sandbox: put %s: %w", dest, err)
	}
	return nil
}

// Pull reads a file from the sandbox.
func (c *Client) Pull(ctx context.Context, runID, path string) ([]byte, error) {
	sb, err := c.sandboxHandle(ctx, ClaimName(runID))
	if err != nil {
		return nil, err
	}
	b, err := sb.Read(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("sandbox: pull %s: %w", path, err)
	}
	return b, nil
}

// ExecClaim runs a raw shell command on the claim (manual CLI operations).
func (c *Client) ExecClaim(ctx context.Context, claim, command string) (string, error) {
	sb, err := c.sandboxHandle(ctx, claim)
	if err != nil {
		return "", err
	}
	res, err := sb.Run(ctx, command)
	if err != nil {
		return "", fmt.Errorf("sandbox: exec: %w", err)
	}
	return strings.TrimSpace(res.Stdout) + res.Stderr, nil
}

// PullClaim reads a file from the claim (manual CLI operations).
func (c *Client) PullClaim(ctx context.Context, claim, path string) ([]byte, error) {
	sb, err := c.sandboxHandle(ctx, claim)
	if err != nil {
		return nil, err
	}
	return sb.Read(ctx, path)
}

func (c *Client) k8sHelper() (*sandboxsdk.K8sHelper, error) {
	return sandboxsdk.NewK8sHelper(c.restConfig(), c.logger())
}

func (c *Client) restConfig() *rest.Config {
	if c.Kubeconfig == "" {
		return nil // SDK: in-cluster config, then default kubeconfig
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", c.Kubeconfig)
	if err != nil {
		return nil
	}
	return cfg
}

func (c *Client) logger() logr.Logger {
	if c.Log.GetSink() == nil {
		return logr.Discard()
	}
	return c.Log
}

// quote renders one POSIX shell argument.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// quoteArgv renders a full argv as a shell command line.
func quoteArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = quote(a)
	}
	return strings.Join(parts, " ")
}

// execScript builds the sh payload for one argv invocation with env and cwd.
// This is the only place in the controller where shell syntax is produced.
func execScript(argv []string, env map[string]string, cwd string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for k, v := range env {
		fmt.Fprintf(&b, "export %s=%s\n", shellSafeName(k), quote(v))
	}
	if cwd != "" && cwd != "/" {
		fmt.Fprintf(&b, "cd %s\n", quote(cwd))
	}
	b.WriteString(quoteArgv(argv))
	b.WriteString("\n")
	return b.String()
}

// backgroundScript detaches the runtime, records its pid and never blocks.
func backgroundScript(argv []string, env map[string]string, cwd, pidFile string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for k, v := range env {
		fmt.Fprintf(&b, "export %s=%s\n", shellSafeName(k), quote(v))
	}
	fmt.Fprintf(&b, "cd %s\n", quote(cwd))
	fmt.Fprintf(&b, "rm -f %s\n", quote(pidFile))
	fmt.Fprintf(&b, "nohup %s > /tmp/llmbench-runtime.log 2>&1 &\n", quoteArgv(argv))
	fmt.Fprintf(&b, "echo $! > %s\n", quote(pidFile))
	return b.String()
}

// shellSafeName keeps only characters valid in a POSIX variable name.
func shellSafeName(name string) string {
	var b strings.Builder
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
