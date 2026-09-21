package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"breaklist/internal/config"
	"breaklist/internal/httpx"
)

// Grafana is a client for a Grafana server's HTTP API. It runs queries
// through Grafana's own data-source proxy, so anything Grafana can chart
// (InfluxDB, Prometheus, Postgres, MySQL, Loki, SQLite…) can be printed.
type Grafana struct {
	cfg config.Grafana
}

// NewGrafana validates the connector settings.
func NewGrafana(cfg config.Grafana) (*Grafana, error) {
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if cfg.URL == "" || strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("Grafana URL and service-account token are required")
	}
	if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
		cfg.URL = "http://" + cfg.URL
	}
	return &Grafana{cfg: cfg}, nil
}

func (g *Grafana) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(g.cfg.Token)}
}

// Health returns the server version.
func (g *Grafana) Health(ctx context.Context) (string, error) {
	var h struct {
		Version  string `json:"version"`
		Database string `json:"database"`
	}
	if err := httpx.GetJSON(ctx, g.cfg.URL+"/api/health", nil, &h); err != nil {
		return "", fmt.Errorf("grafana health: %w", err)
	}
	// Health needs no auth; confirm the token separately.
	var me struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := httpx.GetJSON(ctx, g.cfg.URL+"/api/user", g.headers(), &me); err != nil {
		// Service-account tokens cannot call /api/user; try datasources instead.
		if _, dsErr := g.Datasources(ctx); dsErr != nil {
			return "", fmt.Errorf("grafana token rejected: %w", dsErr)
		}
	}
	return fmt.Sprintf("Grafana %s (%s)", h.Version, h.Database), nil
}

// Datasource is one configured data source.
type Datasource struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsDefault bool   `json:"isDefault"`
}

// Datasources lists the data sources the token can see.
func (g *Grafana) Datasources(ctx context.Context) ([]Datasource, error) {
	var out []Datasource
	if err := httpx.GetJSON(ctx, g.cfg.URL+"/api/datasources", g.headers(), &out); err != nil {
		return nil, fmt.Errorf("grafana datasources: %w", err)
	}
	return out, nil
}

// Frame is a simplified Grafana data frame: named columns of values.
type Frame struct {
	Name    string
	Columns []string
	Units   []string
	Rows    [][]any // row-major
}

// Query runs one query against a data source through /api/ds/query.
// dsType picks how the query text is passed (expr, query, rawSql…).
func (g *Grafana) Query(ctx context.Context, dsUID, dsType, query, from, to string, maxPoints int) ([]Frame, error) {
	if from == "" {
		from = "now-24h"
	}
	if to == "" {
		to = "now"
	}
	q := map[string]any{
		"refId":         "A",
		"datasource":    map[string]string{"uid": dsUID},
		"intervalMs":    60000,
		"maxDataPoints": maxPoints,
	}
	switch strings.ToLower(dsType) {
	case "prometheus", "loki":
		q["expr"] = query
		q["format"] = "time_series"
	case "postgres", "mysql", "mssql", "sqlite", "grafana-postgresql-datasource", "grafana-mysql-datasource", "frser-sqlite-datasource":
		q["rawSql"] = query
		q["format"] = "table"
		q["rawQuery"] = true
	case "influxdb", "influx":
		q["query"] = query
		q["rawQuery"] = true
		q["resultFormat"] = "time_series"
	default:
		// Unknown type: send every common field so most plugins pick one up.
		q["expr"] = query
		q["query"] = query
		q["rawSql"] = query
		q["rawQuery"] = true
	}
	payload := map[string]any{"queries": []any{q}, "from": from, "to": to}
	var res struct {
		Results map[string]struct {
			Error  string `json:"error"`
			Frames []struct {
				Schema struct {
					Name   string `json:"name"`
					Fields []struct {
						Name   string `json:"name"`
						Type   string `json:"type"`
						Config struct {
							Unit        string `json:"unit"`
							DisplayName string `json:"displayNameFromDS"`
						} `json:"config"`
						Labels map[string]string `json:"labels"`
					} `json:"fields"`
				} `json:"schema"`
				Data struct {
					Values [][]any `json:"values"`
				} `json:"data"`
			} `json:"frames"`
		} `json:"results"`
	}
	if err := httpx.PostJSON(ctx, g.cfg.URL+"/api/ds/query", payload, g.headers(), &res); err != nil {
		return nil, fmt.Errorf("grafana query: %w", err)
	}
	r, ok := res.Results["A"]
	if !ok {
		return nil, fmt.Errorf("grafana query: no result")
	}
	if r.Error != "" {
		return nil, fmt.Errorf("grafana query: %s", r.Error)
	}
	var frames []Frame
	for _, f := range r.Frames {
		fr := Frame{Name: f.Schema.Name}
		for _, fld := range f.Schema.Fields {
			name := fld.Config.DisplayName
			if name == "" {
				name = fld.Name
				if len(fld.Labels) > 0 {
					var parts []string
					for k, v := range fld.Labels {
						parts = append(parts, k+"="+v)
					}
					name += " {" + strings.Join(parts, ",") + "}"
				}
			}
			fr.Columns = append(fr.Columns, name)
			fr.Units = append(fr.Units, fld.Config.Unit)
		}
		n := 0
		for _, col := range f.Data.Values {
			if len(col) > n {
				n = len(col)
			}
		}
		for i := 0; i < n; i++ {
			row := make([]any, len(f.Data.Values))
			for c, col := range f.Data.Values {
				if i < len(col) {
					row[c] = col[i]
				}
			}
			fr.Rows = append(fr.Rows, row)
		}
		frames = append(frames, fr)
	}
	return frames, nil
}

// FormatValue renders a cell for print: numbers get trimmed, epoch-millis
// timestamps become local times.
func FormatValue(v any, colName string, loc *time.Location, decimals int) string {
	switch t := v.(type) {
	case nil:
		return "–"
	case float64:
		lower := strings.ToLower(colName)
		if (lower == "time" || strings.HasPrefix(lower, "time") || strings.HasSuffix(lower, "_time")) && t > 1e12 {
			return time.UnixMilli(int64(t)).In(loc).Format("Jan 2 3:04 PM")
		}
		if t == float64(int64(t)) && decimals == 0 {
			return fmt.Sprintf("%d", int64(t))
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.*f", decimals, t), "0"), ".")
	case string:
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			return ts.In(loc).Format("Jan 2 3:04 PM")
		}
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}
