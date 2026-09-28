package site

import (
	"bytes"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
)

const artifactCSP = "default-src 'none'; script-src 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'"

const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; frame-src 'self' about:; base-uri 'none'"

type experiment struct {
	Model  string
	ID     string
	URL    string
	Result *benchmark.Result
	SrcDoc string
}

var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="` + pageCSP + `">
<title>{{.Model}} / {{.ID}}</title>
<style>body{font-family:system-ui,sans-serif;margin:1.5rem}table{border-collapse:collapse}td,th{padding:.3rem .7rem;border-bottom:1px solid #ddd;text-align:left}iframe{width:100%;height:80vh;border:1px solid #ccc}</style>
</head><body>
<p><a href="../../index.html">llm-bench</a></p>
<h1>{{.Model}} / {{.ID}}</h1>
{{with .Result}}
<p>{{.Runtime.Label}} {{range .Runtime.Args}}{{.}} {{end}}· {{range .GPUs}}{{.}} {{end}}· measurement valid: {{.MeasurementValid}}</p>
<table><tr><th>case</th><th>metric</th><th>value</th><th>min</th><th>max</th><th>n</th></tr>
{{range .Metrics}}<tr><td>{{.Case}}</td><td>{{.Name}} ({{.Unit}})</td><td>{{printf "%.2f" .Value}}</td><td>{{printf "%.2f" .Min}}</td><td>{{printf "%.2f" .Max}}</td><td>{{.Samples}}</td></tr>
{{end}}</table>
{{end}}
{{if .SrcDoc}}<iframe sandbox="allow-scripts" srcdoc="{{.SrcDoc}}"></iframe>{{end}}
</body></html>
`))

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="` + pageCSP + `">
<title>llm-bench</title>
<style>body{font-family:system-ui,sans-serif;margin:1.5rem}td,th{padding:.3rem .7rem;text-align:left}</style>
</head><body>
<h1>llm-bench</h1>
<table><tr><th>model</th><th>experiment</th><th>valid</th></tr>
{{range .}}<tr><td>{{.Model}}</td><td><a href="{{.URL}}">{{.ID}}</a></td><td>{{with .Result}}{{.MeasurementValid}}{{end}}</td></tr>
{{end}}</table>
</body></html>
`))

func Build(root, out string) error {
	var experiments []experiment
	dirs, err := filepath.Glob(filepath.Join(root, "*", "*"))
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			continue
		}
		e := experiment{Model: filepath.Base(filepath.Dir(dir)), ID: filepath.Base(dir)}
		e.URL = path.Join(e.Model, e.ID, "index.html")
		if r, err := benchmark.LoadResult(dir); err == nil {
			e.Result = &r
		}
		if html, err := os.ReadFile(filepath.Join(dir, "output", "index.html")); err == nil && len(html) > 0 {
			e.SrcDoc = `<meta http-equiv="Content-Security-Policy" content="` + artifactCSP + `">` + string(html)
		}
		if e.Result == nil && e.SrcDoc == "" {
			continue
		}
		experiments = append(experiments, e)
	}
	sort.Slice(experiments, func(i, j int) bool { return experiments[i].URL > experiments[j].URL })

	if err := os.RemoveAll(out); err != nil {
		return err
	}
	for _, e := range experiments {
		if err := render(filepath.Join(out, filepath.FromSlash(e.URL)), pageTemplate, e); err != nil {
			return err
		}
	}
	return render(filepath.Join(out, "index.html"), indexTemplate, experiments)
}

func render(dest string, t *template.Template, data any) error {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, buf.Bytes(), 0o644)
}
