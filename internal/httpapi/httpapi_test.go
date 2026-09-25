package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nomanoma121/llm-bench/internal/run"
)

type fakeService struct {
	submitted []string
	run       run.Run
	submitErr error
	statusErr error
}

func (f *fakeService) Submit(_ context.Context, experimentPath, _ string) (run.Run, error) {
	f.submitted = append(f.submitted, experimentPath)
	if f.submitErr != nil {
		return run.Run{}, f.submitErr
	}
	return f.run, nil
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
	f := &fakeService{run: run.Run{ID: "r1", Phase: run.PhaseSucceeded, PublicURL: "https://x/y"}}
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
	if view.ID != "r1" || view.PublicURL != "https://x/y" {
		t.Fatalf("view = %+v", view)
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
