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
	"strings"

	"github.com/nomanoma121/llm-bench/internal/benchmark"
)

const artifactCSP = "default-src 'none'; script-src 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'"

const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-src 'self' about:; base-uri 'none'"

type experiment struct {
	Model   string
	ID      string
	URL     string
	Result  *benchmark.Result
	Samples []benchmark.Sample
	Outputs []output
	Charts  []section
}

type output struct {
	Name   string
	SrcDoc string
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
	Accept  string
	Step    string
	ITL     string
	Energy  string
	VRAM    string
	Valid   string
	Outputs int
}

const style = `<style>
:root{color-scheme:light;--surface:#fcfcfb;--text:#0b0b0b;--text-2:#52514e;--muted:#898781;--grid:#e1e0d9;--axis:#c3c2b7;--series-1:#2a78d6;--series-2:#eb6834;--series-3:#1baf7a}
@media (prefers-color-scheme:dark){:root{color-scheme:dark;--surface:#1a1a19;--text:#fff;--text-2:#c3c2b7;--muted:#898781;--grid:#2c2c2a;--axis:#383835;--series-1:#3987e5;--series-2:#d95926;--series-3:#199e70}}
body{margin:1.5rem auto;max-width:1100px;padding:0 1rem;background:var(--surface);color:var(--text);font:14px/1.5 system-ui,sans-serif}
a{color:inherit}h1{font-size:1.2rem;font-weight:600}.muted{color:var(--muted)}
table{border-collapse:collapse;width:100%}th,td{padding:.25rem .6rem;border-bottom:1px solid var(--grid);text-align:left;white-space:nowrap}.scroll{overflow-x:auto}
th{color:var(--text-2);font-weight:500}th[data-sort]{cursor:pointer}td.num,th.num{text-align:right;font-variant-numeric:tabular-nums}
.filters{display:flex;gap:1rem;flex-wrap:wrap;margin:1rem 0;color:var(--text-2)}
figure{margin:1.5rem 0}figcaption{color:var(--text-2);margin-bottom:.25rem}svg{width:100%;max-width:760px;height:auto;overflow:visible}
.grid{stroke:var(--grid)}.tick,.label{fill:var(--muted);font-size:11px}.label{fill:var(--text-2)}
.series{fill:none;stroke-width:2}.hit{fill:transparent}.bar{fill:var(--series-1)}
.legend{display:flex;gap:1rem;color:var(--text-2)}.legend i{display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:4px}
iframe{width:100%;height:60vh;border:1px solid var(--grid);background:#fff}.output{margin:0 0 1rem}.output iframe{height:auto;aspect-ratio:16/10}
td.args{max-width:14rem;overflow:hidden;text-overflow:ellipsis}
dl{display:grid;grid-template-columns:max-content auto;gap:.2rem 1rem}dt{color:var(--text-2)}dd{margin:0}
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
{{end}}
{{if .Outputs}}<h2 id="outputs">Output</h2>{{range .Outputs}}<figure class="output"><figcaption>{{.Name}}</figcaption><iframe sandbox="allow-scripts" loading="lazy" srcdoc="{{.SrcDoc}}"></iframe></figure>{{end}}{{end}}
{{with .Result}}
{{with .Runtime.Info}}<h2>Runtime</h2>
<table>{{range $k, $v := .}}<tr><td class="muted">{{$k}}</td><td>{{$v}}</td></tr>{{end}}</table>{{end}}
<h2>Metrics</h2>
<table><tr><th>case</th><th>metric</th><th class="num">value</th><th class="num">min</th><th class="num">max</th><th class="num">n</th></tr>
{{range .Metrics}}<tr><td>{{.Case}}</td><td>{{.Name}} <span class="muted">{{.Unit}}</span></td><td class="num">{{printf "%.2f" .Value}}</td><td class="num">{{printf "%.2f" .Min}}</td><td class="num">{{printf "%.2f" .Max}}</td><td class="num">{{.Samples}}</td></tr>
{{end}}</table>
{{end}}
{{range .Charts}}<h2>{{.Title}}</h2>{{range .Charts}}{{.}}{{end}}{{end}}
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
<p><button id="compare" disabled>Compare selected</button> <span class="muted">up to 3</span></p>
<div class="scroll"><table id="results"><thead><tr>
<th></th><th data-sort="date">experiment</th><th data-sort="model">model</th><th data-sort="runtime">runtime</th><th>args</th><th data-sort="gpu">gpu</th><th>case</th>
<th class="num" data-sort="decode">decode tok/s</th><th class="num" data-sort="prefill">prefill tok/s</th><th class="num" data-sort="ttft">ttft ms</th><th class="num" data-sort="itl">itl p95 ms</th><th class="num" data-sort="accept">mtp accept</th><th class="num" data-sort="step">tok/step</th><th class="num" data-sort="energy">J/tok</th><th class="num" data-sort="vram">vram MiB</th><th>valid</th><th class="num">outputs</th>
</tr></thead><tbody>
{{range .}}<tr data-date="{{.ID}}" data-model="{{.Model}}" data-runtime="{{.Runtime}}" data-gpu="{{.GPU}}" data-valid="{{.Valid}}" data-decode="{{.Decode}}" data-prefill="{{.Prefill}}" data-ttft="{{.TTFT}}" data-itl="{{.ITL}}" data-accept="{{.Accept}}" data-step="{{.Step}}" data-energy="{{.Energy}}" data-vram="{{.VRAM}}">
<td><input type="checkbox" class="pick" value="{{.Model}}/{{.ID}}"></td><td><a href="{{.URL}}">{{.ID}}</a></td><td>{{.Model}}</td><td>{{.Runtime}}</td><td class="muted args" title="{{.Args}}">{{.Args}}</td><td>{{.GPU}}</td><td>{{.Case}}</td>
<td class="num">{{.Decode}}</td><td class="num">{{.Prefill}}</td><td class="num">{{.TTFT}}</td><td class="num">{{.ITL}}</td><td class="num">{{.Accept}}</td><td class="num">{{.Step}}</td><td class="num">{{.Energy}}</td><td class="num">{{.VRAM}}</td><td>{{.Valid}}</td><td class="num">{{if .Outputs}}<a href="{{.URL}}#outputs">{{.Outputs}}</a>{{end}}</td></tr>
{{end}}</tbody></table></div>
</body></html>
`))

var compareTemplate = template.Must(template.New("compare").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta http-equiv="Content-Security-Policy" content="` + pageCSP + `">
<title>Compare - llm-bench</title>` + style + `
<script src="compare.js" defer></script>
</head><body>
<p><a href="index.html">llm-bench</a></p>
<h1>Compare</h1>
<table id="runs"><thead><tr><th>experiment</th><th>model</th><th>runtime</th><th>args</th><th class="num">decode tok/s</th><th class="num">vs first</th><th class="num">J/tok</th></tr></thead><tbody></tbody></table>
<div id="charts"></div>
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
			e.Charts = charts(r, e.Samples)
		}
		files, _ := filepath.Glob(filepath.Join(dir, "output", "*.html"))
		for _, f := range files {
			if html, err := os.ReadFile(f); err == nil && len(html) > 0 {
				e.Outputs = append(e.Outputs, output{
					Name:   strings.TrimSuffix(filepath.Base(f), ".html"),
					SrcDoc: `<meta http-equiv="Content-Security-Policy" content="` + artifactCSP + `">` + string(html),
				})
			}
		}
		if e.Result == nil && len(e.Outputs) == 0 {
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
		if e.Result != nil {
			d := compareData{
				ID: e.ID, Model: e.Model, Runtime: e.Result.Runtime.Label(),
				Args: strings.Join(e.Result.Runtime.Args, " "), Series: compareSeries(e.Samples),
			}
			if m, ok := firstCase(*e.Result, "decode_tok_per_s"); ok {
				d.Decode = m.Value
			}
			if m, ok := firstCase(*e.Result, "energy_j_per_token"); ok {
				d.Energy = m.Value
			}
			data, err := json.Marshal(d)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, e.Model, e.ID, "data.json"), data, 0o644); err != nil {
				return err
			}
		}
		rows = append(rows, rowsOf(e)...)
	}
	if err := render(filepath.Join(out, "index.html"), indexTemplate, rows); err != nil {
		return err
	}
	if err := render(filepath.Join(out, "compare.html"), compareTemplate, nil); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "compare.js"), []byte(compareJS), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "app.js"), []byte(appJS), 0o644)
}

func rowsOf(e experiment) []row {
	r := e.Result
	if r == nil {
		return []row{{URL: e.URL, ID: e.ID, Model: e.Model, Outputs: len(e.Outputs)}}
	}
	base := row{
		URL: e.URL, ID: e.ID, Model: e.Model,
		Runtime: r.Runtime.Label(),
		Args:    strings.Join(r.Runtime.Args, " "),
		GPU:     strings.Join(unique(r.GPUs), ", "),
		Valid:   map[bool]string{true: "yes", false: "no"}[r.MeasurementValid],
		Outputs: len(e.Outputs),
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
		for _, col := range []struct {
			metric string
			cell   *string
		}{
			{"decode_tok_per_s", &rw.Decode},
			{"prefill_tok_per_s", &rw.Prefill},
			{"ttft_ms", &rw.TTFT},
			{"itl_ms_p95", &rw.ITL},
			{"draft_acceptance", &rw.Accept},
			{"mtp_accept_len", &rw.Step},
			{"energy_j_per_token", &rw.Energy},
		} {
			if m, ok := r.Metric(col.metric, c); ok {
				*col.cell = number(m.Value)
			}
		}
		rows = append(rows, rw)
	}
	if len(rows) == 0 {
		rows = append(rows, base)
	}
	return rows
}

type section struct {
	Title  string
	Charts []template.HTML
}

func charts(r benchmark.Result, samples []benchmark.Sample) []section {
	var out []section
	group := func(title string, cs ...template.HTML) {
		var kept []template.HTML
		for _, c := range cs {
			if c != "" {
				kept = append(kept, c)
			}
		}
		if len(kept) > 0 {
			out = append(out, section{Title: title, Charts: kept})
		}
	}
	single := func(title, unit string, points [][2]float64) template.HTML {
		return lineChart(title, unit, []line{{Name: title, Points: points}})
	}
	perRepeat := func(name, title, unit string) template.HTML {
		var bars []bar
		for _, s := range samples {
			if s.Source == "harness" && s.Name == name {
				bars = append(bars, bar{Label: fmt.Sprintf("%s #%d", s.Case, s.Repeat), Value: s.Value})
			}
		}
		return barChart(title, unit, bars)
	}
	decode := decodeOverTime(samples)
	group("Throughput",
		single("Decode speed over time", "tok/s", decode),
		barChart("Decode speed distribution", "tok/s", distribution(decode)),
		barChart("Inter-token latency", "ms", metricBars(r, "itl_ms_")),
		perRepeat("prefill_tok_per_s", "Prefill per repeat", "tok/s"),
	)
	group("MTP",
		single("Draft acceptance, cumulative", "ratio", ratioOverTime(samples, "llamacpp:spec_decode_num_accepted_tokens_total", "llamacpp:spec_decode_num_draft_tokens_total")),
		single("Tokens per verification step, cumulative", "tokens", plusOne(ratioOverTime(samples, "llamacpp:spec_decode_num_accepted_tokens_total", "llamacpp:spec_decode_num_drafts_total"))),
		barChart("Draft acceptance by position", "ratio", acceptanceByPosition(samples)),
		perRepeat("draft_acceptance", "Draft acceptance per repeat", "ratio"),
	)
	group("Context and requests",
		single("Context used", "tokens", runtimeSeries(samples, "llamacpp:context_tokens")),
		single("Prompt cache hit, cumulative", "ratio", ratioOverTime(samples, "llamacpp:prompt_tokens_cached_total", "llamacpp:prompt_tokens_total")),
		single("Busy slots per decode", "slots", runtimeSeries(samples, "llamacpp:n_busy_slots_per_decode")),
		lineChart("Requests", "requests", []line{
			{Name: "processing", Points: runtimeSeries(samples, "llamacpp:requests_processing")},
			{Name: "deferred", Points: runtimeSeries(samples, "llamacpp:requests_deferred")},
		}),
	)
	group("GPU",
		lineChart("VRAM used", "MiB", perGPU(samples, "vram_used_mib")),
		lineChart("GPU utilization", "%", perGPU(samples, "gpu_util_percent")),
		lineChart("Memory controller utilization (DRAM activity)", "%", perGPU(samples, "mem_util_percent")),
		lineChart("SM clock", "MHz", perGPU(samples, "sm_clock_mhz")),
		lineChart("Clock throttled for power or heat", "1 = throttled", perGPU(samples, "throttled")),
		lineChart("Power", "W", perGPU(samples, "power_w")),
		lineChart("Temperature", "°C", perGPU(samples, "temperature_c")),
		lineChart("PCIe RX", "MB/s", perGPU(samples, "pcie_rx_mbps")),
		lineChart("PCIe TX", "MB/s", perGPU(samples, "pcie_tx_mbps")),
	)
	group("Host",
		lineChart("CPU utilization", "%", []line{
			{Name: "all cores", Points: hostSeries(samples, "cpu_util_percent")},
			{Name: "busiest core", Points: hostSeries(samples, "cpu_max_core_percent")},
		}),
		single("RAM used", "MiB", hostSeries(samples, "ram_used_mib")),
	)
	group("Memory", barChart("Memory by buffer (all devices)", "MiB", memoryBars(r.Runtime.Info)))
	group("Repeats",
		perRepeat("decode_tok_per_s", "Decode per repeat", "tok/s"),
		perRepeat("ttft_ms", "Time to first token per repeat", "ms"),
	)
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

// firstCase returns the named metric of the first workload case that has it.
func firstCase(r benchmark.Result, name string) (benchmark.Metric, bool) {
	for _, m := range r.Metrics {
		if m.Name == name {
			return m, true
		}
	}
	return benchmark.Metric{}, false
}
