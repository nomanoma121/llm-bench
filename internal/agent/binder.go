// Package agent connects a job to the harness that will optimize it.
//
// The harness (DeepSeek Harness) runs as its own long-lived Deployment: the
// controller does not start it, does not own it, and does not shell into a
// one-shot command. What the controller does is create one session on it and
// hand over the task; after that the session belongs to the harness and the
// controller only waits for it to settle (docs/mvp.md §8).
package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/nomanoma121/llm-bench/internal/acp"
)

// Transport opens the ACP stream to the harness. In production this is a
// `pods/exec` connection into the harness Pod; tests use a pipe.
type Transport interface {
	Open(ctx context.Context) (io.ReadWriteCloser, error)
}

// Request is one job's session.
type Request struct {
	// JobID identifies the work; it is included in the task so the harness can
	// tell which sandbox and which request it is working on.
	JobID string
	// Task is the text submitted as the first (and, for the MVP, only) turn.
	Task string
	// SessionID continues an existing session instead of creating one. Empty
	// means a fresh session.
	SessionID string
}

// Result is what the harness reported when the turn settled.
type Result struct {
	SessionID  string
	StopReason string
}

// Binder is the controller's view of the harness.
type Binder interface {
	// Bind creates (or resumes) the session, submits the task, and returns
	// once the turn settles. It answers the harness's permission requests with
	// the configured policy and forwards updates to the log.
	Bind(ctx context.Context, req Request) (Result, error)
}

// ACPBinder is the production Binder: it speaks ACP to the harness Pod.
type ACPBinder struct {
	Transport Transport
	// CWD is the harness-side working directory of the session. It is a path
	// on the harness Pod's filesystem; the GPU sandbox is a different Pod and
	// is reached through the llmbench CLI.
	CWD string
	// AllowTools answers the harness's permission requests. Every request is
	// allowed by default: the session is the operator's own agent, and what it
	// can touch is bounded by the sandbox and the RBAC, not by prompts.
	AllowTools bool
	// Logf receives the session updates (tool calls, messages) and lifecycle
	// lines.
	Logf func(format string, args ...any)
}

func (b *ACPBinder) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
	}
}

// Bind implements Binder.
func (b *ACPBinder) Bind(ctx context.Context, req Request) (Result, error) {
	if b.Transport == nil {
		return Result{}, fmt.Errorf("agent: no transport is configured for the harness")
	}
	conn, err := b.Transport.Open(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("agent: connect to the harness: %w", err)
	}
	client := acp.New(conn, acp.Options{
		OnUpdate: func(u acp.Update) {
			b.logf("session %s: %s", u.SessionID, u.Kind())
		},
		OnPermission: func(p acp.PermissionRequest) (string, bool) {
			if !b.AllowTools {
				return "", false
			}
			optionID, ok := p.Allow()
			if ok {
				b.logf("allowing tool request in session %s", p.SessionID)
			}
			return optionID, ok
		},
	})
	go client.Run()
	defer client.Close()

	if err := client.Initialize(ctx); err != nil {
		return Result{}, err
	}
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID, err = client.NewSession(ctx, b.CWD)
		if err != nil {
			return Result{}, err
		}
		b.logf("created harness session %s for job %s", sessionID, req.JobID)
	} else {
		if err := client.ResumeSession(ctx, sessionID, b.CWD); err != nil {
			return Result{}, err
		}
		b.logf("resumed harness session %s for job %s", sessionID, req.JobID)
	}
	stop, err := client.Prompt(ctx, sessionID, req.Task)
	if err != nil {
		return Result{}, err
	}
	// The session is flushed before the controller reads the completion record
	// from the sandbox: the harness writes that record as its last action.
	if err := client.CloseSession(ctx, sessionID); err != nil {
		b.logf("closing harness session %s: %v", sessionID, err)
	}
	return Result{SessionID: sessionID, StopReason: stop}, nil
}
