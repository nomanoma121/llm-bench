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
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"
	extensionsclient "sigs.k8s.io/agent-sandbox/clients/k8s/extensions/clientset/versioned/typed/api/v1beta1"
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

// convergenceTimeout bounds how long Acquire/Release wait for a claim
// transition before reporting run.ErrPending (the engine retries later).
const convergenceTimeout = 15 * time.Second

// Client implements runner.SandboxClient on top of the Agent Sandbox SDK.
type Client struct {
	// Namespace holds the SandboxClaims.
	Namespace string
	// Kubeconfig optionally points at a cluster; empty means in-cluster
	// config with the default kubeconfig fallback. An explicit path that
	// cannot be loaded is an error: silently connecting to another cluster
	// would release the wrong claim.
	Kubeconfig string
	Log        logr.Logger
	// Extensions optionally overrides the claim clientset (tests and
	// pre-built clients). When nil it is built from Kubeconfig.
	Extensions extensionsclient.ExtensionsV1beta1Interface
	// ConvergenceTimeout overrides how long Acquire/Release wait for a claim
	// transition before reporting run.ErrPending. Defaults to
	// convergenceTimeout.
	ConvergenceTimeout time.Duration
}

func (c *Client) converge() time.Duration {
	if c.ConvergenceTimeout > 0 {
		return c.ConvergenceTimeout
	}
	return convergenceTimeout
}

// claims resolves the SandboxClaim client, honouring an injected clientset.
func (c *Client) claims() (extensionsclient.SandboxClaimInterface, error) {
	if c.Extensions != nil {
		return c.Extensions.SandboxClaims(c.Namespace), nil
	}
	helper, err := c.k8sHelper()
	if err != nil {
		return nil, err
	}
	return helper.ExtensionsClient.SandboxClaims(c.Namespace), nil
}

// EnsureSandboxClaim creates the deterministic SandboxClaim if absent and
// waits until it reports Ready. AlreadyExists is success after verifying the
// warm pool reference matches. ready=false means "not yet": the caller
// (runner) converts that into run.ErrPending so the engine retries the hook
// instead of entering the non-repeatable execution phase. Keeping the
// pending decision out of this package preserves the dependency direction
// (adapter packages do not import run).
func (c *Client) EnsureSandboxClaim(ctx context.Context, claimName, warmPool string) (bool, error) {
	claims, err := c.claims()
	if err != nil {
		return false, err
	}
	claim := &extv1beta1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: c.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "llmbench"},
		},
		Spec: extv1beta1.SandboxClaimSpec{
			WarmPoolRef: extv1beta1.SandboxWarmPoolRef{Name: warmPool},
		},
	}
	if _, err := claims.Create(ctx, claim, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return false, fmt.Errorf("sandbox: create claim %s: %w", claimName, err)
		}
		stored, getErr := claims.Get(ctx, claimName, metav1.GetOptions{})
		if getErr != nil {
			return false, fmt.Errorf("sandbox: get claim %s: %w", claimName, getErr)
		}
		if stored.Spec.WarmPoolRef.Name != warmPool {
			return false, fmt.Errorf("sandbox: claim %s exists with warm pool %q, want %q", claimName, stored.Spec.WarmPoolRef.Name, warmPool)
		}
	}
	return c.waitClaimReady(ctx, claimName)
}

// ReleaseSandboxClaim deletes the claim with foreground propagation and waits
// until it is gone. Foreground deletion makes the claim's disappearance the
// completion point of the Sandbox/Pod cascade: the controller ties the
// Sandbox to the claim, so a plain (background) DELETE could remove the claim
// first and let the GitOps restore start while the GPU is still held.
// released=false means "still terminating": the caller retries.
func (c *Client) ReleaseSandboxClaim(ctx context.Context, claimName string) (bool, error) {
	claims, err := c.claims()
	if err != nil {
		return false, err
	}
	policy := metav1.DeletePropagationForeground
	if err := claims.Delete(ctx, claimName, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("sandbox: release claim %s: %w", claimName, err)
	}
	deadline := time.Now().Add(c.converge())
	for {
		_, err := claims.Get(ctx, claimName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil // claim, sandbox and pod are gone
		}
		if err != nil {
			return false, fmt.Errorf("sandbox: get claim %s: %w", claimName, err)
		}
		if time.Now().After(deadline) {
			return false, nil // still terminating: pending, not an error
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// waitClaimReady polls the claim's Ready condition up to convergenceTimeout.
func (c *Client) waitClaimReady(ctx context.Context, claimName string) (bool, error) {
	claims, err := c.claims()
	if err != nil {
		return false, err
	}
	deadline := time.Now().Add(c.converge())
	for {
		claim, err := claims.Get(ctx, claimName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("sandbox: get claim %s: %w", claimName, err)
		}
		for _, cond := range claim.Status.Conditions {
			if cond.Type == "Ready" {
				if cond.Status == metav1.ConditionTrue {
					return true, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// sandboxHandle attaches to a claim and returns the SDK handle with
// exec/file access over the port-forward transport.
func (c *Client) sandboxHandle(ctx context.Context, claim string) (*sandboxsdk.Sandbox, error) {
	restCfg, err := c.restConfig()
	if err != nil {
		return nil, err
	}
	client, err := sandboxsdk.NewClient(ctx, sandboxsdk.Options{
		Namespace:  c.Namespace,
		Runtime:    sandboxsdk.RuntimeSandboxd,
		RestConfig: restCfg,
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
func (c *Client) Start(ctx context.Context, runID, claim string, argv []string, env map[string]string, cwd string) (string, error) {
	handle := runtimePIDFile(runID)
	sb, err := c.sandboxHandle(ctx, claim)
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
	script, err := backgroundScript(argv, env, cwd, handle)
	if err != nil {
		return "", err
	}
	res, err = sb.Run(ctx, script)
	if err != nil {
		return "", fmt.Errorf("sandbox: start runtime: %w: %s%s", err, res.Stdout, res.Stderr)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("sandbox: start runtime: exit %d: %s%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	return handle, nil
}

// Stop terminates the recorded runtime. Idempotent.
func (c *Client) Stop(ctx context.Context, claim, handle string) error {
	sb, err := c.sandboxHandle(ctx, claim)
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
// boundary; nothing else in the controller deals in shell syntax. Results are
// never retried automatically because commands have side effects.
func (c *Client) Exec(ctx context.Context, claim string, argv []string, env map[string]string, cwd string) ([]byte, []byte, int, error) {
	sb, err := c.sandboxHandle(ctx, claim)
	if err != nil {
		return nil, nil, -1, err
	}
	script, err := execScript(argv, env, cwd)
	if err != nil {
		return nil, nil, -1, err
	}
	res, err := sb.Run(ctx, script)
	if err != nil {
		return nil, nil, -1, fmt.Errorf("sandbox: exec: %w", err)
	}
	return []byte(res.Stdout), []byte(res.Stderr), res.ExitCode, nil
}

// Put streams r into dest inside the sandbox.
func (c *Client) Put(ctx context.Context, claim string, r io.Reader, dest string) error {
	sb, err := c.sandboxHandle(ctx, claim)
	if err != nil {
		return err
	}
	if err := sb.WriteReader(ctx, dest, bufio.NewReader(r)); err != nil {
		return fmt.Errorf("sandbox: put %s: %w", dest, err)
	}
	return nil
}

// Pull reads a file from the sandbox.
func (c *Client) Pull(ctx context.Context, claim, path string) ([]byte, error) {
	sb, err := c.sandboxHandle(ctx, claim)
	if err != nil {
		return nil, err
	}
	b, err := sb.Read(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("sandbox: pull %s: %w", path, err)
	}
	return b, nil
}

func (c *Client) k8sHelper() (*sandboxsdk.K8sHelper, error) {
	restCfg, err := c.restConfig()
	if err != nil {
		return nil, err
	}
	return sandboxsdk.NewK8sHelper(restCfg, c.logger())
}

// restConfig resolves the cluster configuration. An explicitly requested
// kubeconfig must load or the caller fails; only the default path falls back.
func (c *Client) restConfig() (*rest.Config, error) {
	if c.Kubeconfig == "" {
		return nil, nil // SDK: in-cluster config, then default kubeconfig
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", c.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("sandbox: load kubeconfig %s: %w", c.Kubeconfig, err)
	}
	return cfg, nil
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
func execScript(argv []string, env map[string]string, cwd string) (string, error) {
	exports, err := exportLines(env)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, line := range exports {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if cwd != "" && cwd != "/" {
		fmt.Fprintf(&b, "cd %s\n", quote(cwd))
	}
	b.WriteString(quoteArgv(argv))
	b.WriteString("\n")
	return b.String(), nil
}

// backgroundScript detaches the runtime, records its pid and never blocks.
func backgroundScript(argv []string, env map[string]string, cwd, pidFile string) (string, error) {
	exports, err := exportLines(env)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, line := range exports {
		b.WriteString(line)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "cd %s\n", quote(cwd))
	fmt.Fprintf(&b, "rm -f %s\n", quote(pidFile))
	fmt.Fprintf(&b, "nohup %s > /tmp/llmbench-runtime.log 2>&1 &\n", quoteArgv(argv))
	fmt.Fprintf(&b, "echo $! > %s\n", quote(pidFile))
	return b.String(), nil
}

// exportLines renders environment assignments, rejecting names that are not
// valid POSIX identifiers instead of silently rewriting them.
func exportLines(env map[string]string) ([]string, error) {
	lines := make([]string, 0, len(env))
	for name, value := range env {
		if !validEnvName(name) {
			return nil, fmt.Errorf("sandbox: %q is not a valid environment variable name", name)
		}
		lines = append(lines, fmt.Sprintf("export %s=%s", name, quote(value)))
	}
	return lines, nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
