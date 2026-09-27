package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// sseEvent is one `data:` payload of a text/event-stream response.
type sseEvent struct {
	Data string
}

// streamSSE reads a server-sent event response and calls onData for every
// data payload until the context is cancelled, the stream ends, or onData
// returns an error. Both adapters stream so first-token latency is
// observable; they differ only in the payload shape.
func streamSSE(ctx context.Context, resp *http.Response, onData func(data []byte) error) error {
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	// Model output can be long; the default 64 KiB token limit is plenty for
	// a delta but not for a whole flushed response.
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if err := onData(data); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// postJSON sends a JSON body and returns the response for streaming.
func postJSON(ctx context.Context, client *http.Client, url string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("runtime: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("runtime: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("runtime: request: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("runtime: %s returned %d: %s", url, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// getJSON decodes a JSON response into out. A 503 is reported as ErrNotReady:
// every supported engine answers that way while its engine is still loading.
func getJSON(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("runtime: request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("runtime: request: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		return ErrNotReady
	case resp.StatusCode/100 != 2:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("runtime: %s returned %d: %s", url, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("runtime: decode %s: %w", url, err)
	}
	return nil
}

// newHTTPClient returns a client without an overall timeout: a single
// generation may legitimately take minutes, and the callers bound the request
// with a context instead.
func newHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:               nil, // loopback only: never honour a proxy env var
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}}
}
