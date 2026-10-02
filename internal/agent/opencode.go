// Package agent hands an optimize job to a hosted opencode server over its
// REST API (opencode web/serve).
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenCode is an opencode server. Password enables its HTTP basic auth.
type OpenCode struct {
	URL      string
	Username string
	Password string
	// Model is provider/model, e.g. opencode-go/deepseek-v4-flash; empty uses
	// the server's default.
	Model string
	// Poll is how often a running session is checked.
	Poll   time.Duration
	Client *http.Client
}

// Run starts a session with the task, reports its id, waits until opencode
// has finished working on it and returns the end of its last reply. A
// cancelled ctx aborts the session.
func (o *OpenCode) Run(ctx context.Context, task string, onSession func(string)) (string, error) {
	var session struct {
		ID string `json:"id"`
	}
	if err := o.call(ctx, http.MethodPost, "/session", map[string]any{"title": firstLine(task)}, &session); err != nil {
		return "", err
	}
	if session.ID == "" {
		return "", errors.New("opencode: POST /session returned no id")
	}
	onSession(session.ID)
	prompt := map[string]any{"parts": []map[string]string{{"type": "text", "text": task}}}
	if provider, model, ok := strings.Cut(o.Model, "/"); ok {
		prompt["model"] = map[string]string{"providerID": provider, "modelID": model}
	}
	if err := o.call(ctx, http.MethodPost, "/session/"+session.ID+"/prompt_async", prompt, nil); err != nil {
		return "", err
	}
	for {
		select {
		case <-ctx.Done():
			_ = o.call(context.WithoutCancel(ctx), http.MethodPost, "/session/"+session.ID+"/abort", nil, nil)
			return "", ctx.Err()
		case <-time.After(o.Poll):
		}
		var status map[string]struct {
			Type string `json:"type"`
		}
		if err := o.call(ctx, http.MethodGet, "/session/status", nil, &status); err != nil {
			continue
		}
		// A session that has finished is idle or no longer listed.
		if s, ok := status[session.ID]; !ok || s.Type == "idle" {
			return o.lastReply(ctx, session.ID)
		}
	}
}

func (o *OpenCode) lastReply(ctx context.Context, id string) (string, error) {
	var messages []struct {
		Info struct {
			Role  string          `json:"role"`
			Error json.RawMessage `json:"error"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := o.call(ctx, http.MethodGet, "/session/"+id+"/message", nil, &messages); err != nil {
		return "", err
	}
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Info.Role != "assistant" {
			continue
		}
		var text strings.Builder
		for _, p := range m.Parts {
			if p.Type == "text" {
				text.WriteString(p.Text)
			}
		}
		reply := tail(strings.TrimSpace(text.String()), 2000)
		if len(m.Info.Error) > 0 && string(m.Info.Error) != "null" {
			return reply, fmt.Errorf("opencode: the session ended with an error: %s", m.Info.Error)
		}
		return reply, nil
	}
	return "", errors.New("opencode: the session has no reply")
}

func (o *OpenCode) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(o.URL, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.Password != "" {
		req.SetBasicAuth(o.Username, o.Password)
	}
	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return fmt.Errorf("opencode: %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return tail(s, 80)
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
