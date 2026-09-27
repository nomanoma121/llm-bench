package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// harness is a small ACP server: enough of the protocol to drive the client,
// with the permission and update paths exercised.
type harness struct {
	t *testing.T

	clientOut *io.PipeWriter // frames we send to the client
	harnessIn *bufio.Reader  // frames we receive

	sawInitialize bool
	sawAllow      string
	permissions   int
}

// newPair connects a client to an in-process harness over two pipes.
func newPair(t *testing.T, updates int) (*Client, *harness) {
	t.Helper()
	// client writes -> harness reads
	hr, cw := io.Pipe()
	// harness writes -> client reads
	cr, hw := io.Pipe()
	h := &harness{t: t, clientOut: hw, harnessIn: bufio.NewReader(hr)}

	client := New(&pipeConn{Reader: cr, Writer: cw, closeFn: func() error {
		_ = cw.Close()
		_ = hw.Close()
		return nil
	}}, Options{
		OnUpdate: func(u Update) {
			if u.Kind() == "" {
				t.Errorf("update without a kind: %s", u.Update)
			}
		},
		OnPermission: func(req PermissionRequest) (string, bool) {
			h.permissions++
			if id, ok := req.Allow(); ok {
				return id, true
			}
			return "", false
		},
	})
	go client.Run()
	go h.serve(updates)
	return client, h
}

// serve answers the client's requests.
func (h *harness) serve(updates int) {
	for {
		line, err := h.harnessIn.ReadBytes('\n')
		if err != nil {
			return
		}
		var f map[string]any
		if err := json.Unmarshal(line, &f); err != nil {
			h.t.Errorf("harness received invalid JSON: %v", err)
			return
		}
		method, _ := f["method"].(string)
		switch method {
		case methodInitialize:
			h.sawInitialize = true
			h.reply(f, map[string]any{"protocolVersion": ProtocolVersion, "agentCapabilities": map[string]any{}})
		case methodNewSession:
			h.reply(f, map[string]any{"sessionId": "session-1"})
		case methodListSessions:
			h.reply(f, map[string]any{"sessions": []map[string]any{{"sessionId": "session-1", "cwd": "/workspace"}}})
		case methodResumeSession:
			h.reply(f, map[string]any{})
		case methodCloseSession:
			h.reply(f, map[string]any{})
		case methodPrompt:
			// One update and one permission request, as a real turn produces.
			for i := range updates {
				h.notify(map[string]any{
					"jsonrpc": "2.0", "method": methodUpdate,
					"params": map[string]any{
						"sessionId": "session-1",
						"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "n": i},
					},
				})
			}
			h.requestPermission()
			h.reply(f, map[string]any{"stopReason": "end_turn"})
		default:
			h.replyError(f, -32601, "unsupported method "+method)
		}
	}
}

func (h *harness) requestPermission() {
	id := int64(9000)
	h.notify(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": methodRequestPermit,
		"params": map[string]any{
			"sessionId": "session-1",
			"toolCall":  map[string]any{"toolCallId": "call-1"},
			"options": []map[string]any{
				{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
				{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
			},
		},
	})
	line, err := h.harnessIn.ReadBytes('\n')
	if err != nil {
		h.t.Errorf("no permission answer: %v", err)
		return
	}
	var f map[string]any
	if err := json.Unmarshal(line, &f); err != nil {
		h.t.Errorf("bad permission answer: %v", err)
		return
	}
	result, _ := f["result"].(map[string]any)
	outcome, _ := result["outcome"].(map[string]any)
	if outcome["outcome"] != "selected" || outcome["optionId"] != "allow-once" {
		h.t.Errorf("permission answer = %v", f)
	}
	h.sawAllow, _ = outcome["optionId"].(string)
}

func (h *harness) reply(request map[string]any, result map[string]any) {
	h.notify(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
}

func (h *harness) replyError(request map[string]any, code int, message string) {
	h.notify(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": code, "message": message}})
}

func (h *harness) notify(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		h.t.Errorf("marshal: %v", err)
		return
	}
	if _, err := h.clientOut.Write(append(payload, '\n')); err != nil {
		return
	}
}

// pipeConn adapts two pipes into the duplex stream the client wants.
type pipeConn struct {
	io.Reader
	io.Writer
	closeFn func() error
}

func (p *pipeConn) Close() error { return p.closeFn() }

func TestClientDrivesASession(t *testing.T) {
	client, h := newPair(t, 2)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if !h.sawInitialize {
		t.Fatal("the client did not initialize")
	}
	sessions, err := client.ListSessions(ctx, "/workspace")
	if err != nil || len(sessions) != 1 || sessions[0].SessionID != "session-1" {
		t.Fatalf("list = %+v, %v", sessions, err)
	}
	sessionID, err := client.NewSession(ctx, "/workspace")
	if err != nil || sessionID != "session-1" {
		t.Fatalf("new session = %q, %v", sessionID, err)
	}
	stop, err := client.Prompt(ctx, sessionID, "optimize the runtime")
	if err != nil || stop != "end_turn" {
		t.Fatalf("prompt = %q, %v", stop, err)
	}
	if h.sawAllow != "allow-once" {
		t.Fatalf("the permission request was not allowed: %q", h.sawAllow)
	}
	if err := client.CloseSession(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionIsRefusedWithoutAHandler(t *testing.T) {
	hr, cw := io.Pipe()
	cr, hw := io.Pipe()
	client := New(&pipeConn{Reader: cr, Writer: cw, closeFn: func() error { _ = cw.Close(); _ = hw.Close(); return nil }}, Options{})
	go client.Run()
	defer client.Close()

	answers := make(chan string, 1)
	go func() {
		buf := bufio.NewReader(hr)
		line, err := buf.ReadBytes('\n')
		if err != nil {
			answers <- "no handshake: " + err.Error()
			return
		}
		var init map[string]any
		if err := json.Unmarshal(line, &init); err != nil {
			answers <- "bad handshake: " + err.Error()
			return
		}
		_, _ = hw.Write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{"protocolVersion":1}}`+"\n", init["id"])))
		// Ask for a permission the client has no handler for: it must refuse
		// rather than allow by default.
		_, _ = hw.Write([]byte(`{"jsonrpc":"2.0","id":9000,"method":"session/request_permission","params":{"sessionId":"s","options":[{"optionId":"allow-once","kind":"allow_once"}]}}` + "\n"))
		answer, err := buf.ReadBytes('\n')
		if err != nil {
			answers <- "no answer: " + err.Error()
			return
		}
		answers <- string(answer)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-answers:
		if !strings.Contains(answer, `"cancelled"`) {
			t.Fatalf("a permission request without a handler was not refused: %s", answer)
		}
	case <-ctx.Done():
		t.Fatal("the permission request was never answered")
	}
}

func TestCallFailsWhenTheStreamEnds(t *testing.T) {
	hr, cw := io.Pipe()
	cr, hw := io.Pipe()
	// io.Pipe writes block until they are read, so drain the harness side.
	go func() { _, _ = io.Copy(io.Discard, hr) }()
	client := New(&pipeConn{Reader: cr, Writer: cw, closeFn: func() error { _ = cw.Close(); _ = hw.Close(); return nil }}, Options{})
	go client.Run()
	_ = hw.Close() // the harness disappears
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err == nil {
		t.Fatal("a call on a closed stream succeeded")
	}
	client.Close()
}

func TestUpdateKind(t *testing.T) {
	u := Update{Update: json.RawMessage(`{"sessionUpdate":"agent_thought_chunk"}`)}
	if got := u.Kind(); got != "agent_thought_chunk" {
		t.Fatalf("kind = %q", got)
	}
	if got := (Update{}).Kind(); got != "" {
		t.Fatalf("empty kind = %q", got)
	}
}

var _ = fmt.Sprintf
