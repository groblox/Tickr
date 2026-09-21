package kids

import (
	"fmt"
	"math"
	"strings"
)

// Shape is a printable outline for colouring or tracing.
type Shape struct {
	Name string
	Path func(cx, cy, r float64) string // returns SVG path/element markup
}

// Shapes are the outlines used by the "shape of the day" module, in a toddler-
// friendly order (easy shapes first).
var Shapes = []Shape{
	{"circle", func(cx, cy, r float64) string {
		return fmt.Sprintf(`<circle cx="%.1f" cy="%.1f" r="%.1f"/>`, cx, cy, r)
	}},
	{"square", func(cx, cy, r float64) string {
		return fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, cx-r, cy-r, 2*r, 2*r)
	}},
	{"triangle", func(cx, cy, r float64) string {
		return fmt.Sprintf(`<polygon points="%.1f,%.1f %.1f,%.1f %.1f,%.1f"/>`, cx, cy-r, cx+r, cy+r*0.85, cx-r, cy+r*0.85)
	}},
	{"star", func(cx, cy, r float64) string { return fmt.Sprintf(`<path d="%s"/>`, starPath(cx, cy, r)) }},
	{"heart", func(cx, cy, r float64) string { return fmt.Sprintf(`<path d="%s"/>`, heartPath(cx, cy, r)) }},
	{"rectangle", func(cx, cy, r float64) string {
		return fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, cx-r, cy-r*0.6, 2*r, 1.2*r)
	}},
	{"oval", func(cx, cy, r float64) string {
		return fmt.Sprintf(`<ellipse cx="%.1f" cy="%.1f" rx="%.1f" ry="%.1f"/>`, cx, cy, r, r*0.65)
	}},
	{"diamond", func(cx, cy, r float64) string {
		return fmt.Sprintf(`<polygon points="%.1f,%.1f %.1f,%.1f %.1f,%.1f %.1f,%.1f"/>`, cx, cy-r, cx+r*0.7, cy, cx, cy+r, cx-r*0.7, cy)
	}},
	{"hexagon", func(cx, cy, r float64) string {
		return fmt.Sprintf(`<path d="%s"/>`, polygonPath(cx, cy, r, 6, -math.Pi/2))
	}},
	{"crescent moon", func(cx, cy, r float64) string {
		return fmt.Sprintf(`<path d="M %.1f %.1f A %.1f %.1f 0 1 0 %.1f %.1f A %.1f %.1f 0 1 1 %.1f %.1f Z"/>`,
			cx+r*0.3, cy-r, r, r, cx+r*0.3, cy+r, r*0.75, r*0.75, cx+r*0.3, cy-r)
	}},
}

// ShapeSVG draws a shape outline sized for the report with a dashed trace line.
func ShapeSVG(s Shape, size int, dashed bool) string {
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size) * 0.42
	style := `fill="none" stroke="#000" stroke-width="3" stroke-linejoin="round"`
	if dashed {
		style += ` stroke-dasharray="6,4"`
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><g %s>%s</g></svg>`,
		size, size, size, size, style, s.Path(cx, cy, r))
}

// TraceLetterSVG draws a big dashed letter or digit for tracing practice.
func TraceLetterSVG(ch string, size int) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+
		`<text x="50%%" y="50%%" dominant-baseline="central" text-anchor="middle" font-family="Arial, Helvetica, sans-serif" font-weight="bold" `+
		`font-size="%d" fill="none" stroke="#000" stroke-width="1.5" stroke-dasharray="4,3">%s</text></svg>`,
		size, size, size, size, int(float64(size)*0.9), ch)
}

// CountingRowSVG draws n small icons (stars) to count, wrapping at perRow.
func CountingRowSVG(n, perRow, cell int) string {
	if perRow < 1 {
		perRow = 5
	}
	rows := (n + perRow - 1) / perRow
	w := perRow * cell
	h := rows * cell
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><g fill="#000">`, w, h, w, h)
	for i := 0; i < n; i++ {
		cx := float64(i%perRow)*float64(cell) + float64(cell)/2
		cy := float64(i/perRow)*float64(cell) + float64(cell)/2
		fmt.Fprintf(&b, `<path d="%s"/>`, starPath(cx, cy, float64(cell)*0.4))
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

// DotGridSVG draws a grid of dots for a "connect the dots" doodle box.
func DotGridSVG(cols, rows, cell int) string {
	w, h := cols*cell, rows*cell
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><g fill="#000">`, w, h, w, h)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="1.6"/>`, x*cell+cell/2, y*cell+cell/2)
		}
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

func starPath(cx, cy, r float64) string {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		rad := r
		if i%2 == 1 {
			rad = r * 0.45
		}
		a := -math.Pi/2 + float64(i)*math.Pi/5
		x, y := cx+rad*math.Cos(a), cy+rad*math.Sin(a)
		if i == 0 {
			fmt.Fprintf(&b, "M %.1f %.1f", x, y)
		} else {
			fmt.Fprintf(&b, " L %.1f %.1f", x, y)
		}
	}
	b.WriteString(" Z")
	return b.String()
}

func heartPath(cx, cy, r float64) string {
	// Two arcs on top, point at the bottom.
	top := cy - r*0.35
	return fmt.Sprintf("M %.1f %.1f C %.1f %.1f, %.1f %.1f, %.1f %.1f C %.1f %.1f, %.1f %.1f, %.1f %.1f Z",
		cx, cy+r,
		cx-r*1.4, cy+r*0.1, cx-r*1.0, top-r*0.9, cx, top,
		cx+r*1.0, top-r*0.9, cx+r*1.4, cy+r*0.1, cx, cy+r)
}

func polygonPath(cx, cy, r float64, n int, rot float64) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		a := rot + float64(i)*2*math.Pi/float64(n)
		x, y := cx+r*math.Cos(a), cy+r*math.Sin(a)
		if i == 0 {
			fmt.Fprintf(&b, "M %.1f %.1f", x, y)
		} else {
			fmt.Fprintf(&b, " L %.1f %.1f", x, y)
		}
	}
	b.WriteString(" Z")
	return b.String()
}
