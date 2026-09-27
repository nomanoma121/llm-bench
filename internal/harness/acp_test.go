package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
)

type pipe struct {
	io.Reader
	io.Writer
}

func (pipe) Close() error { return nil }

func fakeHarness(t *testing.T, in io.Reader, out io.Writer) {
	send := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
	}
	r := bufio.NewReader(in)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var f struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Result struct {
				Outcome struct {
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			} `json:"result"`
		}
		json.Unmarshal(line, &f)
		switch f.Method {
		case "initialize":
			send(map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": map[string]any{"protocolVersion": 1}})
		case "session/new":
			send(map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": map[string]any{"sessionId": "s1"}})
		case "session/prompt":
			send(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "session/request_permission", "params": map[string]any{
				"options": []map[string]string{{"optionId": "no", "kind": "reject_once"}, {"optionId": "yes", "kind": "allow_once"}},
			}})
			reply, _ := r.ReadBytes('\n')
			json.Unmarshal(reply, &f)
			if f.Result.Outcome.OptionID != "yes" {
				t.Errorf("permission answered with %q", f.Result.Outcome.OptionID)
			}
			for _, text := range []string{"pushed ", "llmbench/x"} {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}},
				}})
			}
			send(map[string]any{"jsonrpc": "2.0", "id": 3, "result": map[string]any{"stopReason": "end_turn"}})
		}
	}
}

func TestRunSession(t *testing.T) {
	clientR, harnessW := io.Pipe()
	harnessR, clientW := io.Pipe()
	go fakeHarness(t, harnessR, harnessW)
	var session string
	msg, err := runSession(context.Background(), pipe{clientR, clientW}, "/workspace", "optimize", func(id string) { session = id })
	if err != nil {
		t.Fatal(err)
	}
	if session != "s1" || msg != "pushed llmbench/x" {
		t.Fatalf("session %q message %q", session, msg)
	}
}
