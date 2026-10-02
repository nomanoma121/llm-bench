package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenCodeRunsASession(t *testing.T) {
	var polls atomic.Int32
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "opencode" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /session":
			w.Write([]byte(`{"id":"ses_1"}`))
		case "POST /session/ses_1/prompt_async":
			var body struct {
				Parts []struct{ Text string } `json:"parts"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			prompt = body.Parts[0].Text
			w.WriteHeader(http.StatusNoContent)
		case "GET /session/status":
			if polls.Add(1) < 3 {
				w.Write([]byte(`{"ses_1":{"type":"busy"}}`))
				return
			}
			w.Write([]byte(`{}`))
		case "GET /session/ses_1/message":
			w.Write([]byte(`[{"info":{"role":"user"},"parts":[{"type":"text","text":"task"}]},
				{"info":{"role":"assistant"},"parts":[{"type":"tool","text":""},{"type":"text","text":"Opened the PR."}]}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	o := &OpenCode{URL: srv.URL, Username: "opencode", Password: "secret", Poll: time.Millisecond}
	var session string
	reply, err := o.Run(context.Background(), "Optimize llama.cpp\nmore", func(id string) { session = id })
	if err != nil || reply != "Opened the PR." || session != "ses_1" || prompt != "Optimize llama.cpp\nmore" || polls.Load() != 3 {
		t.Fatalf("reply %q err %v session %q prompt %q polls %d", reply, err, session, prompt, polls.Load())
	}
}
