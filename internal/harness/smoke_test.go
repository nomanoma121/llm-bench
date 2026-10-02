package harness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSmoke installs the harnesses named in HARNESS_SMOKE (comma separated)
// with mise and runs each against a fake endpoint. It needs mise and Linux,
// as in the sandbox:
//
//	GOOS=linux GOARCH=amd64 go test -c -o harness.test ./internal/harness
//	docker run --rm -v $PWD/harness.test:/t -e HARNESS_SMOKE=opencode,pi,dsh,hermes \
//	  jdxcode/mise:2026.9.18-debian /t -test.run TestSmoke -test.v
func TestSmoke(t *testing.T) {
	if os.Getenv("HARNESS_SMOKE") == "" {
		t.Skip()
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			hits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":null}]}\n\n")
			fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1,\"total_tokens\":6}}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"m","object":"model"}]}`)
	}))
	defer srv.Close()
	m := Mise{DataDir: "/tmp/mise"}
	for _, name := range strings.Split(os.Getenv("HARNESS_SMOKE"), ",") {
		h, _ := New(name)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if err := m.Install(ctx, h); err != nil {
			t.Errorf("%s install: %v", name, err)
			cancel()
			continue
		}
		hits.Store(0)
		var log strings.Builder
		err := m.Run(ctx, h, Params{Prompt: "say done", BaseURL: srv.URL + "/v1", Model: "m", Context: 65536, Home: t.TempDir()}, t.TempDir(), &log)
		cancel()
		out := log.String()
		if len(out) > 1200 {
			out = out[len(out)-1200:]
		}
		if err != nil || hits.Load() == 0 {
			t.Errorf("%s: err=%v requests=%d\n%s", name, err, hits.Load(), out)
		}
	}
}
