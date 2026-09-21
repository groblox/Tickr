package modules

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tickr/internal/httpx"
)

// ── Garden & plant care ──────────────────────────────────────────────────────

type gardenModule struct{}

var gardenTpl = Tpl("garden", `<div class="h">{{.Title}}</div>
<div class="t"><b>Rain:</b> {{.Rain}}</div>
<div class="t"><b>Today:</b> {{.Today}}</div>
{{range .Alerts}}<div class="t" style="margin-top:2px">&#9888; {{.}}</div>{{end}}
{{if .Water}}<div class="t" style="margin-top:3px"><b>Water today:</b> {{.Water}}</div>{{end}}
{{if .Skip}}<div class="s">Fine to skip: {{.Skip}}</div>{{end}}
{{if .Tip}}<div class="s" style="margin-top:3px"><i>{{.Tip}}</i></div>{{end}}`)

func (gardenModule) Info() Info {
	return Info{
		ID: "garden", Name: "Garden & plant care", Category: CatWeather, DefaultEnabled: false, Needs: []string{"weather"},
		Description: "Rain over the last week, today's heat and UV, frost and heat warnings, and which plants are due for water based on how long it has been since real rain. Today's conditions share the report's weather forecast; rain history and evapotranspiration are Open-Meteo-only figures fetched separately.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Garden"},
			{Key: "plants", Label: "Plants", Type: FieldList, Default: "Tomatoes | 2\nPeppers | 2\nHerbs | 3\nHanging baskets | 1\nShrubs | 7",
				Help: "One per line: name | days between waterings. A plant is due when it has been at least that many days since rain of a quarter inch or more, and today is not likely to rain."},
			{Key: "rainThreshold", Label: "Counts as real rain (mm)", Type: FieldNumber, Default: 6, Min: F64(1), Max: F64(25), Help: "6 mm ≈ ¼ inch."},
			{Key: "frostC", Label: "Frost warning below (°C)", Type: FieldNumber, Default: 2, Min: F64(-10), Max: F64(10)},
			{Key: "heatC", Label: "Heat warning above (°C)", Type: FieldNumber, Default: 34, Min: F64(25), Max: F64(45)},
			{Key: "tips", Label: "Monthly tip", Type: FieldBool, Default: true},
		},
	}
}

func (gardenModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	// Today's high/low/UV/wind/rain-probability come from the report's shared
	// forecast (whichever provider is configured), so garden agrees with the
	// rest of the report instead of quietly using a second, unconfigured source.
	fc, err := env.Forecast(ctx)
	if err != nil {
		return nil, err
	}
	today := time.Date(env.Now.Year(), env.Now.Month(), env.Now.Day(), 0, 0, 0, 0, env.Loc)
	todayFC := todayDaily(fc, env.Now)
	if todayFC == nil {
		return nil, fmt.Errorf("no forecast data for today")
	}

	// Rain history and evapotranspiration are Open-Meteo-only figures no
	// other provider exposes, so those two fields alone are fetched directly.
	lat, lng, ok := env.Cfg.LatLng()
	if !ok {
		return nil, fmt.Errorf("location is not set")
	}
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.4f", lat))
	q.Set("longitude", fmt.Sprintf("%.4f", lng))
	q.Set("daily", "precipitation_sum,et0_fao_evapotranspiration")
	q.Set("timezone", env.Cfg.General.Timezone)
	q.Set("past_days", "7")
	q.Set("forecast_days", "1")
	var resp struct {
		Daily struct {
			Time   []string   `json:"time"`
			Precip []*float64 `json:"precipitation_sum"`
			ET0    []*float64 `json:"et0_fao_evapotranspiration"`
		} `json:"daily"`
	}
	if err := httpx.GetJSON(ctx, "https://api.open-meteo.com/v1/forecast?"+q.Encode(), nil, &resp); err != nil {
		return nil, fmt.Errorf("open-meteo: %w", err)
	}
	f := func(a []*float64, i int) float64 {
		if i < len(a) && a[i] != nil {
			return *a[i]
		}
		return 0
	}
	threshold := floatOpt(opt, "rainThreshold", 6)
	var weekRain, et0 float64
	daysSinceRain := 99
	for i, ds := range resp.Daily.Time {
		d, err := time.ParseInLocation("2006-01-02", ds, env.Loc)
		if err != nil {
			continue
		}
		if d.Equal(today) {
			et0 = f(resp.Daily.ET0, i)
		}
		if d.After(today) {
			continue
		}
		p := f(resp.Daily.Precip, i)
		if d.After(today.AddDate(0, 0, -7)) {
			weekRain += p
		}
		if p >= threshold {
			gap := int(today.Sub(d).Hours() / 24)
			if gap < daysSinceRain {
				daysSinceRain = gap
			}
		}
	}
	hi, lo := todayFC.MaxC, todayFC.MinC
	uv := todayFC.UVIndex
	wind := todayFC.WindMaxKmh
	prob := todayFC.PrecipProb

	data := struct {
		Title, Rain, Today, Water, Skip, Tip string
		Alerts                               []string
	}{Title: opt.Str("title", "Garden")}
	last := "no real rain in the last week"
	switch {
	case daysSinceRain == 0:
		last = "real rain today"
	case daysSinceRain == 1:
		last = "last real rain yesterday"
	case daysSinceRain < 99:
		last = fmt.Sprintf("last real rain %d days ago", daysSinceRain)
	}
	data.Rain = fmt.Sprintf("%s in the last 7 days, %s", env.Depth(weekRain), last)
	data.Today = fmt.Sprintf("high %s, low %s, UV %.0f, wind %s, %d%% chance of rain", env.Temp(hi), env.Temp(lo), uv, env.Speed(wind), prob)
	if et0 > 0 {
		data.Today += fmt.Sprintf(", plants lose ≈%s", env.Depth(et0))
	}

	frostC := floatOpt(opt, "frostC", 2)
	heatC := floatOpt(opt, "heatC", 34)
	for _, d := range fc.Daily {
		if d.Date.Before(today) || d.Date.After(today.AddDate(0, 0, 2)) {
			continue // only tonight and the following two nights
		}
		when := "tonight"
		if !d.Date.Equal(today) {
			when = d.Date.Format("Mon") + " night"
		}
		if d.MinC <= frostC {
			data.Alerts = append(data.Alerts, fmt.Sprintf("Frost risk %s (%s): cover tender plants, bring pots in.", when, env.Temp(d.MinC)))
		}
	}
	if hi >= heatC {
		data.Alerts = append(data.Alerts, fmt.Sprintf("Heat: %s today. Water early, shade seedlings, check pots twice.", env.Temp(hi)))
	}
	if wind >= 40 {
		data.Alerts = append(data.Alerts, fmt.Sprintf("Windy (%s): stake tall plants, move hanging baskets.", env.Speed(wind)))
	}
	if uv >= 8 {
		data.Alerts = append(data.Alerts, "Very high UV: hat and sunscreen for the gardener too.")
	}

	var due, skip []string
	rainLikely := prob >= 60
	for _, line := range opt.List("plants") {
		name, daysStr, _ := strings.Cut(line, "|")
		name = strings.TrimSpace(name)
		days, err := strconv.Atoi(strings.TrimSpace(daysStr))
		if name == "" || err != nil || days <= 0 {
			continue
		}
		if daysSinceRain >= days && !rainLikely {
			due = append(due, name)
		} else {
			skip = append(skip, name)
		}
	}
	if len(due) > 0 {
		data.Water = strings.Join(due, ", ")
	} else if len(skip) > 0 {
		if rainLikely {
			data.Water = "nothing, rain is likely today"
		} else {
			data.Water = "nothing, the ground is still wet"
		}
		skip = nil
	}
	if len(skip) > 0 && len(due) > 0 {
		data.Skip = strings.Join(skip, ", ")
	}
	if opt.Bool("tips", true) {
		data.Tip = gardenTips[env.Now.Month()][env.DaySeed()%len(gardenTips[env.Now.Month()])]
	}
	est := 20 + LinesPx(data.Rain, 30, 14) + LinesPx(data.Today, 30, 14) + len(data.Alerts)*28 + 16
	if data.Tip != "" {
		est += LinesPx(data.Tip, 36, 10) + 6
	}
	return Exec("garden", gardenTpl, data, est)
}

// gardenTips are short, month-specific reminders for a temperate/southern US garden.
var gardenTips = map[time.Month][]string{
	time.January:   {"Order seeds now; the good varieties sell out.", "Prune dormant fruit trees and roses on a mild day.", "Check stored bulbs and tubers for rot."},
	time.February:  {"Start tomato and pepper seeds indoors 6–8 weeks before the last frost.", "Cut back ornamental grasses before new growth.", "Spread compost on beds while they are still empty."},
	time.March:     {"Plant potatoes, peas, lettuce and onions once the soil can be worked.", "Pull winter weeds before they set seed.", "Mulch beds to hold spring moisture."},
	time.April:     {"Harden off seedlings for a week before planting out.", "Set out tomatoes after the last frost; bury the stems deep.", "Feed azaleas and camellias after they bloom."},
	time.May:       {"Mulch 2–3 inches deep around vegetables to keep roots cool.", "Pinch basil to keep it bushy.", "Watch for aphids on new growth; a hose blast does wonders."},
	time.June:      {"Water deeply and less often; shallow daily watering makes weak roots.", "Sow a second round of beans and squash.", "Deadhead annuals to keep them flowering."},
	time.July:      {"Water in the early morning to beat evaporation.", "Harvest zucchini small and often.", "Give container plants a diluted feed every two weeks."},
	time.August:    {"Start fall crops: broccoli, kale, carrots, lettuce.", "Let a few herbs flower for the pollinators.", "Check irrigation lines for clogs during the heat."},
	time.September: {"Plant garlic and shallots for next summer.", "Sow spinach, radishes and arugula for a fall harvest.", "Divide crowded perennials while the soil is warm."},
	time.October:   {"Plant spring bulbs before the ground gets cold.", "Bring tender pots inside before the first frost.", "Rake leaves into beds as free mulch."},
	time.November:  {"Clean and oil tools before storing them.", "Plant bare-root trees and shrubs while dormant.", "Empty and store hoses before a hard freeze."},
	time.December:  {"Water evergreens during dry spells; winter drought is real.", "Plan next year's beds and rotate the tomato spot.", "Feed the birds and leave seed heads standing for them."},
}

func init() {
	Register(gardenModule{})
}
