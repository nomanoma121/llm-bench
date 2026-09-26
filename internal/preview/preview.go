// Package preview serves candidate artifacts over a dedicated, unauthenticated
// listener that is meant to sit behind an Ingress terminating cluster
// authentication (docs/architecture.md §4.9).
//
// It deliberately shares nothing with the control API: the handler reads run
// records and the artifact directory only, never the sandbox client, so a
// preview keeps working after the Sandbox is gone.
package preview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nomanoma121/llm-bench/internal/provenance"
	"github.com/nomanoma121/llm-bench/internal/run"
)

// MaxFileSize bounds a single served file. The payload is a single
// self-contained index.html, so this also bounds the whole payload.
const MaxFileSize = 8 << 20 // 8 MiB

// CSP is sent with every artifact response. `sandbox allow-scripts` (without
// allow-same-origin) makes the document opaque, and the remaining directives
// block programmatic network access, forms, objects and workers. Self
// navigation by the generated page is not covered: the listener must not be
// placed on an origin that carries ambient credentials.
const CSP = "sandbox allow-scripts; default-src 'none'; " +
	"script-src 'unsafe-inline' https://cdn.jsdelivr.net; " +
	"style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; " +
	"form-action 'none'; object-src 'none'; frame-src 'none'; " +
	"base-uri 'none'; worker-src 'none'; manifest-src 'none'"

// RunStore is the slice of the run store the preview needs. A non-empty
// Artifacts.ArtifactDigest is what makes an artifact sealed and servable.
type RunStore interface {
	LoadRun(ctx context.Context, id string) (run.Run, error)
}

// Handler serves GET|HEAD /v1/runs/{id}/artifacts/{path...}.
type Handler struct {
	Store        RunStore
	ArtifactsDir func(runID string) string
}

// ServeHTTP implements the preview contract.
func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	runID, rel, ok := parsePath(req.URL.Path)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if h.Store == nil || h.ArtifactsDir == nil {
		http.Error(w, "preview is not configured", http.StatusServiceUnavailable)
		return
	}
	record, err := h.Store.LoadRun(req.Context(), runID)
	if err != nil || record.Artifacts.ArtifactDigest == "" {
		// Unsealed or unknown runs are indistinguishable from outside.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	dir := filepath.Join(h.ArtifactsDir(runID), "output")

	body, err := readConfined(dir, rel)
	switch {
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, "not found", http.StatusNotFound)
		return
	case errors.Is(err, errTooLarge):
		http.Error(w, "artifact too large", http.StatusRequestEntityTooLarge)
		return
	case err != nil:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Recompute the payload digest with the bytes we are about to return: a
	// response can therefore only contain bytes covered by the verified
	// digest. A mismatch means the artifact changed after it was sealed.
	digest, err := provenance.ArtifactDigestWithOverride(dir, rel, body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if digest != record.Artifacts.ArtifactDigest {
		http.Error(w, "artifact changed after sealing", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Security-Policy", CSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", contentType(rel))
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if req.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

var errTooLarge = errors.New("preview: file exceeds the size limit")

// parsePath splits /v1/runs/<id>/artifacts/<path...>. An empty path serves
// index.html so that a run's preview URL can be opened directly.
func parsePath(urlPath string) (runID, rel string, ok bool) {
	const prefix = "/v1/runs/"
	if !strings.HasPrefix(urlPath, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(urlPath, prefix)
	segs := strings.SplitN(rest, "/", 3)
	if len(segs) < 2 || segs[0] == "" || segs[1] != "artifacts" {
		return "", "", false
	}
	runID = segs[0]
	if strings.ContainsAny(runID, `/\`) || runID == "." || runID == ".." {
		return "", "", false
	}
	if len(segs) == 3 {
		rel = segs[2]
	}
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "index.html"
	}
	if !safeRel(rel) {
		return "", "", false
	}
	return runID, rel, true
}

// safeRel rejects absolute paths, parent traversal, backslashes and NUL bytes.
// The URL is already percent-decoded by net/http, so a decoded value is
// checked here.
func safeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.ContainsAny(rel, "\\\x00") {
		return false
	}
	if path.IsAbs(rel) || path.Clean(rel) != rel {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// readConfined reads a payload file, refusing to leave dir. os.OpenRoot keeps
// the resolution under dir even if a component is swapped for a symlink, and
// rejects symlinks in the path.
func readConfined(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	if info.Size() > MaxFileSize {
		return nil, errTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxFileSize {
		return nil, errTooLarge
	}
	return body, nil
}

// contentType maps a payload file to a content type. Unknown extensions are
// served as octet-stream with nosniff, so the browser never sniffs them.
func contentType(rel string) string {
	if ct := mime.TypeByExtension(path.Ext(rel)); ct != "" {
		return ct
	}
	switch path.Ext(rel) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	}
	return "application/octet-stream"
}
