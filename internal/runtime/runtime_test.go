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

func TestLlamaCppInfo(t *testing.T) {
	log := `I load_tensors: offloaded 65/65 layers to GPU
I load_tensors:        CUDA0 model buffer size =  7520.12 MiB
I load_tensors:        CUDA1 model buffer size =  7311.50 MiB
I llama_kv_cache:      CUDA0 KV buffer size =   544.00 MiB
I llama_kv_cache: size = 1088.00 MiB (  8192 cells,  16 layers,  4/1 seqs), K (q8_0):  544.00 MiB, V (q8_0):  544.00 MiB
I llama_context:      CUDA0 compute buffer size =   301.75 MiB
I srv    load_model: initializing, n_slots = 4, n_ctx_slot = 8192, kv_unified = 'true'`
	info := llamaCpp{}.Info(log)
	for k, want := range map[string]string{
		"layers_on_gpu": "65", "model_buffer_mib.CUDA1": "7311.50", "kv_buffer_mib.CUDA0": "544.00",
		"kv_cache_mib": "1088.00", "kv_type_k": "q8_0", "compute_buffer_mib.CUDA0": "301.75", "ctx_per_slot": "8192",
	} {
		if info[k] != want {
			t.Errorf("%s = %q, want %q (%v)", k, info[k], want, info)
		}
	}
}
