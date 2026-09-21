package modules

import (
	"context"
	"strings"
	"time"

	"tickr/internal/connectors"
)

// ── Home Assistant entities ──────────────────────────────────────────────────

type haEntitiesModule struct{}

type haRow struct {
	Name, Value string
}

var haTpl = Tpl("ha", `<div class="h">{{.Title}}</div><table class="kv hdrtype">{{range .Rows}}<tr><td>{{.Name}}</td><td>{{.Value}}</td></tr>{{end}}</table>`)

func (haEntitiesModule) Info() Info {
	return Info{
		ID: "homeassistant", Name: "Home Assistant readings", Category: CatConnectors, DefaultEnabled: false, Needs: []string{"homeassistant"},
		Description: "Current state of the Home Assistant entities you pick: indoor temperature, doors, battery levels, whatever you like.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "At home"},
			{Key: "entities", Label: "Entities", Type: FieldEntities, Help: "One entity id per line, optionally followed by a display name and an attribute: sensor.living_room_temperature | Living room  —  climate.hall | Thermostat | current_temperature"},
			{Key: "hideUnavailable", Label: "Hide unavailable entities", Type: FieldBool, Default: true},
		},
	}
}

func (haEntitiesModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	ha, err := connectors.NewHomeAssistant(env.Cfg.Connectors.HomeAssistant)
	if err != nil {
		return nil, err
	}
	wanted := opt.List("entities")
	if len(wanted) == 0 {
		return EmptySection("homeassistant"), nil
	}
	var rows []haRow
	for _, line := range wanted {
		parts := strings.Split(line, "|")
		id := strings.TrimSpace(parts[0])
		alias, attr := "", ""
		if len(parts) > 1 {
			alias = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			attr = strings.TrimSpace(parts[2])
		}
		ent, err := ha.State(ctx, id)
		if err != nil {
			env.Log("home assistant: %v", err)
			continue
		}
		if opt.Bool("hideUnavailable", true) && (ent.State == "unavailable" || ent.State == "unknown") {
			continue
		}
		name := alias
		if name == "" {
			name = ent.FriendlyName
		}
		value := ent.Display()
		if attr != "" {
			if v := ent.Attribute(attr); v != "" {
				value = v
			}
		}
		rows = append(rows, haRow{Name: name, Value: value})
	}
	if len(rows) == 0 {
		return EmptySection("homeassistant"), nil
	}
	return Exec("homeassistant", haTpl, struct {
		Title string
		Rows  []haRow
	}{opt.Str("title", "At home"), rows}, 20+len(rows)*13)
}

// ── Home Assistant template ──────────────────────────────────────────────────

type haTemplateModule struct{}

func (haTemplateModule) Info() Info {
	return Info{
		ID: "homeassistant_template", Name: "Home Assistant template", Category: CatConnectors, DefaultEnabled: false, Needs: []string{"homeassistant"},
		Description: "Free-form text rendered by Home Assistant's Jinja engine, so you can print anything HA knows.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "House"},
			{Key: "template", Label: "Template", Type: FieldTextarea, Default: "Indoor: {{ states('sensor.indoor_temperature') }}°\nDoors: {{ states.binary_sensor | selectattr('state','eq','on') | map(attribute='name') | list | join(', ') or 'all closed' }}",
				Help: "Jinja template evaluated on your Home Assistant server. Each line becomes a line on the report."},
		},
	}
}

func (haTemplateModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	ha, err := connectors.NewHomeAssistant(env.Cfg.Connectors.HomeAssistant)
	if err != nil {
		return nil, err
	}
	tpl := opt.Str("template", "")
	if strings.TrimSpace(tpl) == "" {
		return EmptySection("homeassistant_template"), nil
	}
	out, err := ha.RenderTemplate(ctx, tpl)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return EmptySection("homeassistant_template"), nil
	}
	return Exec("homeassistant_template", customTpl, struct{ Title, Text, Align string }{opt.Str("title", "House"), out, ""}, 20+LinesPx(out, 32, 14))
}

// ── Home Assistant calendar ──────────────────────────────────────────────────

type haCalendarModule struct{}

func (haCalendarModule) Info() Info {
	return Info{
		ID: "homeassistant_calendar", Name: "Home Assistant calendar", Category: CatConnectors, DefaultEnabled: false, Needs: []string{"homeassistant"},
		Description: "Upcoming events from any calendar entity in Home Assistant (local calendars, CalDAV, Google via HA…).",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Family calendar"},
			{Key: "entity", Label: "Calendar entity", Type: FieldText, Default: "calendar.family", Help: "Entity id starting with calendar."},
			{Key: "days", Label: "Look ahead (days)", Type: FieldNumber, Default: 7, Min: F64(1), Max: F64(60)},
			{Key: "count", Label: "Max events", Type: FieldNumber, Default: 5, Min: F64(1), Max: F64(20)},
		},
	}
}

func (haCalendarModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	ha, err := connectors.NewHomeAssistant(env.Cfg.Connectors.HomeAssistant)
	if err != nil {
		return nil, err
	}
	from := env.Now
	to := from.AddDate(0, 0, opt.Int("days", 7))
	evs, err := ha.CalendarEvents(ctx, opt.Str("entity", "calendar.family"), from, to)
	if err != nil {
		return nil, err
	}
	var events []connectors.CalendarEvent
	for _, e := range evs {
		ev := connectors.CalendarEvent{Title: e.Summary, IsAllDay: e.AllDay, Start: e.Start}
		if e.AllDay {
			ev.When = e.Start.In(env.Loc).Format("Mon Jan 2")
			if e.End.Sub(e.Start) > 24*time.Hour {
				ev.When += " – " + e.End.AddDate(0, 0, -1).In(env.Loc).Format("Mon Jan 2")
			}
		} else {
			ev.When = e.Start.In(env.Loc).Format("Mon Jan 2, 3:04 PM")
		}
		events = append(events, ev)
		if len(events) == opt.Int("count", 5) {
			break
		}
	}
	if len(events) == 0 {
		return EmptySection("homeassistant_calendar"), nil
	}
	est := 20
	for _, ev := range events {
		est += LinesPx(ev.Title, 34, 10) + 12
	}
	return Exec("homeassistant_calendar", calendarTpl, struct {
		Title  string
		Events []connectors.CalendarEvent
	}{opt.Str("title", "Family calendar"), events}, est)
}

func init() {
	Register(haEntitiesModule{})
	Register(haTemplateModule{})
	Register(haCalendarModule{})
}
