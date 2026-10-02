package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if c.PromptTokens != 5 || c.CompletionTokens != 2 || c.TTFT <= 0 || c.Total < c.TTFT || c.Content != "b" || c.Reasoning != "a" {
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

func TestStrata(t *testing.T) {
	loaded := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprintf(w, `{"status":"ok","loaded":%t}`, loaded)
		case "/v1/chat/completions":
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}],\"timings\":{\"draft_n\":4,\"draft_n_accepted\":3}}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
		case "/metrics":
			fmt.Fprint(w, `{"engine":{"max_context":32768},"live":{"state":"generating","prompt_tokens":100,"generated":20,"tok_s":41.5},"requests":[{"hit_rate":0.83}]}`)
		}
	}))
	defer srv.Close()
	o := testOptions(t, srv)
	o.ModelPath = "/models/strata/config/strata-iq2_xs.json"
	a, err := New("strata", o)
	if err != nil {
		t.Fatal(err)
	}
	if argv := a.Argv(); argv[0] != "/opt/strata/.venv/bin/python" || argv[1] != "/opt/strata/serve/server.py" {
		t.Fatalf("argv %v", argv)
	}
	ctx := context.Background()
	if err := a.Ready(ctx); !errors.Is(err, ErrNotReady) {
		t.Fatalf("ready while loading: %v", err)
	}
	loaded = true
	if err := a.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := a.Complete(ctx, Request{Prompt: "hi", MaxTokens: 2})
	if err != nil || c.DraftAccepted != 3 || c.ExpertHitRate != 0.83 || len(c.ITL) != 1 {
		t.Fatalf("completion %+v %v", c, err)
	}
	m, err := a.Metrics(ctx)
	got := map[string]float64{}
	for _, x := range m {
		got[x.Name] = x.Value
	}
	if err != nil || got["decoded_tokens"] != 20 || got["context_tokens"] != 120 || got["context_size"] != 32768 || got["strata:tok_s"] != 41.5 {
		t.Fatalf("metrics %v %v", got, err)
	}
	info := a.Info(`strata generate: pre-filled 5762 of 5762 slots from the profile; slot 0 verified
strata generate: session is up (engine 0.1.31)
strata generate: token graph hit path: 10306 resident experts, decided on the device
strata serve: layer split: layers 0-21 (CUDA0), 22-47 (CUDA1), one hand-off per window
strata serve: 478 MiB of VRAM free with everything loaded
`)
	if info["version"] != "0.1.31" || info["gpu_resident_experts"] != "10306" || info["vram_free_mib"] != "478" ||
		info["layer_split"] != "layers 0-21 (CUDA0), 22-47 (CUDA1), one hand-off per window" {
		t.Fatalf("info %v", info)
	}
}

func TestSettings(t *testing.T) {
	yes, no := true, false
	all := Settings{GPUs: []int{0, 1}, Context: 65536, MTP: &yes, KVCache: "q8_0", ExpertsOnCPU: &yes}
	for _, tc := range []struct {
		engine string
		s      Settings
		want   string
	}{
		{"llamacpp", all, "--device CUDA0,CUDA1 --ctx-size 65536 --cache-type-k q8_0 --cache-type-v q8_0 --spec-type draft-mtp --cpu-moe --x"},
		{"freetoken", Settings{GPUs: []int{1, 0}, Context: 8192, ExpertsOnCPU: &no}, "--tp-size 2 --gpu 1,0 --max-seq-len-override 8192 --moe-strategy fused --x"},
		{"strata", Settings{GPUs: []int{1}, MTP: &yes}, "--gpu 1 --x"},
	} {
		a, err := New(tc.engine, Options{Settings: tc.s, Args: []string{"--x"}})
		if err != nil {
			t.Fatal(err)
		}
		argv := a.Argv()
		if got := strings.Join(argv[len(argv)-len(strings.Fields(tc.want)):], " "); got != tc.want {
			t.Errorf("%s: %s", tc.engine, got)
		}
	}
	for _, tc := range []struct {
		engine string
		s      Settings
	}{
		{"freetoken", Settings{MTP: &yes}},
		{"strata", Settings{MTP: &no}},
		{"strata", Settings{KVCache: "q4_0"}},
		{"llamacpp", Settings{GPUs: []int{2}}},
		{"llamacpp", Settings{GPUs: []int{0, 0}}},
	} {
		if _, err := New(tc.engine, Options{Settings: tc.s}); err == nil {
			t.Errorf("%s accepted %+v", tc.engine, tc.s)
		}
	}
}

// Every listed engine can be made, and nothing else.
func TestEngines(t *testing.T) {
	for _, e := range Engines {
		if _, err := New(e, Options{}); err != nil {
			t.Error(err)
		}
	}
	if _, err := New("nope", Options{}); err == nil {
		t.Error("an unknown engine was accepted")
	}
}

func TestStrataPrepareRewritesConfig(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "in.json")
	os.WriteFile(config, []byte(`{"exe":"strata","args":["--max-context","32768","--spec","4"],"log":"/dev/stderr"}`), 0o644)
	a, err := New("strata", Options{ModelPath: config, Settings: Settings{Context: 65536, KVCache: "q8_0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.(Preparer).Prepare(dir); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(a.Argv(), " ")
	out, _ := os.ReadFile(filepath.Join(dir, "strata-config.json"))
	if !strings.Contains(argv, "--config "+filepath.Join(dir, "strata-config.json")) ||
		!strings.Contains(string(out), `"65536"`) || !strings.Contains(string(out), `"int8"`) || !strings.Contains(string(out), `"/dev/stderr"`) {
		t.Fatalf("argv %s\nconfig %s", argv, out)
	}
}
