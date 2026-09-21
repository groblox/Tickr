package connectors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"tickr/internal/config"
	"tickr/internal/httpx"
)

// HAEntity is one Home Assistant state object.
type HAEntity struct {
	EntityID     string         `json:"entity_id"`
	State        string         `json:"state"`
	Attributes   map[string]any `json:"attributes"`
	LastChanged  time.Time      `json:"last_changed"`
	FriendlyName string         `json:"friendly_name,omitempty"`
	Unit         string         `json:"unit,omitempty"`
}

// HomeAssistant is a REST client for a Home Assistant instance.
type HomeAssistant struct {
	cfg config.HomeAssistant
}

// NewHomeAssistant creates a client. URL should look like http://homeassistant.local:8123.
func NewHomeAssistant(cfg config.HomeAssistant) (*HomeAssistant, error) {
	if strings.TrimSpace(cfg.URL) == "" || strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("Home Assistant URL and long-lived access token are required")
	}
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
		cfg.URL = "http://" + cfg.URL
	}
	return &HomeAssistant{cfg: cfg}, nil
}

func (h *HomeAssistant) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(h.cfg.Token)}
}

// Ping verifies the URL and token. It returns the instance's reported message.
func (h *HomeAssistant) Ping(ctx context.Context) (string, error) {
	var resp struct {
		Message string `json:"message"`
	}
	if err := httpx.GetJSON(ctx, h.cfg.URL+"/api/", h.headers(), &resp); err != nil {
		return "", fmt.Errorf("home assistant: %w", err)
	}
	return resp.Message, nil
}

// States returns every entity, sorted by id, with friendly name and unit lifted
// out of the attributes for convenience.
func (h *HomeAssistant) States(ctx context.Context) ([]HAEntity, error) {
	var list []HAEntity
	if err := httpx.GetJSON(ctx, h.cfg.URL+"/api/states", h.headers(), &list); err != nil {
		return nil, fmt.Errorf("home assistant states: %w", err)
	}
	for i := range list {
		list[i].decorate()
	}
	sort.Slice(list, func(i, j int) bool { return list[i].EntityID < list[j].EntityID })
	return list, nil
}

// State returns one entity.
func (h *HomeAssistant) State(ctx context.Context, entityID string) (*HAEntity, error) {
	var ent HAEntity
	if err := httpx.GetJSON(ctx, h.cfg.URL+"/api/states/"+url.PathEscape(entityID), h.headers(), &ent); err != nil {
		return nil, fmt.Errorf("home assistant %s: %w", entityID, err)
	}
	ent.decorate()
	return &ent, nil
}

// RenderTemplate evaluates a Jinja template on the Home Assistant server.
func (h *HomeAssistant) RenderTemplate(ctx context.Context, tpl string) (string, error) {
	payload := map[string]string{"template": tpl}
	b := strings.NewReader(fmt.Sprintf(`{"template": %s}`, jsonString(payload["template"])))
	headers := h.headers()
	headers["Content-Type"] = "application/json"
	resp, err := httpx.Do(ctx, http.MethodPost, h.cfg.URL+"/api/template", b, headers)
	if err != nil {
		return "", fmt.Errorf("home assistant template: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("home assistant template: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return string(raw), nil
}

// HACalendarEvent is an event from a Home Assistant calendar entity.
type HACalendarEvent struct {
	Summary string
	Start   time.Time
	End     time.Time
	AllDay  bool
}

// CalendarEvents lists events for a calendar.* entity in [from, to).
func (h *HomeAssistant) CalendarEvents(ctx context.Context, entityID string, from, to time.Time) ([]HACalendarEvent, error) {
	q := url.Values{}
	q.Set("start", from.Format(time.RFC3339))
	q.Set("end", to.Format(time.RFC3339))
	var raw []struct {
		Summary string `json:"summary"`
		Start   struct {
			DateTime string `json:"dateTime"`
			Date     string `json:"date"`
		} `json:"start"`
		End struct {
			DateTime string `json:"dateTime"`
			Date     string `json:"date"`
		} `json:"end"`
	}
	endpoint := h.cfg.URL + "/api/calendars/" + url.PathEscape(entityID) + "?" + q.Encode()
	if err := httpx.GetJSON(ctx, endpoint, h.headers(), &raw); err != nil {
		return nil, fmt.Errorf("home assistant calendar %s: %w", entityID, err)
	}
	var out []HACalendarEvent
	for _, r := range raw {
		ev := HACalendarEvent{Summary: r.Summary}
		if r.Start.DateTime != "" {
			ev.Start, _ = time.Parse(time.RFC3339, r.Start.DateTime)
			ev.End, _ = time.Parse(time.RFC3339, r.End.DateTime)
		} else {
			ev.AllDay = true
			ev.Start, _ = time.ParseInLocation("2006-01-02", r.Start.Date, from.Location())
			ev.End, _ = time.ParseInLocation("2006-01-02", r.End.Date, from.Location())
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func (e *HAEntity) decorate() {
	if name, ok := e.Attributes["friendly_name"].(string); ok {
		e.FriendlyName = name
	} else {
		e.FriendlyName = e.EntityID
	}
	if unit, ok := e.Attributes["unit_of_measurement"].(string); ok {
		e.Unit = unit
	}
}

// Display returns a human-readable "state unit" string, with sensible
// phrasing for doors, locks, thermostats and long decimals.
func (e *HAEntity) Display() string {
	s := e.State
	domain, _, _ := strings.Cut(e.EntityID, ".")
	class, _ := e.Attributes["device_class"].(string)
	switch s {
	case "on", "off":
		s = onOffWord(domain, class, s == "on")
	case "unavailable":
		s = "n/a"
	case "unknown":
		s = "?"
	}
	if domain == "climate" {
		out := strings.ToUpper(s[:1]) + s[1:]
		if cur, ok := e.Attributes["current_temperature"].(float64); ok {
			out += fmt.Sprintf(" · %s°", trimFloat(cur))
		}
		if target, ok := e.Attributes["temperature"].(float64); ok {
			out += fmt.Sprintf(" (set %s°)", trimFloat(target))
		}
		return out
	}
	if f, ok := parseFloat(s); ok {
		s = trimFloat(f)
	}
	if e.Unit != "" {
		return s + " " + e.Unit
	}
	return s
}

// Attribute returns a named attribute formatted for print.
func (e *HAEntity) Attribute(name string) string {
	v, ok := e.Attributes[name]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case float64:
		return trimFloat(t)
	case string:
		return t
	case bool:
		if t {
			return "Yes"
		}
		return "No"
	default:
		return fmt.Sprint(t)
	}
}

func onOffWord(domain, class string, on bool) string {
	switch {
	case domain == "binary_sensor" && (class == "door" || class == "window" || class == "garage_door" || class == "opening"):
		if on {
			return "Open"
		}
		return "Closed"
	case domain == "binary_sensor" && (class == "motion" || class == "occupancy" || class == "presence"):
		if on {
			return "Detected"
		}
		return "Clear"
	case domain == "binary_sensor" && class == "moisture":
		if on {
			return "WET"
		}
		return "Dry"
	case domain == "binary_sensor" && (class == "battery_charging" || class == "plug" || class == "power"):
		if on {
			return "Yes"
		}
		return "No"
	case domain == "lock":
		if on {
			return "Locked"
		}
		return "Unlocked"
	}
	if on {
		return "On"
	}
	return "Off"
}

func parseFloat(s string) (float64, bool) {
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return 0, false
	}
	// Reject strings that only start with a number (e.g. timestamps).
	if strings.ContainsAny(s, "-T:") && !strings.HasPrefix(s, "-") {
		return 0, false
	}
	return f, true
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	s = strings.TrimSuffix(s, ".0")
	return s
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
