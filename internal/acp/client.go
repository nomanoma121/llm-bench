// Package acp speaks the Agent Client Protocol to a harness (DeepSeek Harness
// runs one with `dsh --profile acp`). The controller is the client: it creates
// one session per job, hands the task over, and then stays out of the way
// except for answering permission requests, until the turn settles.
//
// The transport is newline-delimited JSON-RPC 2.0 over any duplex stream; in
// production that stream is a `pods/exec` connection into the harness Pod,
// which is why nothing here knows about Kubernetes.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ProtocolVersion is the ACP version this client speaks.
const ProtocolVersion = 1

// Method names of the ACP v1 surface the controller uses.
const (
	methodInitialize    = "initialize"
	methodNewSession    = "session/new"
	methodListSessions  = "session/list"
	methodResumeSession = "session/resume"
	methodCloseSession  = "session/close"
	methodPrompt        = "session/prompt"
	methodCancel        = "session/cancel"
	methodUpdate        = "session/update"
	methodRequestPermit = "session/request_permission"
)

// Update is one `session/update` notification. Only the fields the controller
// records are decoded; the harness may send more.
type Update struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// Kind reports the update's discriminator, for logging.
func (u Update) Kind() string {
	var probe struct {
		Kind string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(u.Update, &probe); err != nil {
		return ""
	}
	return probe.Kind
}

// PermissionRequest is the harness asking whether it may run a tool.
type PermissionRequest struct {
	SessionID string          `json:"sessionId"`
	ToolCall  json.RawMessage `json:"toolCall"`
	Options   []struct {
		OptionID string `json:"optionId"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

// Allow returns the option id that grants the request, preferring a one-shot
// allow over a permanent one so the policy stays visible in the logs.
func (r PermissionRequest) Allow() (string, bool) {
	for _, kind := range []string{"allow_once", "allow_always"} {
		for _, o := range r.Options {
			if o.Kind == kind {
				return o.OptionID, true
			}
		}
	}
	return "", false
}

// Options configure a client.
type Options struct {
	// OnUpdate receives every session update. Optional.
	OnUpdate func(Update)
	// OnPermission answers a permission request. A nil handler refuses every
	// request, which is the safe default if a caller forgets to set it.
	OnPermission func(PermissionRequest) (optionID string, allowed bool)
}

// Client is one ACP connection.
type Client struct {
	conn io.ReadWriteCloser
	r    *bufio.Reader
	opts Options

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan response
	readErr error
	runOnce sync.Once
	done    chan struct{}
}

type response struct {
	result json.RawMessage
	err    error
}

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("acp: %d: %s", e.Code, e.Message) }

// New wraps a duplex stream.
func New(conn io.ReadWriteCloser, opts Options) *Client {
	return &Client{
		conn:    conn,
		r:       bufio.NewReaderSize(conn, 1<<20),
		opts:    opts,
		pending: map[int64]chan response{},
		done:    make(chan struct{}),
	}
}

// Run reads frames until the stream ends. It must be called once, and calls
// after the first are no-ops.
func (c *Client) Run() {
	c.runOnce.Do(func() {
		defer close(c.done)
		for {
			line, err := c.r.ReadBytes('\n')
			if len(line) > 0 {
				c.dispatch(line)
			}
			if err != nil {
				c.failAll(err)
				return
			}
		}
	})
}

// dispatch handles one frame: a response to a request we sent, a notification
// from the harness, or a request the harness makes of us.
func (c *Client) dispatch(line []byte) {
	var f frame
	if err := json.Unmarshal(line, &f); err != nil {
		// A frame we cannot parse is the harness's problem; the connection
		// stays usable and the caller sees the failure on its next request or
		// when the stream ends.
		return
	}
	switch {
	case f.Method == "" && f.ID != nil:
		c.resolve(*f.ID, f)
	case f.Method != "" && f.ID != nil:
		c.handleServerRequest(*f.ID, f)
	case f.Method == methodUpdate:
		if c.opts.OnUpdate != nil {
			var u Update
			if err := json.Unmarshal(f.Params, &u); err == nil {
				c.opts.OnUpdate(u)
			}
		}
	}
}

func (c *Client) resolve(id int64, f frame) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if !ok {
		return
	}
	if f.Error != nil {
		ch <- response{err: f.Error}
		return
	}
	ch <- response{result: f.Result}
}

// handleServerRequest answers the harness's requests. The only one the
// controller acts on is the permission prompt: the policy decides, and every
// other request is refused rather than guessed at.
func (c *Client) handleServerRequest(id int64, f frame) {
	switch f.Method {
	case methodRequestPermit:
		var req PermissionRequest
		if err := json.Unmarshal(f.Params, &req); err != nil {
			c.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "error": &rpcError{Code: -32602, Message: "invalid permission request"}})
			return
		}
		optionID, allowed := "", false
		if c.opts.OnPermission != nil {
			optionID, allowed = c.opts.OnPermission(req)
		}
		if !allowed {
			c.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"outcome": map[string]any{"outcome": "cancelled"},
			}})
			return
		}
		c.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
			"outcome": map[string]any{"outcome": "selected", "optionId": optionID},
		}})
	default:
		c.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "error": &rpcError{Code: -32601, Message: "unsupported method " + f.Method}})
	}
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- response{err: fmt.Errorf("acp: connection closed: %w", err)}
	}
	c.mu.Unlock()
}

// call sends a request and waits for its response.
func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	if c.readErr != nil {
		err := c.readErr
		c.mu.Unlock()
		return fmt.Errorf("acp: %s: %w", method, err)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("acp: %s: connection closed", method)
	case resp := <-ch:
		if resp.err != nil {
			return fmt.Errorf("acp: %s: %w", method, resp.err)
		}
		if out == nil || len(resp.result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.result, out); err != nil {
			return fmt.Errorf("acp: %s: decode result: %w", method, err)
		}
		return nil
	}
}

func (c *Client) writeFrame(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("acp: encode: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.conn.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("acp: write: %w", err)
	}
	return nil
}

// Close closes the stream. The harness drains its agents on disconnect, so the
// caller closes a session first when it wants the work to stop cleanly.
func (c *Client) Close() error { return c.conn.Close() }

// Initialize performs the ACP handshake.
func (c *Client) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{
			// The controller does not implement the client filesystem, terminal
			// or permission extensions: the harness runs its own tools.
			"fs":         map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal":   false,
			"permission": map[string]any{},
		},
	}
	var result struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := c.call(ctx, methodInitialize, params, &result); err != nil {
		return err
	}
	return nil
}

// NewSession creates a fresh persistent session whose working directory is
// cwd (an absolute path on the harness side).
func (c *Client) NewSession(ctx context.Context, cwd string) (string, error) {
	var result struct {
		SessionID string `json:"sessionId"`
	}
	params := map[string]any{"cwd": cwd, "mcpServers": []any{}}
	if err := c.call(ctx, methodNewSession, params, &result); err != nil {
		return "", err
	}
	if result.SessionID == "" {
		return "", errors.New("acp: session/new returned no session id")
	}
	return result.SessionID, nil
}

// Session is one entry of session/list.
type Session struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

// ListSessions returns persisted sessions, newest first, filtered by cwd when
// it is not empty. The controller uses it to recognise a session it already
// created after a restart.
func (c *Client) ListSessions(ctx context.Context, cwd string) ([]Session, error) {
	params := map[string]any{}
	if cwd != "" {
		params["cwd"] = cwd
	}
	var result struct {
		Sessions []Session `json:"sessions"`
	}
	if err := c.call(ctx, methodListSessions, params, &result); err != nil {
		return nil, err
	}
	return result.Sessions, nil
}

// ResumeSession re-attaches to a persisted session.
func (c *Client) ResumeSession(ctx context.Context, sessionID, cwd string) error {
	params := map[string]any{"sessionId": sessionID, "cwd": cwd, "mcpServers": []any{}}
	return c.call(ctx, methodResumeSession, params, nil)
}

// Prompt submits one turn and returns its stop reason once the agent settles.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (string, error) {
	params := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	}
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if err := c.call(ctx, methodPrompt, params, &result); err != nil {
		return "", err
	}
	return result.StopReason, nil
}

// CloseSession ends the session cleanly.
func (c *Client) CloseSession(ctx context.Context, sessionID string) error {
	return c.call(ctx, methodCloseSession, map[string]any{"sessionId": sessionID}, nil)
}

// Cancel asks the harness to stop the session's autonomous work.
func (c *Client) Cancel(sessionID string) {
	_ = c.writeFrame(map[string]any{"jsonrpc": "2.0", "method": methodCancel, "params": map[string]any{"sessionId": sessionID}})
}
