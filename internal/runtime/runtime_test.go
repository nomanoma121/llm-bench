package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func testOptions(t *testing.T, srv *httptest.Server) Options {
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return Options{ModelID: "m", ModelPath: "/models/m", Port: port}
}

func TestLlamaCpp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/v1/chat/completions":
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"a\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
		case "/metrics":
			fmt.Fprint(w, "# HELP x\nllamacpp:kv_cache_usage_ratio 0.5\nllamacpp:requests{slot=\"0\"} 1\n")
		}
	}))
	defer srv.Close()
	a, err := New("llamacpp", testOptions(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := a.Complete(ctx, Request{Prompt: "hi", MaxTokens: 2})
	if err != nil {
		t.Fatal(err)
	}
	if c.PromptTokens != 5 || c.CompletionTokens != 2 || c.TTFT <= 0 || c.Total < c.TTFT {
		t.Fatalf("completion %+v", c)
	}
	m, err := a.Metrics(ctx)
	if err != nil || len(m) != 2 || m[1].Labels["slot"] != "0" {
		t.Fatalf("metrics %+v %v", m, err)
	}
}

func TestFreeTokenNotReadyWhileLoading(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"maintenance":"loading"}`)
	}))
	defer srv.Close()
	a, _ := New("freetoken", testOptions(t, srv))
	if err := a.Ready(context.Background()); err == nil {
		t.Fatal("want not ready")
	}
}
