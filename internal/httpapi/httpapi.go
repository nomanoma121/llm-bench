// Package httpapi exposes the controller HTTP API. Handlers only decode,
// authorize and delegate to the RunService; all logic lives elsewhere.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-logr/logr"

	"github.com/nomanoma121/llm-bench/internal/run"
)

// ErrLocalForbidden is returned by the service when a submit would execute on
// a local target over HTTP without the operator's explicit opt-in.
var ErrLocalForbidden = errors.New("httpapi: local target execution over HTTP is forbidden")

// RunService is the API's view of the controller. Declared here (point of
// use) and implemented by the wiring layer.
type RunService interface {
	// Submit records a run for the experiment at the given repository-relative
	// path. It does not wait for completion.
	Submit(ctx context.Context, experimentPath, inputCommit string) (run.Run, error)
	// Status loads a run record.
	Status(ctx context.Context, id string) (run.Run, error)
	// Metrics returns the sealed measurement evidence of a run. Servable only
	// when the run is sealed and the recorded digest still matches the bytes
	// on disk (docs/optimization.md §5.1).
	Metrics(ctx context.Context, id string) ([]byte, error)
}

// ErrNoEvidence reports a run without sealed measurement evidence.
var ErrNoEvidence = errors.New("httpapi: run has no sealed evidence")

// Server is the HTTP API server.
type Server struct {
	Service RunService
	// Token guards /v1/* when set. Without a token the API is open; callers
	// must bind to loopback in that mode.
	Token string
	Log   logr.Logger
}

// StatusView is the public projection of a run record.
type StatusView struct {
	ID              string              `json:"id"`
	Target          string              `json:"target"`
	Phase           run.Phase           `json:"phase"`
	DerivedLabel    string              `json:"derived_label,omitempty"`
	ExecutionResult run.ExecutionResult `json:"execution_result"`
	ExecutionState  run.ExecutionState  `json:"execution_state"`
	LeaseState      run.LeaseState      `json:"lease_state"`
	WaitReason      string              `json:"wait_reason,omitempty"`
	Hooks           []run.HookState     `json:"hooks"`
	Artifacts       run.Artifacts       `json:"artifacts"`
}

// NewStatusView projects a run record for the API and CLI status output.
func NewStatusView(r run.Run) StatusView {
	v := StatusView{
		ID:              r.ID,
		Target:          r.Target,
		Phase:           r.Phase,
		ExecutionResult: r.ExecutionResult,
		ExecutionState:  r.ExecutionState,
		LeaseState:      r.LeaseState,
		WaitReason:      r.WaitReason,
		Hooks:           r.Hooks,
		Artifacts:       r.Artifacts,
	}
	switch {
	case r.Phase == run.PhaseAcquiring && r.WaitReason != "":
		v.DerivedLabel = "awaiting_acquire"
	case r.Phase == run.PhaseReleasing && hasHookError(r):
		v.DerivedLabel = "needs_restore"
	}
	return v
}

func hasHookError(r run.Run) bool {
	for _, h := range r.Hooks {
		if h.Error != "" {
			return true
		}
	}
	return false
}

// Handler builds the chi router.
func (s *Server) Handler() http.Handler {
	mux := chi.NewRouter()
	mux.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Route("/v1", func(r chi.Router) {
		r.Use(s.authorize)
		r.Post("/runs", s.postRun)
		r.Get("/runs/{id}", s.getRun)
		// Evidence lives on the authenticated control API, never on the
		// preview listener: measurements are not part of the visual payload.
		r.Get("/runs/{id}/metrics", s.getMetrics)
	})
	return mux
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if s.Token == "" {
			next.ServeHTTP(w, req)
			return
		}
		auth := req.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) || strings.TrimPrefix(auth, prefix) != s.Token {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, req)
	})
}

func (s *Server) postRun(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Experiment  string `json:"experiment"`
		InputCommit string `json:"input_commit"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Experiment == "" {
		writeError(w, http.StatusBadRequest, "experiment is required")
		return
	}
	r, err := s.Service.Submit(req.Context(), body.Experiment, body.InputCommit)
	switch {
	case errors.Is(err, ErrLocalForbidden):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case isBadRequest(err):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		s.log().Error(err, "submit failed", "experiment", body.Experiment)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": r.ID})
}

func (s *Server) getRun(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	r, err := s.Service.Status(req.Context(), id)
	switch {
	case errors.Is(err, run.ErrNotFound):
		writeError(w, http.StatusNotFound, "unknown run")
		return
	case err != nil:
		s.log().Error(err, "status failed", "run", id)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, NewStatusView(r))
}

// isBadRequest reports whether err is a caller mistake. Only errors the
// service explicitly classifies as BadRequestError qualify: a bare
// *os.PathError may just as well come from the controller's own state
// directory (EACCES, EIO) and must stay a 500.
func isBadRequest(err error) bool {
	var bad *BadRequestError
	return errors.As(err, &bad)
}

// BadRequestError marks caller mistakes so handlers can map them to 400.
type BadRequestError struct{ Err error }

func (e *BadRequestError) Error() string { return e.Err.Error() }
func (e *BadRequestError) Unwrap() error { return e.Err }

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) log() logr.Logger {
	if s.Log.GetSink() == nil {
		return logr.Discard()
	}
	return s.Log
}

// getMetrics serves the sealed measurement evidence as JSON.
func (s *Server) getMetrics(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	body, err := s.Service.Metrics(req.Context(), id)
	switch {
	case errors.Is(err, run.ErrNotFound), errors.Is(err, ErrNoEvidence):
		writeError(w, http.StatusNotFound, "no evidence for this run")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not read evidence")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
