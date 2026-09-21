package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tickr/internal/connectors"
)

// ── Header ───────────────────────────────────────────────────────────────────

type headerModule struct{}

var headerTpl = Tpl("header", `<div class="hdr">{{.Date}}</div>
{{if .Greeting}}<div class="c t">{{.Greeting}}</div>{{end}}
{{if .Sub}}<div class="c s">{{.Sub}}</div>{{end}}`)

func (headerModule) Info() Info {
	return Info{
		ID: "header", Name: "Date header", Category: CatCore, DefaultEnabled: true,
		Description: "Today's date at the top of the strip, with an optional greeting.",
		Fields: []Field{
			{Key: "greeting", Label: "Greeting", Type: FieldText, Help: "Use {name} for the child's name, e.g. \"Good morning, {name}!\""},
			{Key: "showDayNumber", Label: "Show day of year and week number", Type: FieldBool, Default: false},
			{Key: "showTimestamp", Label: "Show time generated", Type: FieldBool, Default: false, Help: "Appends the time this report was generated next to the date, e.g. \"- 6:23a\"."},
		},
	}
}

func (headerModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	data := struct{ Date, Greeting, Sub string }{Date: env.Now.Format(env.Cfg.General.DateFormat)}
	if opt.Bool("showTimestamp", false) {
		data.Date += " - " + strings.TrimSuffix(env.Now.Format("3:04pm"), "m")
	}
	if g := opt.Str("greeting", ""); g != "" {
		data.Greeting = strings.ReplaceAll(g, "{name}", env.Cfg.General.ChildName)
	}
	if opt.Bool("showDayNumber", false) {
		_, week := env.Now.ISOWeek()
		data.Sub = fmt.Sprintf("Day %d · Week %d", env.Now.YearDay(), week)
	}
	return Exec("header", headerTpl, data, 24)
}

// ── Tasks & reminders ────────────────────────────────────────────────────────

type tasksModule struct{}

var tasksTpl = Tpl("tasks", `{{if .Title}}<div class="h">{{.Title}}</div>{{end}}
<ul class="tasks">{{range .Items}}<li>{{.}}</li>{{end}}</ul>`)

func (tasksModule) Info() Info {
	return Info{
		ID: "tasks", Name: "Tasks & reminders", Category: CatCore, DefaultEnabled: true,
		Description: "Your to-do list (local file or Dropbox) plus reminders whose schedule matches today.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: ""},
			{Key: "maxItems", Label: "Max items", Type: FieldNumber, Default: 20, Min: F64(1), Max: F64(100)},
			{Key: "includeReminders", Label: "Include reminders", Type: FieldBool, Default: true},
			{Key: "extraTasks", Label: "Always-on tasks", Type: FieldList, Help: "One per line. Printed every day."},
		},
	}
}

func (tasksModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	var items []string
	cfg := env.Cfg
	if cfg.Tasks.Source == "dropbox" {
		tasks, err := connectors.NewDropbox(cfg.Connectors.Dropbox).GetTasks(ctx)
		if err != nil {
			return nil, err
		}
		items = append(items, tasks...)
	} else {
		lines, err := readLines(env.resolve(cfg.Tasks.TasksPath), true)
		if err != nil {
			return nil, err
		}
		items = append(items, lines...)
	}
	if opt.Bool("includeReminders", true) {
		lines, err := readLines(env.resolve(cfg.Tasks.RemindersPath), true)
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			expr, text, ok := strings.Cut(l, "|")
			if !ok {
				continue
			}
			if MatchCron(env.Now, strings.TrimSpace(expr)) {
				items = append(items, strings.TrimSpace(text))
			}
		}
	}
	items = append(items, opt.List("extraTasks")...)
	if max := opt.Int("maxItems", 20); len(items) > max {
		items = items[:max]
	}
	if len(items) == 0 {
		return EmptySection("tasks"), nil
	}
	est := 10
	for _, it := range items {
		est += LinesPx(it, 30, 14) + 6
	}
	return Exec("tasks", tasksTpl, struct {
		Title string
		Items []string
	}{opt.Str("title", ""), items}, est)
}

// readLines reads a text file, skipping blanks and # comments. When create is
// true a missing file is created empty so first runs do not fail.
func readLines(path string, create bool) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && create {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = os.WriteFile(path, []byte{}, 0o600)
			return nil, nil
		}
		return nil, err
	}
	return connectors.ParseTaskLines(string(data)), nil
}

func (e *Env) resolve(p string) string {
	if p == "" {
		return p
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.DataDir, p)
}

// ── Google Calendar ──────────────────────────────────────────────────────────

type calendarModule struct{}

var calendarTpl = Tpl("calendar", `<div class="h">{{.Title}}</div>
<div class="events">{{range .Events}}<div class="ev"><span class="ev-t">{{.Title}}</span> <span class="ev-d">{{.When}}</span></div>{{end}}</div>`)

func (calendarModule) Info() Info {
	return Info{
		ID: "calendar", Name: "Google Calendar", Category: CatCore, DefaultEnabled: true, Needs: []string{"google"},
		Description: "The next few events from your linked Google Calendar.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Upcoming"},
			{Key: "count", Label: "Number of events", Type: FieldNumber, Default: 4, Min: F64(1), Max: F64(15)},
			{Key: "horizon", Label: "Look ahead (days)", Type: FieldNumber, Default: 0, Min: F64(0), Max: F64(365), Help: "0 = no limit, just the next events regardless of how far off. Use this to keep a far-future event (a birthday next spring) from crowding out this week."},
		},
	}
}

func (calendarModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	events, err := connectors.GetCalendarEvents(ctx, env.Cfg.Connectors.Google, env.Loc, opt.Int("count", 4), opt.Int("horizon", 0))
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return EmptySection("calendar"), nil
	}
	est := 20
	for _, ev := range events {
		est += LinesPx(ev.Title+" "+ev.When, 34, 10) + 6
	}
	return Exec("calendar", calendarTpl, struct {
		Title  string
		Events []connectors.CalendarEvent
	}{opt.Str("title", "Upcoming"), events}, est)
}

// ── Personal weather station ─────────────────────────────────────────────────

type pwsModule struct{}

var pwsTpl = Tpl("pws", `<div class="h">{{.Title}}</div><table class="kv hdrtype">{{range .Rows}}<tr><td>{{.Label}}</td><td>{{.Value}}</td></tr>{{end}}</table>`)

func (pwsModule) Info() Info {
	return Info{
		ID: "pws", Name: "Backyard weather station", Category: CatWeather, DefaultEnabled: false, Needs: []string{"aeris"},
		Description: "Live readings from your personal weather station via the Aeris / Xweather API. Pick which readings to show and in what order.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Backyard"},
			{Key: "showTemp", Label: "Temperature", Type: FieldBool, Default: true},
			{Key: "showFeelsLike", Label: "Feels like", Type: FieldBool, Default: false, Help: "Not every station reports this."},
			{Key: "showHumidity", Label: "Humidity", Type: FieldBool, Default: true},
			{Key: "showDewPoint", Label: "Dew point", Type: FieldBool, Default: false},
			{Key: "showWind", Label: "Wind speed", Type: FieldBool, Default: true},
			{Key: "showWindGust", Label: "Wind gust", Type: FieldBool, Default: false, Help: "Not every station reports this."},
			{Key: "showPressure", Label: "Barometric pressure", Type: FieldBool, Default: false},
			{Key: "showRainToday", Label: "Rain today", Type: FieldBool, Default: true},
			{Key: "showRainYesterday", Label: "Rain yesterday", Type: FieldBool, Default: true},
		},
	}
}

func (pwsModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	st, err := connectors.GetPWSStats(ctx, env.Cfg.Connectors.Aeris)
	if err != nil {
		return nil, err
	}
	if !st.HasStats {
		return EmptySection("pws"), nil
	}
	type row struct{ Label, Value string }
	var rows []row
	add := func(key string, def bool, label, value string) {
		if opt.Bool(key, def) {
			rows = append(rows, row{label, value})
		}
	}
	add("showTemp", true, "Temp", env.Temp(st.TempC))
	add("showFeelsLike", false, "Feels like", env.Temp(st.FeelsLikeC))
	add("showHumidity", true, "Humidity", fmt.Sprintf("%.0f%%", st.Humidity))
	add("showDewPoint", false, "Dew point", env.Temp(st.DewpointC))
	add("showWind", true, "Wind", env.Speed(st.WindKmh))
	add("showWindGust", false, "Gust", env.Speed(st.WindGustKmh))
	add("showPressure", false, "Pressure", env.Pressure(st.PressureMB))
	add("showRainToday", true, "Rain today", env.Depth(st.RainTodayMM))
	add("showRainYesterday", true, "Rain yesterday", env.Depth(st.RainYesterday))
	if len(rows) == 0 {
		return EmptySection("pws"), nil
	}
	return Exec("pws", pwsTpl, struct {
		Title string
		Rows  []row
	}{opt.Str("title", "Backyard"), rows}, 20+len(rows)*13)
}

// ── Custom text ──────────────────────────────────────────────────────────────

type customTextModule struct{}

var customTpl = Tpl("custom", `{{if .Title}}<div class="h">{{.Title}}</div>{{end}}<div class="t custom {{.Align}}">{{.Text}}</div>`)

func (customTextModule) Info() Info {
	return Info{
		ID: "custom_text", Name: "Custom note", Category: CatCore, DefaultEnabled: false,
		Description: "A fixed block of text you write yourself: a motto, a reminder, a note to the family.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText},
			{Key: "text", Label: "Text", Type: FieldTextarea, Help: "Line breaks are kept. {name} inserts the child's name, {date} today's date."},
			{Key: "align", Label: "Alignment", Type: FieldSelect, Options: []string{"left", "center"}, Default: "left"},
		},
	}
}

func (customTextModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	text := strings.TrimSpace(opt.Str("text", ""))
	if text == "" {
		return EmptySection("custom_text"), nil
	}
	text = strings.ReplaceAll(text, "{name}", env.Cfg.General.ChildName)
	text = strings.ReplaceAll(text, "{date}", env.Now.Format("Jan 2"))
	align := ""
	if opt.Str("align", "left") == "center" {
		align = "c"
	}
	return Exec("custom_text", customTpl, struct{ Title, Text, Align string }{opt.Str("title", ""), text, align}, LinesPx(text, 32, 14)+20)
}

func init() {
	Register(headerModule{})
	Register(tasksModule{})
	Register(calendarModule{})
	Register(pwsModule{})
	Register(customTextModule{})
}
