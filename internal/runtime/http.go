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
)

func setSampling(body map[string]any, req Request) {
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
}

func streamSSE(ctx context.Context, resp *http.Response, onData func([]byte) error) error {
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, ok := bytes.CutPrefix(scanner.Bytes(), []byte("data:"))
		data = bytes.TrimSpace(data)
		if !ok || len(data) == 0 || string(data) == "[DONE]" {
			continue
		}
		if err := onData(data); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func postJSON(ctx context.Context, client *http.Client, url string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("runtime: %s returned %d: %s", url, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		return ErrNotReady
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("runtime: %s returned %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
