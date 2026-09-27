package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/run"
)

type fakeService struct {
	metrics    []byte
	metricsErr error
	submitted  []string
	run        run.Run
	submitErr  error
	statusErr  error
}

func (f *fakeService) Submit(_ context.Context, experimentPath, _ string) (run.Run, error) {
	f.submitted = append(f.submitted, experimentPath)
	if f.submitErr != nil {
		return run.Run{}, f.submitErr
	}
	return f.run, nil
}

func (f *fakeService) Metrics(_ context.Context, _ string) ([]byte, error) {
	if f.metricsErr != nil {
		return nil, f.metricsErr
	}
	return f.metrics, nil
}

func (f *fakeService) Status(_ context.Context, _ string) (run.Run, error) {
	if f.statusErr != nil {
		return run.Run{}, f.statusErr
	}
	return f.run, nil
}

func serve(t *testing.T, svc RunService, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer((&Server{Service: svc, Token: token}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// get performs an unauthenticated GET and returns the response.
func get(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestSubmitRequiresAuthWhenTokenSet(t *testing.T) {
	f := &fakeService{run: run.Run{ID: "r1"}}
	ts := serve(t, f, "secret")

	resp, err := http.Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(`{"experiment":"e.yaml"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/runs", strings.NewReader(`{"experiment":"e.yaml"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	req.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: status = %d", resp2.StatusCode)
	}

	req3, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/runs", strings.NewReader(`{"experiment":"e.yaml"}`))
	req3.Header.Set("Authorization", "Bearer secret")
	req3.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusAccepted {
		t.Fatalf("good token: status = %d", resp3.StatusCode)
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	_ = json.NewDecoder(resp3.Body).Decode(&body)
	if body.RunID != "r1" {
		t.Fatalf("run_id = %q", body.RunID)
	}
}

func TestNoAuthNeededWithoutToken(t *testing.T) {
	f := &fakeService{run: run.Run{ID: "r1"}}
	ts := serve(t, f, "")
	resp, err := http.Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(`{"experiment":"e.yaml"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestSubmitMapsErrors(t *testing.T) {
	t.Run("local forbidden -> 403", func(t *testing.T) {
		f := &fakeService{submitErr: fmt.Errorf("wrap: %w", ErrLocalForbidden)}
		ts := serve(t, f, "")
		resp, err := http.Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(`{"experiment":"e.yaml"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})
	t.Run("bad request -> 400", func(t *testing.T) {
		f := &fakeService{submitErr: &BadRequestError{Err: errors.New("missing file")}}
		ts := serve(t, f, "")
		resp, err := http.Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(`{"experiment":"e.yaml"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})
	t.Run("invalid json -> 400", func(t *testing.T) {
		f := &fakeService{}
		ts := serve(t, f, "")
		resp, err := http.Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(`{`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})
	t.Run("state write failure -> 500", func(t *testing.T) {
		f := &fakeService{submitErr: &os.PathError{Op: "write", Path: "/state/run.json", Err: syscall.EACCES}}
		ts := serve(t, f, "")
		resp, err := http.Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(`{"experiment":"e.yaml"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (a controller-side PathError is not a caller mistake)", resp.StatusCode)
		}
	})
	t.Run("internal failure -> 500", func(t *testing.T) {
		f := &fakeService{submitErr: errors.New("store unavailable")}
		ts := serve(t, f, "")
		resp, err := http.Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(`{"experiment":"e.yaml"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
	t.Run("missing experiment -> 400", func(t *testing.T) {
		f := &fakeService{}
		ts := serve(t, f, "")
		resp, err := http.Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})
}

func TestStatusEndpoint(t *testing.T) {
	f := &fakeService{run: run.Run{ID: "r1", Phase: run.PhaseSucceeded,
		Artifacts: run.Artifacts{ArtifactDigest: "sha256:abc"}}}
	ts := serve(t, f, "")

	resp, err := http.Get(ts.URL + "/v1/runs/r1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var view StatusView
	_ = json.NewDecoder(resp.Body).Decode(&view)
	if view.ID != "r1" || view.Artifacts.ArtifactDigest != "sha256:abc" {
		t.Fatalf("view = %+v", view)
	}
	// Publication is CI's business: the API never reports a public URL.
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "public_url") || strings.Contains(string(body), "publish_error") {
		t.Fatalf("status view must not expose legacy publication fields: %s", body)
	}

	f.statusErr = run.ErrNotFound
	resp2, err := http.Get(ts.URL + "/v1/runs/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("missing: status = %d", resp2.StatusCode)
	}
}

func TestDerivedLabels(t *testing.T) {
	r := run.Run{Phase: run.PhaseAcquiring, WaitReason: "target busy"}
	if NewStatusView(r).DerivedLabel != "awaiting_acquire" {
		t.Fatal("awaiting_acquire label missing")
	}
	r2 := run.Run{
		Phase: run.PhaseReleasing,
		Hooks: []run.HookState{{Name: "a", Phase: run.HookReleasing, Error: "k8s down"}},
	}
	if NewStatusView(r2).DerivedLabel != "needs_restore" {
		t.Fatal("needs_restore label missing")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	body := []byte(`{"schema_version":1,"run_id":"r1","kind":"measurement","protocol":{"id":"p","digest":"d"},"measurement_valid":true,"metrics":[{"name":"decode_step_ms","value":17.7,"unit":"ms/step","source":"driver"}]}`)

	f := &fakeService{run: run.Run{ID: "r1", Phase: run.PhaseSucceeded, MetricsDigest: "m"}, metrics: body}
	ts := serve(t, f, "secret")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/runs/r1/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(body) {
		t.Fatalf("body = %s", got)
	}

	// Evidence is on the authenticated control API only.
	if rec := get(t, ts.URL+"/v1/runs/r1/metrics", ""); rec.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", rec.StatusCode)
	}

	// A run without evidence is 404, as is an unknown run.
	f2 := &fakeService{run: run.Run{ID: "r2"}, metricsErr: ErrNoEvidence}
	ts2 := serve(t, f2, "")
	if rec := get(t, ts2.URL+"/v1/runs/r2/metrics", ""); rec.StatusCode != http.StatusNotFound {
		t.Fatalf("no-evidence status = %d", rec.StatusCode)
	}
	f3 := &fakeService{metricsErr: run.ErrNotFound}
	ts3 := serve(t, f3, "")
	if rec := get(t, ts3.URL+"/v1/runs/missing/metrics", ""); rec.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d", rec.StatusCode)
	}

	// Corruption is a server error, never a silent empty answer.
	f4 := &fakeService{metricsErr: errors.New("digest mismatch")}
	ts4 := serve(t, f4, "")
	if rec := get(t, ts4.URL+"/v1/runs/r4/metrics", ""); rec.StatusCode != http.StatusInternalServerError {
		t.Fatalf("corrupt status = %d", rec.StatusCode)
	}
}
