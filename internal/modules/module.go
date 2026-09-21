// Package modules defines the pluggable content blocks that make up a report.
//
// Each module declares its metadata and option schema (so the GUI can render a
// settings form without knowing anything about the module) and renders itself
// to an HTML fragment plus a rough pixel-height estimate.
package modules

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tickr/internal/config"
	"tickr/internal/connectors"
)

// Category groups modules in the GUI.
const (
	CatCore       = "Daily essentials"
	CatWeather    = "Weather & sky"
	CatNews       = "News & reading"
	CatFun        = "Fun & trivia"
	CatKids       = "For little ones"
	CatConnectors = "Connected devices"
)

// FieldType is the input control the GUI should render for an option.
type FieldType string

// Supported field types.
const (
	FieldText     FieldType = "text"
	FieldSecret   FieldType = "secret"
	FieldNumber   FieldType = "number"
	FieldBool     FieldType = "bool"
	FieldSelect   FieldType = "select"
	FieldTextarea FieldType = "textarea"
	FieldList     FieldType = "list" // newline separated list, stored as []string
	FieldPath     FieldType = "path"
	FieldEntities FieldType = "ha_entities" // Home Assistant entity picker
)

// Field describes one configurable option.
type Field struct {
	Key     string    `json:"key"`
	Label   string    `json:"label"`
	Type    FieldType `json:"type"`
	Default any       `json:"default,omitempty"`
	Options []string  `json:"options,omitempty"`
	Help    string    `json:"help,omitempty"`
	Min     *float64  `json:"min,omitempty"`
	Max     *float64  `json:"max,omitempty"`
}

// Info is the static description of a module.
type Info struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Category       string   `json:"category"`
	Fields         []Field  `json:"fields"`
	DefaultEnabled bool     `json:"defaultEnabled"`
	Needs          []string `json:"needs,omitempty"` // connector ids required
	Source         string   `json:"source,omitempty"`
}

// Section is the rendered output of a module.
type Section struct {
	ID    string
	HTML  template.HTML
	EstPx int  // rough content height in CSS pixels, refined by the renderer
	Empty bool // nothing to show today; the renderer skips it silently
}

// Module is one content block.
type Module interface {
	Info() Info
	Render(ctx context.Context, env *Env, opt Options) (*Section, error)
}

// Env is everything a module may need while rendering.
type Env struct {
	Cfg     *config.Config
	Now     time.Time
	Loc     *time.Location
	DataDir string
	Log     func(format string, args ...any)

	forecastOnce sync.Once
	forecast     *connectors.Forecast
	forecastErr  error
}

// NewEnv builds an Env for the configured time zone.
func NewEnv(cfg *config.Config, dataDir string, logf func(string, ...any)) *Env {
	loc := cfg.Location()
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Env{Cfg: cfg, Now: time.Now().In(loc), Loc: loc, DataDir: dataDir, Log: logf}
}

// Forecast fetches the weather once and shares it between modules.
func (e *Env) Forecast(ctx context.Context) (*connectors.Forecast, error) {
	e.forecastOnce.Do(func() {
		e.forecast, e.forecastErr = connectors.GetForecast(ctx, e.Cfg)
	})
	return e.forecast, e.forecastErr
}

// Temp formats a Celsius value in the configured unit.
func (e *Env) Temp(c float64) string {
	if e.Cfg.Imperial() {
		return fmt.Sprintf("%.0f°F", math.Round(c*9/5+32))
	}
	return fmt.Sprintf("%.0f°C", math.Round(c))
}

// Speed formats km/h in the configured unit.
func (e *Env) Speed(kmh float64) string {
	if e.Cfg.Imperial() {
		return fmt.Sprintf("%.0f mph", kmh*0.621371)
	}
	return fmt.Sprintf("%.0f km/h", kmh)
}

// Depth formats millimetres of rain in the configured unit.
func (e *Env) Depth(mm float64) string {
	if e.Cfg.Imperial() {
		return fmt.Sprintf("%.2f in", mm/25.4)
	}
	return fmt.Sprintf("%.1f mm", mm)
}

// Pressure formats millibars in the configured unit.
func (e *Env) Pressure(mb float64) string {
	if e.Cfg.Imperial() {
		return fmt.Sprintf("%.2f inHg", mb*0.0295299831)
	}
	return fmt.Sprintf("%.0f mb", mb)
}

// imageTargetDPI is the raster resolution embedded images are prepared at.
// The PDF itself is authored at a fixed 96 CSS px/inch (see contentDPI in
// package render), but that number describes page layout, not what a 1-bit
// thermal print head can resolve — most receipt/label printers are 200-300+
// dots per inch. An image capped to a flat pixel width regardless of paper
// size looks soft next to crisp vector text and icons; sizing it from the
// configured paper width instead keeps it sharp on any paper width.
const imageTargetDPI = 300.0

// ImageMaxWidth returns the raster width, in pixels, an image should be
// prepared at to print sharply at widthPct of the configured paper width.
func (e *Env) ImageMaxWidth(widthPct int) int {
	base := int(e.Cfg.General.PaperWidthMM / 25.4 * imageTargetDPI)
	if base < 200 {
		base = 200
	}
	if widthPct > 100 {
		return base * widthPct / 100
	}
	return base
}

// DaySeed returns a stable per-day integer for "of the day" picks.
func (e *Env) DaySeed() int {
	y, m, d := e.Now.Date()
	return y*10000 + int(m)*100 + d
}

// Options is a module's option map with typed accessors.
type Options map[string]any

// Str returns a string option.
func (o Options) Str(key, def string) string {
	if v, ok := o[key]; ok {
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
			return def
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(t)
		}
	}
	return def
}

// Int returns an integer option.
func (o Options) Int(key string, def int) int {
	if v, ok := o[key]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n
			}
		}
	}
	return def
}

// Bool returns a boolean option.
func (o Options) Bool(key string, def bool) bool {
	if v, ok := o[key]; ok {
		switch t := v.(type) {
		case bool:
			return t
		case string:
			if b, err := strconv.ParseBool(t); err == nil {
				return b
			}
		}
	}
	return def
}

// List returns a list option (stored as []any, []string or newline text).
func (o Options) List(key string) []string {
	v, ok := o[key]
	if !ok {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case []string:
		out = t
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		out = strings.Split(t, "\n")
	}
	var clean []string
	for _, s := range out {
		s = strings.TrimSpace(s)
		if s != "" {
			clean = append(clean, s)
		}
	}
	return clean
}

// ApplyDefaults returns a copy of opt with every field's declared default
// filled in where the option is missing, so modules see the same values the
// GUI shows.
func ApplyDefaults(info Info, opt map[string]any) Options {
	out := Options{}
	for k, v := range opt {
		out[k] = v
	}
	for _, f := range info.Fields {
		if _, ok := out[f.Key]; ok || f.Default == nil {
			continue
		}
		out[f.Key] = f.Default
	}
	return out
}

// ── registry ─────────────────────────────────────────────────────────────────

var (
	regMu    sync.Mutex
	registry = map[string]Module{}
	order    []string
)

// Register adds a module to the catalog. Called from init().
func Register(m Module) {
	regMu.Lock()
	defer regMu.Unlock()
	id := m.Info().ID
	if _, dup := registry[id]; dup {
		panic("duplicate module id " + id)
	}
	registry[id] = m
	order = append(order, id)
}

// Get returns a module by id.
func Get(id string) (Module, bool) {
	regMu.Lock()
	defer regMu.Unlock()
	m, ok := registry[id]
	return m, ok
}

// All returns every module in registration order.
func All() []Module {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]Module, 0, len(order))
	for _, id := range order {
		out = append(out, registry[id])
	}
	return out
}

// Catalog returns every module's Info, grouped in a stable category order.
func Catalog() []Info {
	all := All()
	infos := make([]Info, 0, len(all))
	for _, m := range all {
		infos = append(infos, m.Info())
	}
	rank := map[string]int{CatCore: 0, CatWeather: 1, CatNews: 2, CatFun: 3, CatKids: 4, CatConnectors: 5}
	sort.SliceStable(infos, func(i, j int) bool { return rank[infos[i].Category] < rank[infos[j].Category] })
	return infos
}

// NormalizeSections makes cfg.Sections contain every registered module exactly
// once, preserving the user's order and adding new modules at the end.
// On a brand-new config the default layout is applied instead.
func NormalizeSections(cfg *config.Config) {
	if len(cfg.Sections) == 0 {
		cfg.Sections = DefaultLayout()
		return
	}
	seen := map[string]bool{}
	var kept []config.Section
	for _, s := range cfg.Sections {
		if _, ok := registry[s.ID]; ok && !seen[s.ID] {
			seen[s.ID] = true
			if s.Options == nil {
				s.Options = map[string]any{}
			}
			kept = append(kept, s)
		}
	}
	for _, m := range All() {
		id := m.Info().ID
		if !seen[id] {
			kept = append(kept, config.Section{ID: id, Enabled: false, Options: map[string]any{}})
		}
	}
	cfg.Sections = kept
}

// DefaultLayout is the order used for a fresh install.
func DefaultLayout() []config.Section {
	preferred := []string{
		"header", "pws", "calendar", "tasks", "joke", "farside", "weather_hourly", "weather_daily",
		"sun_moon", "news_nyt", "news_hn", "quote", "onthisday", "homeassistant", "custom_text",
	}
	var out []config.Section
	seen := map[string]bool{}
	for _, id := range preferred {
		if m, ok := registry[id]; ok {
			out = append(out, config.Section{ID: id, Enabled: m.Info().DefaultEnabled, Options: map[string]any{}})
			seen[id] = true
		}
	}
	for _, m := range All() {
		id := m.Info().ID
		if !seen[id] {
			out = append(out, config.Section{ID: id, Enabled: m.Info().DefaultEnabled, Options: map[string]any{}})
		}
	}
	return out
}

// ── template helpers ─────────────────────────────────────────────────────────

var funcs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"mul": func(a, b int) int { return a * b },
	"seq": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}
		return s
	},
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"title": func(s string) string {
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:]
	},
	"trunc": func(n int, s string) string {
		r := []rune(s)
		if len(r) <= n {
			return s
		}
		return strings.TrimSpace(string(r[:n-1])) + "…"
	},
	"safe": func(s string) template.HTML { return template.HTML(s) },
	"url":  func(s string) template.URL { return template.URL(s) },
}

// Tpl parses a module template at init time.
func Tpl(name, src string) *template.Template {
	return template.Must(template.New(name).Funcs(funcs).Parse(src))
}

// Exec renders a parsed template into a Section with the given estimate.
func Exec(id string, t *template.Template, data any, estPx int) (*Section, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("%s template: %w", id, err)
	}
	return &Section{ID: id, HTML: template.HTML(buf.String()), EstPx: estPx}, nil
}

// EmptySection returns a section the renderer will skip.
func EmptySection(id string) *Section { return &Section{ID: id, Empty: true} }

// LinesPx estimates the pixel height of text wrapped at charsPerLine.
func LinesPx(text string, charsPerLine, lineHeight int) int {
	lines := 0
	for _, para := range strings.Split(text, "\n") {
		n := (len([]rune(para)) + charsPerLine - 1) / charsPerLine
		if n < 1 {
			n = 1
		}
		lines += n
	}
	return lines * lineHeight
}

// Ordinal renders 1 -> "1st".
func Ordinal(n int) string {
	suffix := "th"
	switch n % 10 {
	case 1:
		if n%100 != 11 {
			suffix = "st"
		}
	case 2:
		if n%100 != 12 {
			suffix = "nd"
		}
	case 3:
		if n%100 != 13 {
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// F64 is a tiny helper for building Min/Max pointers in Field literals.
func F64(v float64) *float64 { return &v }
