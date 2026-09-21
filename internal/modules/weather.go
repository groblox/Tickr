package modules

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strings"
	"time"

	"tickr/assets"
	"tickr/internal/connectors"
	"tickr/internal/httpx"
	"tickr/internal/wxicons"
)

// ── Hourly strip ─────────────────────────────────────────────────────────────

type hourlyModule struct{}

type hourRow struct {
	Time, Temp, Feels, Dew string
	Icon                   template.HTML
	Precip                 int
}

// weatherIcon renders a weather glyph in the configured style.
func weatherIcon(env *Env, code int, isDay bool, size int) template.HTML {
	if env.Cfg.General.IconStyle == "classic" {
		return template.HTML(fmt.Sprintf(`<img src="%s" width="%d" height="%d">`, assets.WeatherIcon(connectors.IconCode(code, isDay)), size, size))
	}
	return template.HTML(wxicons.SVG(wxicons.ForCode(code, isDay), size))
}

var hourlyTpl = Tpl("hourly", `<div class="h">{{.Title}}</div>
<table class="wx-h"><tr>{{range .Rows}}<td><div class="wx-h-time">{{.Time}}</div><div class="wx-ico">{{.Icon}}</div><div class="wx-h-temp">{{.Temp}}</div>{{if $.ShowFeels}}<div class="wx-sub">feels {{.Feels}}</div>{{end}}{{if $.ShowDew}}<div class="wx-sub">dew {{.Dew}}</div>{{end}}<div class="wx-sub">rain {{.Precip}}%</div></td>{{end}}</tr></table>`)

func (hourlyModule) Info() Info {
	return Info{
		ID: "weather_hourly", Name: "Today's forecast strip", Category: CatWeather, DefaultEnabled: true, Needs: []string{"weather"},
		Description: "Three to six snapshots of the rest of today with icons, temperature and rain chance.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Help: "Blank uses \"<Location name> today\"."},
			{Key: "slots", Label: "Number of slots", Type: FieldNumber, Default: 3, Min: F64(2), Max: F64(6)},
			{Key: "stepHours", Label: "Hours between slots", Type: FieldNumber, Default: 3, Min: F64(1), Max: F64(6)},
			{Key: "showFeels", Label: "Show feels-like", Type: FieldBool, Default: false},
			{Key: "showDew", Label: "Show dew point", Type: FieldBool, Default: true},
		},
	}
}

func (hourlyModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	fc, err := env.Forecast(ctx)
	if err != nil {
		return nil, err
	}
	step := opt.Int("stepHours", 3)
	want := opt.Int("slots", 3)
	var rows []hourRow
	last := time.Time{}
	for _, h := range fc.Hourly {
		if h.Time.Before(env.Now.Add(-time.Hour)) {
			continue
		}
		if !last.IsZero() && h.Time.Sub(last) < time.Duration(step)*time.Hour {
			continue
		}
		last = h.Time
		rows = append(rows, hourRow{
			Time: h.Time.Format("3 PM"), Temp: env.Temp(h.TempC), Feels: env.Temp(h.FeelsC), Dew: env.Temp(h.DewC),
			Icon: weatherIcon(env, h.Code, h.IsDay, 28), Precip: h.PrecipProb,
		})
		if len(rows) == want {
			break
		}
	}
	if len(rows) == 0 {
		return EmptySection("weather_hourly"), nil
	}
	title := opt.Str("title", "")
	if title == "" {
		title = strings.TrimSpace(env.Cfg.General.LocationName + " today")
	}
	est := 20 + 62
	if opt.Bool("showFeels", false) {
		est += 8
	}
	return Exec("weather_hourly", hourlyTpl, struct {
		Title              string
		Rows               []hourRow
		ShowFeels, ShowDew bool
	}{title, rows, opt.Bool("showFeels", false), opt.Bool("showDew", true)}, est)
}

// ── Daily outlook ────────────────────────────────────────────────────────────

type dailyModule struct{}

type dayRow struct {
	Day, Date, Hi, Lo, Dew, Desc string
	Icon                         template.HTML
	Precip                       int
}

var dailyTpl = Tpl("daily", `<div class="h">{{.Title}}</div>
<table class="wx-d">{{range .Rows}}<tr><td class="wx-d-day"><div class="wx-d-name">{{.Day}}</div><div class="wx-d-date">{{.Date}}</div></td><td class="wx-d-icon"><div class="wx-ico">{{.Icon}}</div></td><td class="wx-d-temps"><div class="wx-d-hilo"><span>&#9650;{{.Hi}}</span>&nbsp;<span class="lo">&#9660;{{.Lo}}</span></div><div class="wx-sub">{{if $.ShowDesc}}{{.Desc}} · {{end}}{{if $.ShowDew}}dew {{.Dew}} · {{end}}rain {{.Precip}}%</div></td></tr>{{end}}</table>`)

func (dailyModule) Info() Info {
	return Info{
		ID: "weather_daily", Name: "Multi-day outlook", Category: CatWeather, DefaultEnabled: true, Needs: []string{"weather"},
		Description: "High/low, icon and rain chance for the coming days.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Outlook"},
			{Key: "days", Label: "Days to show", Type: FieldNumber, Default: 4, Min: F64(1), Max: F64(6)},
			{Key: "includeToday", Label: "Include today", Type: FieldBool, Default: false},
			{Key: "showDew", Label: "Show dew point", Type: FieldBool, Default: true},
			{Key: "showDesc", Label: "Show conditions text", Type: FieldBool, Default: false},
		},
	}
}

func (dailyModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	fc, err := env.Forecast(ctx)
	if err != nil {
		return nil, err
	}
	today := time.Date(env.Now.Year(), env.Now.Month(), env.Now.Day(), 0, 0, 0, 0, env.Loc)
	var rows []dayRow
	for _, d := range fc.Daily {
		if d.Date.Before(today) || (!opt.Bool("includeToday", false) && d.Date.Equal(today)) {
			continue
		}
		rows = append(rows, dayRow{
			Day: d.Date.Format("Mon"), Date: d.Date.Format("Jan 2"), Hi: env.Temp(d.MaxC), Lo: env.Temp(d.MinC),
			Dew: env.Temp(d.DewC), Icon: weatherIcon(env, d.Code, true, 22), Desc: connectors.Describe(d.Code), Precip: d.PrecipProb,
		})
		if len(rows) == opt.Int("days", 4) {
			break
		}
	}
	if len(rows) == 0 {
		return EmptySection("weather_daily"), nil
	}
	return Exec("weather_daily", dailyTpl, struct {
		Title             string
		Rows              []dayRow
		ShowDew, ShowDesc bool
	}{opt.Str("title", "Outlook"), rows, opt.Bool("showDew", true), opt.Bool("showDesc", false)}, 20+len(rows)*27)
}

// ── Sun & moon ───────────────────────────────────────────────────────────────

type sunMoonModule struct{}

var sunMoonTpl = Tpl("sunmoon", `<div class="h">{{.Title}}</div>
<div class="c t">Sun: {{.Sunrise}} - {{.Sunset}}</div>
{{if .MoonIcon}}<div class="moon">{{.MoonIcon}}<div class="s">{{.Moon}}</div></div>{{end}}`)

func (sunMoonModule) Info() Info {
	return Info{
		ID: "sun_moon", Name: "Sun & moon", Category: CatWeather, DefaultEnabled: false, Needs: []string{"weather"},
		Description: "Sunrise and sunset from the weather forecast, plus a graphic of tonight's moon phase (computed locally, no separate API call).",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Sun & moon"},
			{Key: "showMoon", Label: "Show moon phase", Type: FieldBool, Default: true},
		},
	}
}

func (sunMoonModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	// Sunrise/sunset come from the shared forecast (both Open-Meteo and
	// Tomorrow.io report them per day), so this needs no API call of its own.
	fc, err := env.Forecast(ctx)
	if err != nil {
		return nil, err
	}
	day := todayDaily(fc, env.Now)
	if day == nil || day.Sunrise.IsZero() || day.Sunset.IsZero() {
		return nil, fmt.Errorf("the weather provider did not return sunrise/sunset times")
	}
	data := struct {
		Title, Sunrise, Sunset, Moon string
		MoonIcon                     template.HTML
	}{
		Title: opt.Str("title", "Sun & moon"), Sunrise: day.Sunrise.Format("3:04pm"), Sunset: day.Sunset.Format("3:04pm"),
	}
	est := 20 + 15
	if opt.Bool("showMoon", true) {
		name, _, frac := MoonPhase(env.Now)
		data.Moon = name
		data.MoonIcon = template.HTML(wxicons.MoonPhaseSVG(frac, 44))
		est += 44 + 12
	}
	return Exec("sun_moon", sunMoonTpl, data, est)
}

// todayDaily returns the forecast's entry for today (matching by calendar
// day, not array position), falling back to the first entry if none match.
// Shared by every module that needs "today" out of a multi-day forecast.
func todayDaily(fc *connectors.Forecast, now time.Time) *connectors.Day {
	for i := range fc.Daily {
		if sameDay(fc.Daily[i].Date, now) {
			return &fc.Daily[i]
		}
	}
	if len(fc.Daily) > 0 {
		return &fc.Daily[0]
	}
	return nil
}

// MoonPhase returns the phase name, illuminated fraction and position in the
// cycle (0 = new moon, 0.5 = full moon, approaching 1 = new moon again) for a
// date, using the mean synodic month from a known new moon (2000-01-06 18:14
// UTC). The phase position is what a graphic of the moon's current shape
// needs; see wxicons.MoonPhaseSVG.
func MoonPhase(t time.Time) (string, float64, float64) {
	const synodic = 29.53058867
	ref := time.Date(2000, 1, 6, 18, 14, 0, 0, time.UTC)
	days := t.UTC().Sub(ref).Hours() / 24
	age := math.Mod(days, synodic)
	if age < 0 {
		age += synodic
	}
	frac := age / synodic
	illum := (1 - math.Cos(2*math.Pi*frac)) / 2
	var name string
	switch {
	case frac < 0.034 || frac >= 0.966:
		name = "New moon"
	case frac < 0.216:
		name = "Waxing crescent"
	case frac < 0.284:
		name = "First quarter"
	case frac < 0.466:
		name = "Waxing gibbous"
	case frac < 0.534:
		name = "Full moon"
	case frac < 0.716:
		name = "Waning gibbous"
	case frac < 0.784:
		name = "Last quarter"
	default:
		name = "Waning crescent"
	}
	return name, illum, frac
}

// ── Air quality ──────────────────────────────────────────────────────────────

type airModule struct{}

var airTpl = Tpl("air", `<div class="h">{{.Title}}</div>
<div class="c t"><b>{{.Label}}</b> · AQI {{.AQI}}</div><div class="c s">PM2.5 {{.PM25}} µg/m³ · pollen {{.Pollen}}</div>`)

func (airModule) Info() Info {
	return Info{
		ID: "air_quality", Name: "Air quality", Category: CatWeather, DefaultEnabled: false,
		Description: "US AQI, PM2.5 and a pollen hint from Open-Meteo (no key needed).",
		Fields:      []Field{{Key: "title", Label: "Heading", Type: FieldText, Default: "Air quality"}},
	}
}

func (airModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	lat, lng, ok := env.Cfg.LatLng()
	if !ok {
		return nil, fmt.Errorf("location is not set")
	}
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.4f", lat))
	q.Set("longitude", fmt.Sprintf("%.4f", lng))
	q.Set("current", "us_aqi,pm2_5,grass_pollen,birch_pollen,ragweed_pollen")
	var resp struct {
		Current struct {
			AQI     *float64 `json:"us_aqi"`
			PM25    *float64 `json:"pm2_5"`
			Grass   *float64 `json:"grass_pollen"`
			Birch   *float64 `json:"birch_pollen"`
			Ragweed *float64 `json:"ragweed_pollen"`
		} `json:"current"`
	}
	if err := httpx.GetJSON(ctx, "https://air-quality-api.open-meteo.com/v1/air-quality?"+q.Encode(), nil, &resp); err != nil {
		return nil, fmt.Errorf("open-meteo air quality: %w", err)
	}
	if resp.Current.AQI == nil {
		return EmptySection("air_quality"), nil
	}
	aqi := int(math.Round(*resp.Current.AQI))
	label := "Good"
	switch {
	case aqi > 300:
		label = "Hazardous"
	case aqi > 200:
		label = "Very unhealthy"
	case aqi > 150:
		label = "Unhealthy"
	case aqi > 100:
		label = "Unhealthy for sensitive groups"
	case aqi > 50:
		label = "Moderate"
	}
	pm := "?"
	if resp.Current.PM25 != nil {
		pm = fmt.Sprintf("%.0f", *resp.Current.PM25)
	}
	pollen := "n/a"
	var maxPollen float64 = -1
	for _, p := range []*float64{resp.Current.Grass, resp.Current.Birch, resp.Current.Ragweed} {
		if p != nil && *p > maxPollen {
			maxPollen = *p
		}
	}
	switch {
	case maxPollen < 0:
	case maxPollen < 10:
		pollen = "low"
	case maxPollen < 50:
		pollen = "moderate"
	default:
		pollen = "high"
	}
	return Exec("air_quality", airTpl, struct {
		Title, Label, PM25, Pollen string
		AQI                        int
	}{opt.Str("title", "Air quality"), label, pm, pollen, aqi}, 20+28)
}

func init() {
	Register(hourlyModule{})
	Register(dailyModule{})
	Register(sunMoonModule{})
	Register(airModule{})
}
