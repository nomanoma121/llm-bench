package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

// proxy stands between a harness and the runtime. It forwards every request
// and measures each chat completion the way a direct request is measured, so
// the numbers do not depend on how a harness talks to the model. The job's
// sampling settings replace the harness's own, so harnesses compare fairly.
type proxy struct {
	URL      string
	target   *url.URL
	sampling job.Sampling
	reverse  *httputil.ReverseProxy
	client   *http.Client
	server   *http.Server

	mu      sync.Mutex
	records []runtime.Completion
}

func startProxy(target string, sampling job.Sampling) (*proxy, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &proxy{
		URL:      "http://" + ln.Addr().String(),
		target:   u,
		sampling: sampling,
		reverse:  httputil.NewSingleHostReverseProxy(u),
		client:   &http.Client{Transport: &http.Transport{Proxy: nil}},
	}
	p.server = &http.Server{Handler: p}
	go func() { _ = p.server.Serve(ln) }()
	return p, nil
}

func (p *proxy) Close() error { return p.server.Close() }

// mark returns a position in the record list; since returns what came after.
func (p *proxy) mark() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.records)
}

func (p *proxy) since(mark int) []runtime.Completion {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]runtime.Completion(nil), p.records[mark:]...)
}

func (p *proxy) add(c runtime.Completion) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = append(p.records, c)
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		p.reverse.ServeHTTP(w, r)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	stream, _ := body["stream"].(bool)
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if t := p.sampling.Temperature; t != nil {
		body["temperature"] = *t
	}
	if t := p.sampling.TopP; t != nil {
		body["top_p"] = *t
	}
	if s := p.sampling.Seed; s != nil {
		body["seed"] = *s
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.target.JoinPath(r.URL.Path).String(), bytes.NewReader(b))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Content-Length")
	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(w, resp.Body)
		return
	}
	if !stream {
		raw, _ := io.ReadAll(resp.Body)
		_, _ = w.Write(raw)
		p.add(completionOf(raw, time.Since(start)))
		return
	}
	// The stream goes to the harness as it arrives and, through the pipe, to
	// the same reader a direct request uses.
	pr, pw := io.Pipe()
	measured := make(chan runtime.Completion, 1)
	go func() {
		got, _ := runtime.ReadStream(context.WithoutCancel(r.Context()), pr, start)
		_, _ = io.Copy(io.Discard, pr)
		measured <- got
	}()
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, _ = pw.Write(buf[:n])
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
	pw.Close()
	p.add(<-measured)
}

// completionOf reads the usage of a non-streamed completion, which has no
// first-token time of its own.
func completionOf(raw []byte, total time.Duration) runtime.Completion {
	var v struct {
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &v)
	c := runtime.Completion{PromptTokens: v.Usage.PromptTokens, CompletionTokens: v.Usage.CompletionTokens, TTFT: total, Total: total}
	if d := v.Usage.PromptTokensDetails; d != nil {
		c.CachedTokens = d.CachedTokens
	}
	return c
}
