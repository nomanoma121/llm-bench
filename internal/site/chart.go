package site

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

type line struct {
	Name   string
	Points [][2]float64
}

type bar struct {
	Label string
	Value float64
}

const (
	chartWidth  = 640.0
	chartHeight = 180.0
	padLeft     = 52.0
	padBottom   = 22.0
	padTop      = 8.0
	padRight    = 12.0
)

func lineChart(title, unit string, lines []line) template.HTML {
	var maxX, maxY float64
	for _, l := range lines {
		for _, p := range l.Points {
			maxX, maxY = math.Max(maxX, p[0]), math.Max(maxY, p[1])
		}
	}
	if maxX == 0 || maxY == 0 {
		return ""
	}
	maxY = niceMax(maxY)
	x := func(v float64) float64 { return padLeft + v/maxX*(chartWidth-padLeft-padRight) }
	y := func(v float64) float64 { return padTop + (1-v/maxY)*(chartHeight-padTop-padBottom) }

	var b strings.Builder
	fmt.Fprintf(&b, `<figure><figcaption>%s <span class="muted">(%s)</span></figcaption>`, template.HTMLEscapeString(title), template.HTMLEscapeString(unit))
	if len(lines) > 1 {
		b.WriteString(`<div class="legend">`)
		for i, l := range lines {
			fmt.Fprintf(&b, `<span><i style="background:var(--series-%d)"></i>%s</span>`, i%2+1, template.HTMLEscapeString(l.Name))
		}
		b.WriteString(`</div>`)
	}
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartWidth, chartHeight, template.HTMLEscapeString(title))
	for _, t := range []float64{0, maxY / 2, maxY} {
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/><text class="tick" x="%.1f" y="%.1f" text-anchor="end">%s</text>`,
			padLeft, chartWidth-padRight, y(t), y(t), padLeft-6, y(t)+4, number(t))
	}
	for _, t := range []float64{0, maxX / 2, maxX} {
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%ss</text>`, x(t), chartHeight-6, number(t))
	}
	for i, l := range lines {
		var pts []string
		for _, p := range l.Points {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(p[0]), y(p[1])))
		}
		fmt.Fprintf(&b, `<polyline class="series" style="stroke:var(--series-%d)" points="%s"/>`, i%2+1, strings.Join(pts, " "))
		for _, p := range l.Points {
			fmt.Fprintf(&b, `<circle class="hit" cx="%.1f" cy="%.1f" r="6"><title>%s · %ss · %s %s</title></circle>`,
				x(p[0]), y(p[1]), template.HTMLEscapeString(l.Name), number(p[0]), number(p[1]), template.HTMLEscapeString(unit))
		}
	}
	b.WriteString(`</svg></figure>`)
	return template.HTML(b.String())
}

func barChart(title, unit string, bars []bar) template.HTML {
	if len(bars) == 0 {
		return ""
	}
	var maxV float64
	for _, br := range bars {
		maxV = math.Max(maxV, br.Value)
	}
	if maxV == 0 {
		return ""
	}
	const rowHeight, labelWidth, valueWidth = 22.0, 140.0, 70.0
	height := rowHeight * float64(len(bars))
	scale := (chartWidth - labelWidth - valueWidth) / maxV

	var b strings.Builder
	fmt.Fprintf(&b, `<figure><figcaption>%s <span class="muted">(%s)</span></figcaption>`, template.HTMLEscapeString(title), template.HTMLEscapeString(unit))
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartWidth, height, template.HTMLEscapeString(title))
	for i, br := range bars {
		top := float64(i) * rowHeight
		fmt.Fprintf(&b, `<text class="label" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, labelWidth-8, top+15, template.HTMLEscapeString(br.Label))
		fmt.Fprintf(&b, `<rect class="bar" x="%.1f" y="%.1f" width="%.1f" height="14" rx="3"><title>%s · %s %s</title></rect>`,
			labelWidth, top+4, math.Max(br.Value*scale, 1), template.HTMLEscapeString(br.Label), number(br.Value), template.HTMLEscapeString(unit))
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f">%s</text>`, labelWidth+br.Value*scale+6, top+15, number(br.Value))
	}
	b.WriteString(`</svg></figure>`)
	return template.HTML(b.String())
}

func niceMax(v float64) float64 {
	step := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*step >= v {
			return m * step
		}
	}
	return 10 * step
}

func number(v float64) string {
	switch {
	case v == 0:
		return "0"
	case math.Abs(v) >= 100:
		return fmt.Sprintf("%.0f", v)
	case math.Abs(v) >= 10:
		return fmt.Sprintf("%.1f", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}
