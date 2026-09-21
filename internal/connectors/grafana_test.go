package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"breaklist/internal/config"
)

func TestGrafanaQuery(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"11.2.0","database":"ok"}`))
	})
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`[{"uid":"abc","name":"InfluxDB","type":"influxdb","isDefault":true}]`))
	})
	mux.HandleFunc("/api/user", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	mux.HandleFunc("/api/ds/query", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		q := body["queries"].([]any)[0].(map[string]any)
		if q["query"] != "SELECT 1" || q["datasource"].(map[string]any)["uid"] != "abc" {
			t.Errorf("unexpected query payload: %v", q)
		}
		_, _ = w.Write([]byte(`{"results":{"A":{"frames":[{"schema":{"name":"nursery","fields":[{"name":"time","type":"time"},{"name":"value","type":"number","config":{"unit":"fahrenheit"}}]},"data":{"values":[[1700000000000,1700003600000],[72.5,73.25]]}}]}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	g, err := NewGrafana(config.Grafana{URL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if msg, err := g.Health(ctx); err != nil || msg != "Grafana 11.2.0 (ok)" {
		t.Fatalf("health: %v %q", err, msg)
	}
	ds, err := g.Datasources(ctx)
	if err != nil || len(ds) != 1 || ds[0].UID != "abc" {
		t.Fatalf("datasources: %v %+v", err, ds)
	}
	frames, err := g.Query(ctx, "abc", "influxdb", "SELECT 1", "now-1h", "now", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || len(frames[0].Rows) != 2 || frames[0].Columns[1] != "value" || frames[0].Units[1] != "fahrenheit" {
		t.Fatalf("frame wrong: %+v", frames)
	}
	if v := FormatValue(frames[0].Rows[1][1], "value", time.UTC, 1); v != "73.3" && v != "73.2" {
		t.Fatalf("format value: %s", v)
	}
	if v := FormatValue(1700000000000.0, "time", time.UTC, 0); v != "Nov 14 10:13 PM" {
		t.Fatalf("format time: %s", v)
	}
	bad, _ := NewGrafana(config.Grafana{URL: srv.URL, Token: "wrong"})
	if _, err := bad.Datasources(ctx); err == nil {
		t.Fatal("bad token should fail")
	}
}
