package benchmark

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nomanoma121/llm-bench/internal/job"
	"github.com/nomanoma121/llm-bench/internal/runtime"
)

func TestProxyMeasuresAndOverridesSampling(t *testing.T) {
	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		got = nil
		json.NewDecoder(r.Body).Decode(&got)
		if got["stream"] != true {
			fmt.Fprint(w, `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
			return
		}
		for _, c := range []string{"a", "b", "c"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", c)
			w.(http.Flusher).Flush()
			time.Sleep(5 * time.Millisecond)
		}
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":4}}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	zero := 0.0
	p, err := startProxy(upstream.URL, job.Sampling{Temperature: &zero, ReasoningEffort: "none"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	post := func(body string) string {
		resp, err := http.Post(p.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	mark := p.mark()
	if out := post(`{"model":"m","stream":true,"temperature":0.9,"messages":[]}`); !strings.Contains(out, `"content":"c"`) {
		t.Fatalf("stream not passed through: %s", out)
	}
	if sent := got; sent["temperature"] != 0.0 || sent["stream_options"] == nil || sent["reasoning_effort"] != "none" || sent["chat_template_kwargs"].(map[string]any)["enable_thinking"] != false {
		t.Fatalf("upstream got %v", got)
	}
	post(`{"model":"m","messages":[]}`)
	if resp, err := http.Get(p.URL + "/v1/models"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("other paths are not proxied: %v", err)
	}
	reqs := p.since(mark)
	if len(reqs) != 2 || reqs[0].CompletionTokens != 3 || reqs[0].CachedTokens != 4 || reqs[0].TTFT <= 0 || len(reqs[0].ITL) != 2 || reqs[1].PromptTokens != 7 {
		t.Fatalf("records %+v", reqs)
	}
}

func TestRequestValuesAddsUpASession(t *testing.T) {
	v := requestValues([]runtime.Completion{
		{PromptTokens: 1000, CompletionTokens: 11, TTFT: time.Second, Total: 2 * time.Second},
		{PromptTokens: 1200, CachedTokens: 1000, CompletionTokens: 21, TTFT: time.Second, Total: 3 * time.Second},
	})
	// decode: (10 + 20) tokens over (1 + 2) s; prefill: (1000 + 200) tokens over 2 s
	if v["requests"] != 2 || v["decode_tok_per_s"] != 10 || v["prefill_tok_per_s"] != 600 || v["tokens_out"] != 32 || v["cached_tokens"] != 1000 {
		t.Fatalf("%v", v)
	}
}
