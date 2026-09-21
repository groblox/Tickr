package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"breaklist/internal/config"
)

// fakeHA mimics the handful of Home Assistant REST endpoints Breaklist uses.
func fakeHA(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
			return false
		}
		return true
	}
	states := []map[string]any{
		{"entity_id": "sensor.indoor_temp", "state": "21.5", "attributes": map[string]any{"friendly_name": "Indoor temperature", "unit_of_measurement": "°C"}, "last_changed": time.Now().Format(time.RFC3339)},
		{"entity_id": "binary_sensor.front_door", "state": "off", "attributes": map[string]any{"friendly_name": "Front door", "device_class": "door"}, "last_changed": time.Now().Format(time.RFC3339)},
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.URL.Path != "/api/" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "API running."})
	})
	mux.HandleFunc("/api/states", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_ = json.NewEncoder(w).Encode(states)
		}
	})
	mux.HandleFunc("/api/states/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/states/")
		for _, s := range states {
			if s["entity_id"] == id {
				_ = json.NewEncoder(w).Encode(s)
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/template", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body struct {
			Template string `json:"template"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte("rendered: " + body.Template))
	})
	mux.HandleFunc("/api/calendars/calendar.family", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = w.Write([]byte(`[{"summary":"Swim class","start":{"dateTime":"2030-01-02T10:00:00+00:00"},"end":{"dateTime":"2030-01-02T11:00:00+00:00"}},
		{"summary":"Grandma visits","start":{"date":"2030-01-01"},"end":{"date":"2030-01-03"}}]`))
	})
	return httptest.NewServer(mux)
}

func TestHomeAssistantClient(t *testing.T) {
	srv := fakeHA(t)
	defer srv.Close()
	ctx := context.Background()

	ha, err := NewHomeAssistant(config.HomeAssistant{URL: srv.URL + "/", Token: " good-token "})
	if err != nil {
		t.Fatal(err)
	}
	if msg, err := ha.Ping(ctx); err != nil || msg != "API running." {
		t.Fatalf("ping: %v %q", err, msg)
	}
	states, err := ha.States(ctx)
	if err != nil || len(states) != 2 {
		t.Fatalf("states: %v %d", err, len(states))
	}
	if states[0].EntityID != "binary_sensor.front_door" || states[0].FriendlyName != "Front door" || states[0].Display() != "Closed" {
		t.Fatalf("decorate/sort wrong: %+v", states[0])
	}
	ent, err := ha.State(ctx, "sensor.indoor_temp")
	if err != nil || ent.Display() != "21.5 °C" {
		t.Fatalf("state: %v %+v", err, ent)
	}
	out, err := ha.RenderTemplate(ctx, `{{ states("sensor.x") }} "quoted"`)
	if err != nil || out != `rendered: {{ states("sensor.x") }} "quoted"` {
		t.Fatalf("template: %v %q", err, out)
	}
	evs, err := ha.CalendarEvents(ctx, "calendar.family", time.Now(), time.Now().Add(24*time.Hour))
	if err != nil || len(evs) != 2 {
		t.Fatalf("calendar: %v %d", err, len(evs))
	}
	if !evs[0].AllDay || evs[0].Summary != "Grandma visits" || evs[1].Summary != "Swim class" {
		t.Fatalf("calendar order/parse wrong: %+v", evs)
	}

	bad, _ := NewHomeAssistant(config.HomeAssistant{URL: srv.URL, Token: "wrong"})
	if _, err := bad.Ping(ctx); err == nil {
		t.Fatal("bad token should fail")
	}
	if _, err := NewHomeAssistant(config.HomeAssistant{URL: "", Token: "x"}); err == nil {
		t.Fatal("missing url should fail")
	}
}

func TestParseTasksJSON(t *testing.T) {
	cases := map[string][]string{
		`["a","b"]`: {"a", "b"},
		`[{"text":"a","done":false},{"text":"b","done":true}]`: {"a"},
		`{"tasks":[{"title":"x"}]}`:                            {"x"},
		``:                                                     {},
	}
	for in, want := range cases {
		got, err := ParseTasksJSON([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: got %v want %v", in, got, want)
		}
	}
	if _, err := ParseTasksJSON([]byte(`"nope"`)); err == nil {
		t.Fatal("string should not parse as list")
	}
}
