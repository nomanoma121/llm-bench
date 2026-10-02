package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

type conn struct {
	rw       io.ReadWriteCloser
	onUpdate func(json.RawMessage)

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan frame
	closed  chan struct{}
	err     error
}

type frame struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newConn(rw io.ReadWriteCloser, onUpdate func(json.RawMessage)) *conn {
	c := &conn{rw: rw, onUpdate: onUpdate, pending: map[int64]chan frame{}, closed: make(chan struct{})}
	go c.read()
	return c
}

func (c *conn) read() {
	r := bufio.NewReaderSize(c.rw, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		var f frame
		if len(line) > 0 && json.Unmarshal(line, &f) == nil {
			c.dispatch(f)
		}
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			close(c.closed)
			return
		}
	}
}

func (c *conn) dispatch(f frame) {
	switch {
	case f.Method == "" && f.ID != nil:
		c.mu.Lock()
		ch := c.pending[*f.ID]
		delete(c.pending, *f.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- f
		}
	case f.Method == "session/update":
		var p struct {
			Update json.RawMessage `json:"update"`
		}
		if json.Unmarshal(f.Params, &p) == nil && c.onUpdate != nil {
			c.onUpdate(p.Update)
		}
	case f.Method == "session/request_permission" && f.ID != nil:
		_ = c.write(map[string]any{"jsonrpc": "2.0", "id": *f.ID, "result": map[string]any{"outcome": allow(f.Params)}})
	case f.ID != nil:
		_ = c.write(map[string]any{"jsonrpc": "2.0", "id": *f.ID, "error": map[string]any{"code": -32601, "message": "unsupported: " + f.Method}})
	}
}

func allow(params json.RawMessage) map[string]any {
	var req struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &req)
	for _, kind := range []string{"allow_once", "allow_always"} {
		for _, o := range req.Options {
			if o.Kind == kind {
				return map[string]any{"outcome": "selected", "optionId": o.OptionID}
			}
		}
	}
	return map[string]any{"outcome": "cancelled"}
}

func (c *conn) call(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan frame, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return fmt.Errorf("acp %s: connection closed: %w", method, c.err)
	case f := <-ch:
		if f.Error != nil {
			return fmt.Errorf("acp %s: %d %s", method, f.Error.Code, f.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(f.Result, out)
	}
}

func (c *conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.rw.Write(append(b, '\n'))
	return err
}

type transcript struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (t *transcript) add(update json.RawMessage) {
	var u struct {
		Kind    string `json:"sessionUpdate"`
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(update, &u) != nil || u.Kind != "agent_message_chunk" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.WriteString(u.Content.Text)
}

func (t *transcript) tail(n int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.buf.String()
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return strings.TrimSpace(s)
}

func runSession(ctx context.Context, rw io.ReadWriteCloser, cwd, task string, onSession func(string)) (string, error) {
	var t transcript
	c := newConn(rw, t.add)
	defer rw.Close()
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false},
	}, nil); err != nil {
		return "", err
	}
	var session struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &session); err != nil {
		return "", err
	}
	if session.SessionID == "" {
		return "", errors.New("acp: session/new returned no session id")
	}
	onSession(session.SessionID)
	var result struct {
		StopReason string `json:"stopReason"`
	}
	err := c.call(ctx, "session/prompt", map[string]any{
		"sessionId": session.SessionID,
		"prompt":    []map[string]string{{"type": "text", "text": task}},
	}, &result)
	if err != nil {
		return t.tail(2000), err
	}
	if result.StopReason != "end_turn" {
		return t.tail(2000), fmt.Errorf("harness stopped with %q", result.StopReason)
	}
	return t.tail(2000), nil
}
