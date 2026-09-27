package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// harness is a minimal ACP server: it creates a session, streams one update,
// asks for permission, and settles the turn.
type harness struct {
	in  *bufio.Reader
	out io.Writer

	createdCWD string
	task       string
	allowed    bool
}

func (h *harness) serve() {
	for {
		line, err := h.in.ReadBytes('\n')
		if err != nil {
			return
		}
		var f map[string]any
		if err := json.Unmarshal(line, &f); err != nil {
			return
		}
		method, _ := f["method"].(string)
		switch method {
		case "initialize":
			h.reply(f, map[string]any{"protocolVersion": 1})
		case "session/new":
			params, _ := f["params"].(map[string]any)
			h.createdCWD, _ = params["cwd"].(string)
			h.reply(f, map[string]any{"sessionId": "session-1"})
		case "session/prompt":
			params, _ := f["params"].(map[string]any)
			if parts, ok := params["prompt"].([]any); ok && len(parts) > 0 {
				if first, ok := parts[0].(map[string]any); ok {
					h.task, _ = first["text"].(string)
				}
			}
			h.notify(map[string]any{
				"jsonrpc": "2.0", "method": "session/update",
				"params": map[string]any{"sessionId": "session-1", "update": map[string]any{"sessionUpdate": "agent_message_chunk"}},
			})
			// The Agent asks to run a tool; the binder answers.
			h.notify(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "session/request_permission",
				"params": map[string]any{
					"sessionId": "session-1",
					"options": []map[string]any{
						{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
						{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
					},
				},
			})
			answer, err := h.in.ReadBytes('\n')
			if err == nil && strings.Contains(string(answer), "allow-once") {
				h.allowed = true
			}
			h.reply(f, map[string]any{"stopReason": "end_turn"})
		case "session/close":
			h.reply(f, map[string]any{})
		default:
			h.reply(f, map[string]any{})
		}
	}
}

func (h *harness) reply(req map[string]any, result map[string]any) {
	h.notify(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": result})
}

func (h *harness) notify(v any) {
	payload, _ := json.Marshal(v)
	_, _ = h.out.Write(append(payload, '\n'))
}

type pipeTransport struct{ h *harness }

func (p *pipeTransport) Open(context.Context) (io.ReadWriteCloser, error) {
	hr, cw := io.Pipe()
	cr, hw := io.Pipe()
	p.h.in = bufio.NewReader(hr)
	p.h.out = hw
	go p.h.serve()
	return &conn{Reader: cr, Writer: cw, closeFn: func() error { _ = cw.Close(); _ = hw.Close(); return nil }}, nil
}

type conn struct {
	io.Reader
	io.Writer
	closeFn func() error
}

func (c *conn) Close() error { return c.closeFn() }

func TestBindCreatesASessionAndWaitsForIt(t *testing.T) {
	h := &harness{}
	binder := &ACPBinder{Transport: &pipeTransport{h: h}, CWD: "/workspace", AllowTools: true}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := binder.Bind(ctx, Request{JobID: "2026-09-27-issue42", Task: "optimize the runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "session-1" || result.StopReason != "end_turn" {
		t.Fatalf("result = %+v", result)
	}
	if h.createdCWD != "/workspace" {
		t.Fatalf("session cwd = %q", h.createdCWD)
	}
	if h.task != "optimize the runtime" {
		t.Fatalf("task = %q", h.task)
	}
	if !h.allowed {
		t.Fatal("the tool permission request was not allowed")
	}
}

func TestBindRefusesToolsWhenThePolicySaysSo(t *testing.T) {
	h := &harness{}
	binder := &ACPBinder{Transport: &pipeTransport{h: h}, CWD: "/workspace"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := binder.Bind(ctx, Request{JobID: "job"}); err != nil {
		t.Fatal(err)
	}
	if h.allowed {
		t.Fatal("a tool request was allowed although the policy disables it")
	}
}

func TestBindWithoutATransportFails(t *testing.T) {
	binder := &ACPBinder{}
	if _, err := binder.Bind(context.Background(), Request{JobID: "job"}); err == nil {
		t.Fatal("binding without a transport succeeded")
	}
}
