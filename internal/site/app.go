package site

const appJS = `const rows = [...document.querySelectorAll("#results tbody tr")];
const filters = document.getElementById("filters");
const chart = document.getElementById("chart");
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
  const shown = rows.filter(visible);
  for (const r of rows) r.hidden = !shown.includes(r);
  drawChart(shown.filter(r => r.dataset.decode));
}

function drawChart(shown) {
  chart.replaceChildren();
  if (!shown.length) return;
  const ns = "http://www.w3.org/2000/svg", width = 640, rowHeight = 22, labelWidth = 220, valueWidth = 60;
  const max = Math.max(...shown.map(r => parseFloat(r.dataset.decode)));
  const scale = (width - labelWidth - valueWidth) / max;
  const fig = document.createElement("figure");
  const cap = document.createElement("figcaption");
  cap.innerHTML = 'Decode <span class="muted">(tok/s)</span>';
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 " + width + " " + rowHeight * shown.length);
  shown.forEach((r, i) => {
    const v = parseFloat(r.dataset.decode), top = i * rowHeight;
    const text = (x, y, s, cls, anchor) => {
      const t = document.createElementNS(ns, "text");
      t.setAttribute("x", x); t.setAttribute("y", y); t.setAttribute("class", cls);
      if (anchor) t.setAttribute("text-anchor", anchor);
      t.textContent = s;
      svg.append(t);
    };
    text(labelWidth - 8, top + 15, r.dataset.label, "label", "end");
    const rect = document.createElementNS(ns, "rect");
    for (const [k, val] of Object.entries({x: labelWidth, y: top + 4, width: Math.max(v * scale, 1), height: 14, rx: 3, class: "bar"})) rect.setAttribute(k, val);
    const title = document.createElementNS(ns, "title");
    title.textContent = r.dataset.label + " · " + r.dataset.runtime + " · " + v + " tok/s";
    rect.append(title);
    svg.append(rect);
    text(labelWidth + v * scale + 6, top + 15, r.dataset.decode, "tick");
  });
  fig.append(cap, svg);
  chart.append(fig);
}

update();
`
