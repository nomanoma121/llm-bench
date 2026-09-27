package sandbox

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	sdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"
	claimsv1beta1 "sigs.k8s.io/agent-sandbox/clients/k8s/extensions/clientset/versioned/typed/api/v1beta1"
	extv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

type Client struct {
	Namespace string
	WarmPool  string
	REST      *rest.Config
}

func Name(jobID string) string { return "llmbench-" + strings.ToLower(jobID) }

func (c *Client) Ensure(ctx context.Context, jobID string) error {
	claims, err := c.claims()
	if err != nil {
		return err
	}
	name := Name(jobID)
	_, err = claims.Create(ctx, &extv1beta1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app.kubernetes.io/managed-by": "llmbench"}},
		Spec:       extv1beta1.SandboxClaimSpec{WarmPoolRef: extv1beta1.SandboxWarmPoolRef{Name: c.WarmPool}},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return poll(ctx, func() (bool, error) {
		claim, err := claims.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range claim.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == metav1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
}

func (c *Client) Delete(ctx context.Context, jobID string) error {
	claims, err := c.claims()
	if err != nil {
		return err
	}
	name := Name(jobID)
	foreground := metav1.DeletePropagationForeground
	err = claims.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &foreground})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return poll(ctx, func() (bool, error) {
		_, err := claims.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}

type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (c *Client) Exec(ctx context.Context, jobID string, argv []string, env map[string]string) (Output, error) {
	sb, err := c.attach(ctx, jobID)
	if err != nil {
		return Output{}, err
	}
	var script strings.Builder
	for k, v := range env {
		fmt.Fprintf(&script, "export %s=%s\n", k, quote(v))
	}
	for i, a := range argv {
		if i > 0 {
			script.WriteByte(' ')
		}
		script.WriteString(quote(a))
	}
	res, err := sb.Run(ctx, script.String())
	if err != nil {
		return Output{}, err
	}
	return Output{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}, nil
}

func (c *Client) Put(ctx context.Context, jobID, path string, r io.Reader) error {
	sb, err := c.attach(ctx, jobID)
	if err != nil {
		return err
	}
	return sb.WriteReader(ctx, path, r)
}

func (c *Client) Get(ctx context.Context, jobID, path string) ([]byte, error) {
	sb, err := c.attach(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return sb.Read(ctx, path)
}

func (c *Client) attach(ctx context.Context, jobID string) (*sdk.Sandbox, error) {
	client, err := sdk.NewClient(ctx, sdk.Options{Namespace: c.Namespace, Runtime: sdk.RuntimeSandboxd, RestConfig: c.REST})
	if err != nil {
		return nil, err
	}
	return client.GetSandbox(ctx, Name(jobID), c.Namespace)
}

func (c *Client) claims() (claimsv1beta1.SandboxClaimInterface, error) {
	helper, err := sdk.NewK8sHelper(c.REST, logr.Discard())
	if err != nil {
		return nil, err
	}
	return helper.ExtensionsClient.SandboxClaims(c.Namespace), nil
}

func poll(ctx context.Context, done func() (bool, error)) error {
	for {
		ok, err := done()
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
