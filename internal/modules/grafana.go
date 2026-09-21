package modules

import (
	"context"
	"fmt"
	"math"
	"strings"

	"breaklist/internal/connectors"
)

// ── Grafana query ────────────────────────────────────────────────────────────

type grafanaModule struct{}

type grafanaRow struct {
	Cells []string
}

var grafanaTpl = Tpl("grafana", `<div class="h">{{.Title}}</div>
{{if eq .Mode "stats"}}<table class="kv">{{range .Stats}}<tr><td>{{index . 0}}</td><td>{{index . 1}}</td></tr>{{end}}</table>
{{else}}<table class="gtable">{{if .Header}}<tr>{{range .Header}}<th>{{.}}</th>{{end}}</tr>{{end}}{{range .Rows}}<tr>{{range .Cells}}<td>{{.}}</td>{{end}}</tr>{{end}}</table>{{end}}
{{if .Spark}}<div class="c spark">{{.Spark}}</div>{{end}}{{if .Note}}<div class="s">{{.Note}}</div>{{end}}`)

func (grafanaModule) Info() Info {
	return Info{
		ID: "grafana", Name: "Grafana query", Category: CatConnectors, DefaultEnabled: false, Needs: []string{"grafana"},
		Description: "Runs a query through your Grafana server and prints the result: the latest value of each series, a small table, or a text sparkline. Works with any data source Grafana can chart.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Grafana"},
			{Key: "datasource", Label: "Data source UID", Type: FieldText, Help: "Use the “List data sources” button on the Connectors tab to find UIDs. Blank uses the default data source."},
			{Key: "dsType", Label: "Data source type", Type: FieldSelect, Options: []string{"influxdb", "prometheus", "postgres", "mysql", "sqlite", "loki", "other"}, Default: "influxdb"},
			{Key: "query", Label: "Query", Type: FieldTextarea, Default: "", Help: "In the data source's own language: InfluxQL/Flux, PromQL, SQL… Example (InfluxQL): SELECT mean(\"value\") FROM \"°F\" WHERE \"entity_id\" = 'baby_1_temperature' AND $timeFilter GROUP BY time(1h)"},
			{Key: "from", Label: "Time range start", Type: FieldText, Default: "now-24h", Help: "Grafana syntax: now-24h, now-7d, now/d"},
			{Key: "mode", Label: "Show", Type: FieldSelect, Options: []string{"latest", "stats", "table", "sparkline"}, Default: "latest",
				Help: "latest = last value of each column · stats = min/max/avg/last of the first numeric column · table = last rows · sparkline = ▁▂▃▅▇ of the first numeric column plus last value"},
			{Key: "rows", Label: "Table rows", Type: FieldNumber, Default: 5, Min: F64(1), Max: F64(30)},
			{Key: "decimals", Label: "Decimals", Type: FieldNumber, Default: 1, Min: F64(0), Max: F64(4)},
			{Key: "unit", Label: "Unit suffix", Type: FieldText, Help: "Appended to numbers, e.g. °F or kWh. Blank uses the unit Grafana reports, if any."},
			{Key: "labels", Label: "Column labels", Type: FieldText, Help: "Optional comma-separated names to replace the column headers."},
		},
	}
}

func (grafanaModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	g, err := connectors.NewGrafana(env.Cfg.Connectors.Grafana)
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(opt.Str("query", ""))
	if query == "" {
		return EmptySection("grafana"), nil
	}
	uid := strings.TrimSpace(opt.Str("datasource", ""))
	dsType := opt.Str("dsType", "influxdb")
	if uid == "" {
		list, err := g.Datasources(ctx)
		if err != nil {
			return nil, err
		}
		for _, d := range list {
			if d.IsDefault || uid == "" {
				uid, dsType = d.UID, d.Type
				if d.IsDefault {
					break
				}
			}
		}
	}
	mode := opt.Str("mode", "latest")
	maxPoints := 60
	if mode == "table" {
		maxPoints = opt.Int("rows", 5) * 4
	}
	frames, err := g.Query(ctx, uid, dsType, query, opt.Str("from", "now-24h"), "now", maxPoints)
	if err != nil {
		return nil, err
	}
	if len(frames) == 0 {
		return EmptySection("grafana"), nil
	}
	decimals := opt.Int("decimals", 1)
	unitOverride := strings.TrimSpace(opt.Str("unit", ""))
	labels := keywordListKeepCase(opt.Str("labels", ""))
	data := struct {
		Title, Mode, Spark, Note string
		Header                   []string
		Rows                     []grafanaRow
		Stats                    [][2]string
	}{Title: opt.Str("title", "Grafana"), Mode: mode}

	colName := func(f connectors.Frame, i int) string {
		if i < len(labels) && labels[i] != "" {
			return labels[i]
		}
		if f.Name != "" && len(f.Columns) == 2 && i == 1 {
			return f.Name
		}
		return f.Columns[i]
	}
	unitFor := func(f connectors.Frame, i int) string {
		if unitOverride != "" {
			return unitOverride
		}
		if i < len(f.Units) {
			return grafanaUnit(f.Units[i])
		}
		return ""
	}
	numeric := func(f connectors.Frame) (int, []float64) {
		for c := range f.Columns {
			if strings.EqualFold(f.Columns[c], "time") {
				continue
			}
			var vals []float64
			for _, r := range f.Rows {
				if c < len(r) {
					if v, ok := r[c].(float64); ok && !math.IsNaN(v) {
						vals = append(vals, v)
					}
				}
			}
			if len(vals) > 0 {
				return c, vals
			}
		}
		return -1, nil
	}

	switch mode {
	case "latest":
		data.Mode = "stats"
		for _, f := range frames {
			if len(f.Rows) == 0 {
				continue
			}
			last := f.Rows[len(f.Rows)-1]
			for c := range f.Columns {
				if strings.EqualFold(f.Columns[c], "time") || c >= len(last) || last[c] == nil {
					continue
				}
				data.Stats = append(data.Stats, [2]string{colName(f, c), connectors.FormatValue(last[c], f.Columns[c], env.Loc, decimals) + unitFor(f, c)})
			}
		}
	case "stats":
		f := frames[0]
		c, vals := numeric(f)
		if c < 0 {
			return nil, fmt.Errorf("grafana: no numeric column in result")
		}
		mn, mx, sum := vals[0], vals[0], 0.0
		for _, v := range vals {
			mn, mx = math.Min(mn, v), math.Max(mx, v)
			sum += v
		}
		u := unitFor(f, c)
		fv := func(v float64) string { return connectors.FormatValue(v, "", env.Loc, decimals) + u }
		data.Stats = [][2]string{{"Last", fv(vals[len(vals)-1])}, {"Average", fv(sum / float64(len(vals)))}, {"Min", fv(mn)}, {"Max", fv(mx)}}
		data.Note = fmt.Sprintf("%s · %d points since %s", colName(f, c), len(vals), opt.Str("from", "now-24h"))
	case "table":
		f := frames[0]
		for c := range f.Columns {
			data.Header = append(data.Header, colName(f, c))
		}
		n := opt.Int("rows", 5)
		start := len(f.Rows) - n
		if start < 0 {
			start = 0
		}
		for _, r := range f.Rows[start:] {
			var cells []string
			for c := range f.Columns {
				var v any
				if c < len(r) {
					v = r[c]
				}
				cells = append(cells, connectors.FormatValue(v, f.Columns[c], env.Loc, decimals)+unitFor(f, c))
			}
			data.Rows = append(data.Rows, grafanaRow{Cells: cells})
		}
	case "sparkline":
		data.Mode = "stats"
		f := frames[0]
		c, vals := numeric(f)
		if c < 0 {
			return nil, fmt.Errorf("grafana: no numeric column in result")
		}
		u := unitFor(f, c)
		mn, mx := vals[0], vals[0]
		for _, v := range vals {
			mn, mx = math.Min(mn, v), math.Max(mx, v)
		}
		data.Stats = [][2]string{{colName(f, c), connectors.FormatValue(vals[len(vals)-1], "", env.Loc, decimals) + u}}
		data.Spark = sparkline(vals, 40)
		data.Note = fmt.Sprintf("range %s – %s over %s", connectors.FormatValue(mn, "", env.Loc, decimals)+u, connectors.FormatValue(mx, "", env.Loc, decimals)+u, opt.Str("from", "now-24h"))
	}
	if len(data.Stats) == 0 && len(data.Rows) == 0 {
		return EmptySection("grafana"), nil
	}
	est := 20 + len(data.Stats)*13 + (len(data.Rows)+1)*12 + 10
	if data.Spark != "" {
		est += 16
	}
	return Exec("grafana", grafanaTpl, data, est)
}

// sparkline renders values as block characters, resampled to width columns.
func sparkline(vals []float64, width int) string {
	if len(vals) == 0 {
		return ""
	}
	if len(vals) > width {
		var res []float64
		for i := 0; i < width; i++ {
			a := i * len(vals) / width
			b := (i + 1) * len(vals) / width
			if b <= a {
				b = a + 1
			}
			var s float64
			for _, v := range vals[a:b] {
				s += v
			}
			res = append(res, s/float64(b-a))
		}
		vals = res
	}
	mn, mx := vals[0], vals[0]
	for _, v := range vals {
		mn, mx = math.Min(mn, v), math.Max(mx, v)
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range vals {
		idx := 0
		if mx > mn {
			idx = int((v - mn) / (mx - mn) * float64(len(bars)-1))
		}
		b.WriteRune(bars[idx])
	}
	return b.String()
}

func grafanaUnit(u string) string {
	switch u {
	case "", "none", "short":
		return ""
	case "fahrenheit":
		return "°F"
	case "celsius":
		return "°C"
	case "percent":
		return "%"
	case "watt":
		return " W"
	case "kwatth":
		return " kWh"
	case "humidity":
		return "%"
	}
	if strings.HasPrefix(u, "°") || strings.HasPrefix(u, "%") {
		return u
	}
	return " " + u
}

func keywordListKeepCase(s string) []string {
	var out []string
	for _, w := range strings.Split(s, ",") {
		out = append(out, strings.TrimSpace(w))
	}
	if len(out) == 1 && out[0] == "" {
		return nil
	}
	return out
}

func init() {
	Register(grafanaModule{})
}
