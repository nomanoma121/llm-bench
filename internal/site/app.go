package site

const appJS = `const rows = [...document.querySelectorAll("#results tbody tr")];
const filters = document.getElementById("filters");
const state = {model: "", runtime: "", gpu: "", valid: false};

for (const key of ["model", "runtime", "gpu"]) {
  const values = [...new Set(rows.map(r => r.dataset[key]).filter(Boolean))].sort();
  const select = document.createElement("select");
  select.append(new Option("all", ""), ...values.map(v => new Option(v, v)));
  select.onchange = () => { state[key] = select.value; update(); };
  const label = document.createElement("label");
  label.append(key + " ", select);
  filters.append(label);
}
const valid = document.createElement("input");
valid.type = "checkbox";
valid.onchange = () => { state.valid = valid.checked; update(); };
const validLabel = document.createElement("label");
validLabel.append(valid, " valid only");
filters.append(validLabel);

let sortKey = "", sortDesc = true;
for (const th of document.querySelectorAll("th[data-sort]")) {
  th.onclick = () => {
    sortDesc = sortKey === th.dataset.sort ? !sortDesc : true;
    sortKey = th.dataset.sort;
    const numeric = th.classList.contains("num");
    rows.sort((a, b) => {
      const x = a.dataset[sortKey], y = b.dataset[sortKey];
      const c = numeric ? (parseFloat(x) || 0) - (parseFloat(y) || 0) : x.localeCompare(y);
      return sortDesc ? -c : c;
    });
    document.querySelector("#results tbody").append(...rows);
    update();
  };
}

function visible(r) {
  return (!state.model || r.dataset.model === state.model)
    && (!state.runtime || r.dataset.runtime === state.runtime)
    && (!state.gpu || r.dataset.gpu === state.gpu)
    && (!state.valid || r.dataset.valid === "yes");
}

function update() {
  for (const r of rows) r.hidden = !visible(r);
}

const picks = [...document.querySelectorAll(".pick")];
const compare = document.getElementById("compare");
for (const p of picks) p.onchange = () => {
  const chosen = picks.filter(x => x.checked);
  if (chosen.length > 3) p.checked = false;
  compare.disabled = picks.filter(x => x.checked).length < 2;
};
compare.onclick = () => {
  const ids = [...new Set(picks.filter(x => x.checked).map(x => x.value))];
  location.href = "compare.html?ids=" + encodeURIComponent(ids.join(","));
};

update();
`

const compareJS = `const ns = "http://www.w3.org/2000/svg";
const ids = (new URLSearchParams(location.search).get("ids") || "").split(",").filter(Boolean).slice(0, 3);
const metrics = [
  ["decode", "Decode speed over time", "tok/s"],
  ["context", "Context used", "tokens"],
  ["vram", "VRAM used (all GPUs)", "MiB"],
  ["util", "GPU utilization (mean)", "%"],
  ["power", "GPU power (all GPUs)", "W"],
  ["acceptance", "Draft (MTP) acceptance, cumulative", "ratio"],
];

function el(name, attrs, text) {
  const e = document.createElementNS(ns, name);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  if (text !== undefined) e.textContent = text;
  return e;
}

function fmt(v) {
  return Math.abs(v) >= 100 ? v.toFixed(0) : Math.abs(v) >= 10 ? v.toFixed(1) : v.toFixed(2);
}

function niceMax(v) {
  const step = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * step >= v) return m * step;
  return 10 * step;
}

function chart(runs, key, title, unit) {
  const lines = runs.map((r, i) => ({name: r.id, color: "var(--series-" + (i + 1) + ")", points: r.series[key] || []})).filter(l => l.points.length);
  if (!lines.length) return null;
  const all = lines.flatMap(l => l.points);
  const maxX = Math.max(...all.map(p => p[0])), maxY = niceMax(Math.max(...all.map(p => p[1])));
  if (!maxX || !maxY) return null;
  const W = 640, H = 180, L = 52, B = 22, T = 8, R = 12;
  const x = v => L + v / maxX * (W - L - R), y = v => T + (1 - v / maxY) * (H - T - B);
  const fig = document.createElement("figure");
  const cap = document.createElement("figcaption");
  cap.textContent = title + " ";
  const u = document.createElement("span");
  u.className = "muted";
  u.textContent = "(" + unit + ")";
  cap.append(u);
  const legend = document.createElement("div");
  legend.className = "legend";
  for (const l of lines) {
    const s = document.createElement("span");
    const i = document.createElement("i");
    i.style.background = l.color;
    s.append(i, l.name);
    legend.append(s);
  }
  const svg = el("svg", {viewBox: "0 0 " + W + " " + H, role: "img", "aria-label": title});
  for (const t of [0, maxY / 2, maxY]) {
    svg.append(el("line", {class: "grid", x1: L, x2: W - R, y1: y(t), y2: y(t)}));
    svg.append(el("text", {class: "tick", x: L - 6, y: y(t) + 4, "text-anchor": "end"}, fmt(t)));
  }
  for (const t of [0, maxX / 2, maxX]) svg.append(el("text", {class: "tick", x: x(t), y: H - 6, "text-anchor": "middle"}, fmt(t) + "s"));
  for (const l of lines) {
    svg.append(el("polyline", {class: "series", style: "stroke:" + l.color, points: l.points.map(p => x(p[0]) + "," + y(p[1])).join(" ")}));
    for (const p of l.points) {
      const c = el("circle", {class: "hit", cx: x(p[0]), cy: y(p[1]), r: 6});
      c.append(el("title", {}, l.name + " · " + fmt(p[0]) + "s · " + fmt(p[1]) + " " + unit));
      svg.append(c);
    }
  }
  fig.append(cap, legend, svg);
  return fig;
}

Promise.all(ids.map(id => fetch(id + "/data.json").then(r => r.json()))).then(runs => {
  const body = document.querySelector("#runs tbody");
  runs.forEach((r, i) => {
    const tr = document.createElement("tr");
    const first = document.createElement("td");
    const swatch = document.createElement("span");
    swatch.className = "legend";
    const sw = document.createElement("i");
    sw.style.background = "var(--series-" + (i + 1) + ")";
    const a = document.createElement("a");
    a.href = ids[i] + "/index.html";
    a.textContent = r.id;
    swatch.append(sw, a);
    first.append(swatch);
    tr.append(first);
    const base = runs[0].decode;
    const cells = [r.model, r.runtime, r.args,
      r.decode ? fmt(r.decode) : "",
      i > 0 && r.decode && base ? "×" + (r.decode / base).toFixed(2) : "",
      r.energy ? fmt(r.energy) : ""];
    cells.forEach((v, j) => {
      const td = document.createElement("td");
      if (j >= 3) td.className = "num";
      td.textContent = v;
      tr.append(td);
    });
    body.append(tr);
  });
  const charts = document.getElementById("charts");
  for (const [key, title, unit] of metrics) {
    const c = chart(runs, key, title, unit);
    if (c) charts.append(c);
  }
});
`
