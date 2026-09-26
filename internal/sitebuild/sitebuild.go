// Package sitebuild generates the static publication site from adopted
// artifacts (docs/architecture.md §4.12).
//
// Only experiments with a valid manifest are published, and the raw generated
// HTML is never written to a directly navigable path: each page embeds it into
// a sandboxed iframe via srcdoc, with a meta CSP that blocks programmatic
// network access. Everything is generated from bytes read out of git, so the
// published site never depends on a preview URL or a running controller.
package sitebuild

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nomanoma121/llm-bench/internal/adopt"
)

// MetaCSP is the policy inserted at the top of the framed artifact document.
// The iframe's sandbox attribute isolates the origin; this policy restricts
// what the embedded page can do once it runs (the sandbox directive itself is
// header-only, so the CD host relies on the attribute plus this meta tag).
const MetaCSP = "default-src 'none'; script-src 'unsafe-inline' https://cdn.jsdelivr.net; " +
	"style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; " +
	"form-action 'none'; object-src 'none'; frame-src 'none'; base-uri 'none'; worker-src 'none'"

// WrapperCSP keeps the wrapper page itself inert while allowing the iframe.
const WrapperCSP = "default-src 'none'; style-src 'unsafe-inline'; frame-src 'self' about:; base-uri 'none'"

// BuildOptions configures a site build.
type BuildOptions struct {
	ExperimentsRoot string
	Out             string
}

// page is the data for the experiment wrapper template.
type page struct {
	Title        string
	Model        string
	ExperimentID string
	AdoptedAt    string
	RunID        string
	Digest       string
	ReviewURL    string
	Reviewed     bool
	// SRCDoc is untrusted artifact HTML. It stays a plain string so
	// html/template escapes it as an attribute value: a template.HTMLAttr
	// would disable escaping and let the artifact close the attribute.
	SRCDoc string
}

// indexPage is the data for the gallery index.
type indexPage struct {
	Pages []indexEntry
}

type indexEntry struct {
	Model        string
	ExperimentID string
	AdoptedAt    string
	Reviewed     bool
	URL          string
}

var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="` + WrapperCSP + `">
<title>{{.Title}}</title>
<style>body{font-family:system-ui,sans-serif;margin:1.5rem}iframe{width:100%;height:80vh;border:1px solid #ccc}dl{display:grid;grid-template-columns:max-content auto;gap:.25rem 1rem}dt{font-weight:600}</style>
</head>
<body>
<h1>{{.Title}}</h1>
<dl>
<dt>Model</dt><dd>{{.Model}}</dd>
<dt>Experiment</dt><dd>{{.ExperimentID}}</dd>
<dt>Adopted</dt><dd>{{.AdoptedAt}}</dd>
<dt>Run</dt><dd>{{.RunID}}</dd>
<dt>Artifact digest</dt><dd><code>{{.Digest}}</code></dd>
<dt>Review</dt><dd>{{if .Reviewed}}<a href="{{.ReviewURL}}">Issue decision</a>{{else}}not reviewed{{end}}</dd>
</dl>
<iframe sandbox="allow-scripts" srcdoc="{{.SRCDoc}}"></iframe>
</body>
</html>
`))

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="` + WrapperCSP + `">
<title>llm-bench results</title>
<style>body{font-family:system-ui,sans-serif;margin:1.5rem}table{border-collapse:collapse}td,th{padding:.35rem .75rem;border-bottom:1px solid #ddd;text-align:left}</style>
</head>
<body>
<h1>llm-bench results</h1>
<p>Only artifacts adopted from reviewed runs are published here.</p>
<table>
<thead><tr><th>Model</th><th>Experiment</th><th>Adopted</th><th>Review</th></tr></thead>
<tbody>
{{range .Pages}}<tr><td>{{.Model}}</td><td><a href="{{.URL}}">{{.ExperimentID}}</a></td><td>{{.AdoptedAt}}</td><td>{{if .Reviewed}}reviewed{{else}}not reviewed{{end}}</td></tr>
{{end}}</tbody>
</table>
</body>
</html>
`))

// Build writes the site to opts.Out. It is deterministic: the same manifests
// produce byte-identical output.
func Build(opts BuildOptions) error {
	if opts.Out == "" || opts.ExperimentsRoot == "" {
		return fmt.Errorf("sitebuild: experiments root and output directory are required")
	}
	adopted, err := adopt.Find(opts.ExperimentsRoot)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(opts.Out); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.Out, 0o755); err != nil {
		return err
	}
	index := indexPage{}
	for _, a := range adopted {
		payload, err := a.Payload()
		if err != nil {
			return err
		}
		title := a.Model + " / " + a.ExperimentID
		data := page{
			Title:        title,
			Model:        a.Model,
			ExperimentID: a.ExperimentID,
			AdoptedAt:    a.Manifest.AdoptedAt.UTC().Format("2006-01-02 15:04:05Z"),
			RunID:        a.Manifest.RunID,
			Digest:       a.Manifest.ArtifactDigest,
			Reviewed:     a.Manifest.Review != nil,
			SRCDoc:       withMetaCSP(payload),
		}
		if a.Manifest.Review != nil {
			data.ReviewURL = a.Manifest.Review.IssueURL
		}
		rel := path.Join(a.Model, a.ExperimentID, "index.html")
		if err := writeFile(filepath.Join(opts.Out, filepath.FromSlash(rel)), pageTemplate, data); err != nil {
			return err
		}
		index.Pages = append(index.Pages, indexEntry{
			Model:        a.Model,
			ExperimentID: a.ExperimentID,
			AdoptedAt:    data.AdoptedAt,
			Reviewed:     data.Reviewed,
			URL:          rel,
		})
	}
	return writeFile(filepath.Join(opts.Out, "index.html"), indexTemplate, index)
}

// withMetaCSP inserts the meta policy before any artifact content, so nothing
// runs before it applies. A document without <head> gets it at the very top.
func withMetaCSP(payload []byte) string {
	meta := []byte(`<meta http-equiv="Content-Security-Policy" content="` + MetaCSP + `">`)
	lower := bytes.ToLower(payload)
	if i := bytes.Index(lower, []byte("<head")); i >= 0 {
		if end := bytes.IndexByte(lower[i:], '>'); end >= 0 {
			insert := i + end + 1
			out := make([]byte, 0, len(payload)+len(meta))
			out = append(out, payload[:insert]...)
			out = append(out, meta...)
			out = append(out, payload[insert:]...)
			return string(out)
		}
	}
	return string(meta) + string(payload)
}

// writeFile renders a template to a file, creating parent directories.
func writeFile(dest string, tmpl *template.Template, data any) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := syncFile(dest); err != nil {
		return err
	}
	return nil
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// VerifyOutput fails when the generated tree exposes a raw artifact: the
// published site must only contain wrapper pages and the gallery index, never
// a copy of the untrusted HTML that could be opened directly (which would
// bypass the iframe sandbox).
func VerifyOutput(out string) error {
	return filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(out, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		switch {
		case rel == "index.html":
			// The gallery lists experiments and embeds nothing.
			if bytes.Contains(body, []byte("srcdoc=")) {
				return fmt.Errorf("sitebuild: the gallery index must not embed an artifact")
			}
			return nil
		case strings.HasSuffix(rel, "/index.html"):
			if !bytes.Contains(body, []byte(`sandbox="allow-scripts"`)) ||
				!bytes.Contains(body, []byte("srcdoc=")) {
				return fmt.Errorf("sitebuild: %s does not embed its artifact in a sandboxed iframe", rel)
			}
			return nil
		default:
			return fmt.Errorf("sitebuild: unexpected published file %s", rel)
		}
	})
}
