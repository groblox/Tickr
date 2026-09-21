// Package wxicons draws weather glyphs as inline SVG line art, which stays
// crisp at any size on a 1-bit thermal printer.
package wxicons

import (
	"fmt"
	"strings"
)

// Kind is an icon identifier.
type Kind string

// Available icons.
const (
	ClearDay      Kind = "clear-day"
	ClearNight    Kind = "clear-night"
	PartlyDay     Kind = "partly-day"
	PartlyNight   Kind = "partly-night"
	Cloudy        Kind = "cloudy"
	Fog           Kind = "fog"
	Drizzle       Kind = "drizzle"
	Rain          Kind = "rain"
	HeavyRain     Kind = "heavy-rain"
	Snow          Kind = "snow"
	Sleet         Kind = "sleet"
	Thunder       Kind = "thunder"
	Wind          Kind = "wind"
	MostlyDay     Kind = "mostly-day"
	MostlyNight   Kind = "mostly-night"
	MostlyCloudyD Kind = "mostly-cloudy"
)

// ForCode maps a Tomorrow.io weather code (also used for Open-Meteo via the
// connector's WMO mapping) to an icon.
func ForCode(code int, isDay bool) Kind {
	pick := func(day, night Kind) Kind {
		if isDay {
			return day
		}
		return night
	}
	switch code {
	case 1000:
		return pick(ClearDay, ClearNight)
	case 1100:
		return pick(MostlyDay, MostlyNight)
	case 1101:
		return pick(PartlyDay, PartlyNight)
	case 1102:
		return MostlyCloudyD
	case 1001:
		return Cloudy
	case 2000, 2100:
		return Fog
	case 4000:
		return Drizzle
	case 4200, 4001:
		return Rain
	case 4201:
		return HeavyRain
	case 5000, 5001, 5100, 5101:
		return Snow
	case 6000, 6001, 6200, 6201, 7000, 7101, 7102:
		return Sleet
	case 8000:
		return Thunder
	}
	return Cloudy
}

// SVG returns the inline SVG markup for an icon at the given pixel size.
func SVG(k Kind, size int) string {
	body, ok := icons[k]
	if !ok {
		body = icons[Cloudy]
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 64 64" fill="none" stroke="#000" stroke-width="4" stroke-linecap="round" stroke-linejoin="round">%s</svg>`, size, size, body)
}

// Kinds lists every icon, for the preview gallery.
func Kinds() []Kind {
	return []Kind{ClearDay, ClearNight, MostlyDay, MostlyNight, PartlyDay, PartlyNight, MostlyCloudyD, Cloudy, Fog, Drizzle, Rain, HeavyRain, Snow, Sleet, Thunder, Wind}
}

// ── primitives ───────────────────────────────────────────────────────────────

func sun(cx, cy, r float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f"/>`, cx, cy, r)
	// eight rays
	for i := 0; i < 8; i++ {
		a := float64(i) * 45
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" transform="rotate(%.0f %.1f %.1f)"/>`,
			cx, cy-r-4, cx, cy-r-9, a, cx, cy)
	}
	return b.String()
}

func moon(cx, cy, r float64) string {
	// crescent: outer arc of radius r, inner arc offset to the right
	return fmt.Sprintf(`<path d="M %.1f %.1f A %.1f %.1f 0 1 0 %.1f %.1f A %.1f %.1f 0 0 1 %.1f %.1f Z"/>`,
		cx+r*0.35, cy-r*0.94, r, r, cx+r*0.35, cy+r*0.94, r*0.78, r*0.78, cx+r*0.35, cy-r*0.94)
}

// cloud draws a cloud whose bottom edge is at y and which spans x0..x1.
func cloud(x0, x1, y float64) string {
	w := x1 - x0
	return fmt.Sprintf(`<path d="M %.1f %.1f H %.1f a %.1f %.1f 0 0 0 0 -%.1f a %.1f %.1f 0 0 0 -%.1f -4 a %.1f %.1f 0 0 0 -%.1f %.1f Z"/>`,
		x0+w*0.2, y, x1-w*0.22, w*0.22, w*0.22, w*0.44, w*0.3, w*0.3, w*0.56, w*0.2, w*0.2, w*0.02, w*0.48)
}

func rainLines(n int, y float64, spread float64) string {
	var b strings.Builder
	start := 32 - spread/2
	for i := 0; i < n; i++ {
		x := start + spread*float64(i)/float64(max(n-1, 1))
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, x, y, x-3, y+9)
	}
	return b.String()
}

func dots(n int, y float64, spread float64) string {
	var b strings.Builder
	start := 32 - spread/2
	for i := 0; i < n; i++ {
		x := start + spread*float64(i)/float64(max(n-1, 1))
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, x, y, x-0.5, y+1.5)
	}
	return b.String()
}

func flake(cx, cy, r float64) string {
	var b strings.Builder
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" transform="rotate(%d %.1f %.1f)"/>`, cx, cy-r, cx, cy+r, i*60, cx, cy)
	}
	return b.String()
}

var icons = map[Kind]string{
	ClearDay:      sun(32, 32, 12),
	ClearNight:    moon(32, 32, 15),
	MostlyDay:     sun(28, 28, 11) + `<path d="M 40 50 H 54 a 6 6 0 0 0 0 -12 a 9 9 0 0 0 -16 -3" fill="#fff"/>`,
	MostlyNight:   moon(28, 28, 13) + `<path d="M 40 50 H 54 a 6 6 0 0 0 0 -12 a 9 9 0 0 0 -16 -3" fill="#fff"/>`,
	PartlyDay:     sun(24, 24, 9) + `<g fill="#fff">` + cloud(20, 58, 52) + `</g>`,
	PartlyNight:   moon(24, 24, 11) + `<g fill="#fff">` + cloud(20, 58, 52) + `</g>`,
	MostlyCloudyD: `<path d="M 44 22 a 8 8 0 1 1 6 14" />` + `<g fill="#fff">` + cloud(8, 52, 50) + `</g>`,
	Cloudy:        cloud(8, 58, 46),
	Fog:           cloud(10, 56, 34) + `<line x1="14" y1="44" x2="50" y2="44"/><line x1="20" y1="53" x2="44" y2="53"/>`,
	Drizzle:       cloud(10, 56, 36) + dots(3, 46, 20) + dots(2, 54, 10),
	Rain:          cloud(10, 56, 36) + rainLines(3, 44, 20),
	HeavyRain:     cloud(10, 56, 34) + rainLines(4, 42, 24) + rainLines(3, 53, 16),
	Snow:          cloud(10, 56, 34) + flake(22, 48, 5) + flake(42, 48, 5) + flake(32, 56, 5),
	Sleet:         cloud(10, 56, 36) + `<line x1="22" y1="44" x2="19" y2="53"/>` + flake(32, 49, 5) + `<line x1="44" y1="44" x2="41" y2="53"/>`,
	Thunder:       cloud(10, 56, 34) + `<path d="M 34 36 L 26 50 H 33 L 29 60 L 40 44 H 33 L 36 36 Z" fill="#000" stroke="none"/>`,
	Wind:          `<path d="M 10 26 H 40 a 6 6 0 1 0 -6 -6"/><path d="M 10 38 H 48 a 6 6 0 1 1 -6 6"/><path d="M 14 50 H 32 a 4 4 0 1 1 -4 4"/>`,
}
