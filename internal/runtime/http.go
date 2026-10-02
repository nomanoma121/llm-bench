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
	SetReasoningEffort(body, req.ReasoningEffort)
}

// SetReasoningEffort asks for an effort the OpenAI way. "none" also turns
// thinking off through the chat template, which is how llama.cpp, Strata and
// FreeToken switch Qwen-style thinking off.
func SetReasoningEffort(body map[string]any, effort string) {
	if effort == "" {
		return
	}
	body["reasoning_effort"] = effort
	if effort == "none" {
		kwargs, _ := body["chat_template_kwargs"].(map[string]any)
		if kwargs == nil {
			kwargs = map[string]any{}
		}
		kwargs["enable_thinking"] = false
		body["chat_template_kwargs"] = kwargs
	}
}

func streamSSE(ctx context.Context, body io.ReadCloser, onData func([]byte) error) error {
	defer body.Close()
	scanner := bufio.NewScanner(body)
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

func chatCompletion(ctx context.Context, client *http.Client, baseURL, model string, req Request, extra map[string]any) (Completion, error) {
	body := map[string]any{
		"model":          model,
		"messages":       []map[string]string{{"role": "user", "content": req.Prompt}},
		"max_tokens":     req.MaxTokens,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	setSampling(body, req)
	for k, v := range extra {
		body[k] = v
	}
	start := time.Now()
	resp, err := postJSON(ctx, client, baseURL+"/v1/chat/completions", body)
	if err != nil {
		return Completion{}, err
	}
	return ReadStream(ctx, resp.Body, start)
}

// ReadStream reads an OpenAI chat completion event stream that was requested
// at start, timing its tokens and collecting usage and llama.cpp's timings.
func ReadStream(ctx context.Context, body io.ReadCloser, start time.Time) (Completion, error) {
	var out Completion
	var last time.Duration
	var content, reasoning strings.Builder
	err := streamSSE(ctx, body, func(data []byte) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptTokensDetails *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Timings *struct {
				CacheN         int `json:"cache_n"`
				DraftN         int `json:"draft_n"`
				DraftNAccepted int `json:"draft_n_accepted"`
			} `json:"timings"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content == "" && c.Delta.ReasoningContent == "" {
				continue
			}
			now := time.Since(start)
			if out.TTFT == 0 {
				out.TTFT = now
			} else {
				out.ITL = append(out.ITL, now-last)
			}
			last = now
			content.WriteString(c.Delta.Content)
			reasoning.WriteString(c.Delta.ReasoningContent)
		}
		if u := chunk.Usage; u != nil {
			out.PromptTokens = u.PromptTokens
			out.CompletionTokens = u.CompletionTokens
			if u.PromptTokensDetails != nil {
				out.CachedTokens = u.PromptTokensDetails.CachedTokens
			}
		}
		if t := chunk.Timings; t != nil {
			out.CachedTokens = t.CacheN
			out.DraftTokens = t.DraftN
			out.DraftAccepted = t.DraftNAccepted
		}
		return nil
	})
	out.Total = last
	out.Content = content.String()
	out.Reasoning = reasoning.String()
	return out, err
}
