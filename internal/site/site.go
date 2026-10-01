package site

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
)

const artifactCSP = "default-src 'none'; script-src 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'"

const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src 'self'; frame-src 'self' about:; base-uri 'none'"

type experiment struct {
	Model   string
	ID      string
	URL     string
	Result  *benchmark.Result
	Samples []benchmark.Sample
	SrcDoc  string
	Charts  []template.HTML
}

type row struct {
	URL     string
	ID      string
	Model   string
	Runtime string
	Args    string
	GPU     string
	Case    string
	Decode  string
	Prefill string
	TTFT    string
	VRAM    string
	Valid   string
}

const style = `<style>
:root{color-scheme:light;--surface:#fcfcfb;--text:#0b0b0b;--text-2:#52514e;--muted:#898781;--grid:#e1e0d9;--axis:#c3c2b7;--series-1:#2a78d6;--series-2:#eb6834}
@media (prefers-color-scheme:dark){:root{color-scheme:dark;--surface:#1a1a19;--text:#fff;--text-2:#c3c2b7;--muted:#898781;--grid:#2c2c2a;--axis:#383835;--series-1:#3987e5;--series-2:#d95926}}
body{margin:1.5rem auto;max-width:1100px;padding:0 1rem;background:var(--surface);color:var(--text);font:14px/1.5 system-ui,sans-serif}
a{color:inherit}h1{font-size:1.2rem;font-weight:600}.muted{color:var(--muted)}
table{border-collapse:collapse;width:100%}th,td{padding:.25rem .6rem;border-bottom:1px solid var(--grid);text-align:left;white-space:nowrap}.scroll{overflow-x:auto}
th{color:var(--text-2);font-weight:500}th[data-sort]{cursor:pointer}td.num,th.num{text-align:right;font-variant-numeric:tabular-nums}
.filters{display:flex;gap:1rem;flex-wrap:wrap;margin:1rem 0;color:var(--text-2)}
figure{margin:1.5rem 0}figcaption{color:var(--text-2);margin-bottom:.25rem}svg{width:100%;max-width:760px;height:auto;overflow:visible}
.grid{stroke:var(--grid)}.tick,.label{fill:var(--muted);font-size:11px}.label{fill:var(--text-2)}
.series{fill:none;stroke-width:2}.hit{fill:transparent}.bar{fill:var(--series-1)}
.legend{display:flex;gap:1rem;color:var(--text-2)}.legend i{display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:4px}
iframe{width:100%;height:80vh;border:1px solid var(--grid)}dl{display:grid;grid-template-columns:max-content auto;gap:.2rem 1rem}dt{color:var(--text-2)}dd{margin:0}
</style>`

var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta http-equiv="Content-Security-Policy" content="` + pageCSP + `">
<title>{{.Model}} / {{.ID}}</title>` + style + `
</head><body>
<p><a href="../../index.html">llm-bench</a></p>
<h1>{{.Model}} / {{.ID}}</h1>
{{with .Result}}
<dl>
<dt>runtime</dt><dd>{{.Runtime.Label}}</dd>
<dt>args</dt><dd>{{range .Runtime.Args}}{{.}} {{end}}</dd>
<dt>gpu</dt><dd>{{range .GPUs}}{{.}} {{end}}{{if .Driver}}(driver {{.Driver}}){{end}}</dd>
<dt>model digest</dt><dd>{{.Model.Digest}}</dd>
<dt>started</dt><dd>{{.StartedAt.Format "2006-01-02 15:04 UTC"}} ({{printf "%.0f" .DurationSeconds}}s)</dd>
<dt>measurement valid</dt><dd>{{.MeasurementValid}}{{range .InvalidReasons}}<br>{{.}}{{end}}</dd>
</dl>
<h2>Metrics</h2>
<table><tr><th>case</th><th>metric</th><th class="num">value</th><th class="num">min</th><th class="num">max</th><th class="num">n</th></tr>
{{range .Metrics}}<tr><td>{{.Case}}</td><td>{{.Name}} <span class="muted">{{.Unit}}</span></td><td class="num">{{printf "%.2f" .Value}}</td><td class="num">{{printf "%.2f" .Min}}</td><td class="num">{{printf "%.2f" .Max}}</td><td class="num">{{.Samples}}</td></tr>
{{end}}</table>
{{end}}
{{range .Charts}}{{.}}{{end}}
{{if .SrcDoc}}<h2>Output</h2><iframe sandbox="allow-scripts" srcdoc="{{.SrcDoc}}"></iframe>{{end}}
</body></html>
`))

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta http-equiv="Content-Security-Policy" content="` + pageCSP + `">
<title>llm-bench</title>` + style + `
<script src="app.js" defer></script>
</head><body>
<h1>llm-bench</h1>
<div class="filters" id="filters"></div>
<div id="chart"></div>
<div class="scroll"><table id="results"><thead><tr>
<th data-sort="date">experiment</th><th data-sort="model">model</th><th data-sort="runtime">runtime</th><th>args</th><th data-sort="gpu">gpu</th><th>case</th>
<th class="num" data-sort="decode">decode tok/s</th><th class="num" data-sort="prefill">prefill tok/s</th><th class="num" data-sort="ttft">ttft ms</th><th class="num" data-sort="vram">vram MiB</th><th>valid</th>
</tr></thead><tbody>
{{range .}}<tr data-date="{{.ID}}" data-model="{{.Model}}" data-runtime="{{.Runtime}}" data-gpu="{{.GPU}}" data-valid="{{.Valid}}" data-decode="{{.Decode}}" data-prefill="{{.Prefill}}" data-ttft="{{.TTFT}}" data-vram="{{.VRAM}}" data-label="{{.ID}} {{.Case}}">
<td><a href="{{.URL}}">{{.ID}}</a></td><td>{{.Model}}</td><td>{{.Runtime}}</td><td class="muted">{{.Args}}</td><td>{{.GPU}}</td><td>{{.Case}}</td>
<td class="num">{{.Decode}}</td><td class="num">{{.Prefill}}</td><td class="num">{{.TTFT}}</td><td class="num">{{.VRAM}}</td><td>{{.Valid}}</td></tr>
{{end}}</tbody></table></div>
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
			e.Samples = loadSamples(filepath.Join(dir, "series.jsonl"))
			e.Charts = charts(e.Samples)
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
	var rows []row
	for _, e := range experiments {
		if err := render(filepath.Join(out, filepath.FromSlash(e.URL)), pageTemplate, e); err != nil {
			return err
		}
		rows = append(rows, rowsOf(e)...)
	}
	if err := render(filepath.Join(out, "index.html"), indexTemplate, rows); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "app.js"), []byte(appJS), 0o644)
}

func rowsOf(e experiment) []row {
	r := e.Result
	if r == nil {
		return []row{{URL: e.URL, ID: e.ID, Model: e.Model}}
	}
	base := row{
		URL: e.URL, ID: e.ID, Model: e.Model,
		Runtime: r.Runtime.Label(),
		Args:    strings.Join(r.Runtime.Args, " "),
		GPU:     strings.Join(unique(r.GPUs), ", "),
		Valid:   map[bool]string{true: "yes", false: "no"}[r.MeasurementValid],
	}
	if m, ok := r.Metric("vram_used_mib", ""); ok {
		base.VRAM = number(m.Value)
	}
	var cases []string
	for _, m := range r.Metrics {
		if m.Case != "" && !contains(cases, m.Case) {
			cases = append(cases, m.Case)
		}
	}
	var rows []row
	for _, c := range cases {
		rw := base
		rw.Case = c
		if m, ok := r.Metric("decode_tok_per_s", c); ok {
			rw.Decode = number(m.Value)
		}
		if m, ok := r.Metric("prefill_tok_per_s", c); ok {
			rw.Prefill = number(m.Value)
		}
		if m, ok := r.Metric("ttft_ms", c); ok {
			rw.TTFT = number(m.Value)
		}
		rows = append(rows, rw)
	}
	if len(rows) == 0 {
		rows = append(rows, base)
	}
	return rows
}

func charts(samples []benchmark.Sample) []template.HTML {
	var out []template.HTML
	for _, metric := range []struct{ name, title, unit string }{
		{"vram_used_mib", "VRAM used", "MiB"},
		{"gpu_util_percent", "GPU utilization", "%"},
	} {
		var lines []line
		seen := map[string]int{}
		for _, s := range samples {
			if s.Source != "gpu" || s.Name != metric.name {
				continue
			}
			key := fmt.Sprintf("%d/%s", s.AtMS, s.Labels["gpu"])
			index := seen[key]
			seen[key]++
			if id, err := strconv.Atoi(s.Labels["gpu"]); err == nil {
				index = id
			}
			for len(lines) <= index {
				lines = append(lines, line{Name: fmt.Sprintf("GPU %d", len(lines))})
			}
			lines[index].Points = append(lines[index].Points, [2]float64{float64(s.AtMS) / 1000, s.Value})
		}
		if c := lineChart(metric.title, metric.unit, lines); c != "" {
			out = append(out, c)
		}
	}
	for _, metric := range []struct{ name, title, unit string }{
		{"decode_tok_per_s", "Decode per repeat", "tok/s"},
		{"ttft_ms", "Time to first token per repeat", "ms"},
	} {
		var bars []bar
		for _, s := range samples {
			if s.Source == "harness" && s.Name == metric.name {
				bars = append(bars, bar{Label: fmt.Sprintf("%s #%d", s.Case, s.Repeat), Value: s.Value})
			}
		}
		if c := barChart(metric.title, metric.unit, bars); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func loadSamples(path string) []benchmark.Sample {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []benchmark.Sample
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var s benchmark.Sample
		if json.Unmarshal(scanner.Bytes(), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func unique(values []string) []string {
	var out []string
	for _, v := range values {
		if !contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
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
