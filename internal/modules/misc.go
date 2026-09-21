package modules

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"tickr/internal/httpx"
)

// ── Countdowns ───────────────────────────────────────────────────────────────

type countdownModule struct{}

type countdownRow struct {
	Label string
	Days  int
	When  string
}

var countdownTpl = Tpl("countdown", `<div class="h">{{.Title}}</div><table class="kv">{{range .Rows}}<tr><td>{{.Label}}</td><td>{{if eq .Days 0}}<b>TODAY!</b>{{else if eq .Days 1}}tomorrow{{else}}{{.Days}} days{{end}} <span class="s">{{.When}}</span></td></tr>{{end}}</table>`)

func (countdownModule) Info() Info {
	return Info{
		ID: "countdown", Name: "Countdowns", Category: CatCore, DefaultEnabled: false,
		Description: "Days until birthdays, trips and holidays you list. Yearly dates roll over automatically.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Coming up"},
			{Key: "events", Label: "Events", Type: FieldList, Help: "One per line as \"YYYY-MM-DD Label\" (one-off) or \"MM-DD Label\" (every year). Example: 12-25 Christmas"},
			{Key: "horizon", Label: "Only show within (days)", Type: FieldNumber, Default: 60, Min: F64(1), Max: F64(400)},
		},
	}
}

func (countdownModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	today := time.Date(env.Now.Year(), env.Now.Month(), env.Now.Day(), 0, 0, 0, 0, env.Loc)
	horizon := opt.Int("horizon", 60)
	var rows []countdownRow
	for _, line := range opt.List("events") {
		datePart, label, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		var when time.Time
		if t, err := time.ParseInLocation("2006-01-02", datePart, env.Loc); err == nil {
			when = t
		} else if t, err := time.ParseInLocation("01-02", datePart, env.Loc); err == nil {
			when = time.Date(today.Year(), t.Month(), t.Day(), 0, 0, 0, 0, env.Loc)
			if when.Before(today) {
				when = when.AddDate(1, 0, 0)
			}
		} else {
			continue
		}
		days := int(when.Sub(today).Hours()/24 + 0.5)
		if days < 0 || days > horizon {
			continue
		}
		rows = append(rows, countdownRow{Label: strings.TrimSpace(label), Days: days, When: when.Format("Mon Jan 2")})
	}
	if len(rows) == 0 {
		return EmptySection("countdown"), nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Days < rows[j].Days })
	return Exec("countdown", countdownTpl, struct {
		Title string
		Rows  []countdownRow
	}{opt.Str("title", "Coming up"), rows}, 20+len(rows)*13)
}

// ── Public holidays (Nager.Date) ─────────────────────────────────────────────

type holidayModule struct{}

func (holidayModule) Info() Info {
	return Info{
		ID: "holidays", Name: "Public holidays", Category: CatCore, DefaultEnabled: false, Source: "date.nager.at",
		Description: "Upcoming public holidays for your country.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Holidays"},
			{Key: "country", Label: "Country code", Type: FieldText, Default: "US", Help: "ISO 3166 two-letter code, e.g. US, GB, DE"},
			{Key: "count", Label: "How many", Type: FieldNumber, Default: 3, Min: F64(1), Max: F64(10)},
		},
	}
}

func (holidayModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	country := strings.ToUpper(strings.TrimSpace(opt.Str("country", "US")))
	var list []struct {
		Date      string `json:"date"`
		LocalName string `json:"localName"`
		Name      string `json:"name"`
	}
	if err := httpx.GetJSON(ctx, "https://date.nager.at/api/v3/NextPublicHolidays/"+country, nil, &list); err != nil {
		return nil, fmt.Errorf("nager.date: %w", err)
	}
	today := time.Date(env.Now.Year(), env.Now.Month(), env.Now.Day(), 0, 0, 0, 0, env.Loc)
	var rows []countdownRow
	for _, h := range list {
		t, err := time.ParseInLocation("2006-01-02", h.Date, env.Loc)
		if err != nil {
			continue
		}
		days := int(t.Sub(today).Hours()/24 + 0.5)
		if days < 0 {
			continue
		}
		rows = append(rows, countdownRow{Label: h.LocalName, Days: days, When: t.Format("Mon Jan 2")})
		if len(rows) == opt.Int("count", 3) {
			break
		}
	}
	if len(rows) == 0 {
		return EmptySection("holidays"), nil
	}
	return Exec("holidays", countdownTpl, struct {
		Title string
		Rows  []countdownRow
	}{opt.Str("title", "Holidays"), rows}, 20+len(rows)*13)
}

// ── Crypto prices (CoinGecko) ────────────────────────────────────────────────

type cryptoModule struct{}

type priceRow struct {
	Name, Price, Change string
	Up                  bool
}

var priceTpl = Tpl("prices", `<div class="h">{{.Title}}</div><table class="kv">{{range .Rows}}<tr><td>{{.Name}}</td><td>{{.Price}} <span class="s">{{if .Up}}&#9650;{{else}}&#9660;{{end}}{{.Change}}</span></td></tr>{{end}}</table>`)

func (cryptoModule) Info() Info {
	return Info{
		ID: "crypto", Name: "Crypto prices", Category: CatNews, DefaultEnabled: false, Source: "coingecko.com",
		Description: "Spot prices and 24h change for the coins you list.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Crypto"},
			{Key: "coins", Label: "Coins", Type: FieldText, Default: "bitcoin,ethereum", Help: "CoinGecko ids, comma separated (bitcoin, ethereum, solana, dogecoin…)"},
			{Key: "currency", Label: "Currency", Type: FieldText, Default: "usd"},
		},
	}
}

func (cryptoModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	coins := strings.ReplaceAll(strings.ToLower(opt.Str("coins", "bitcoin,ethereum")), " ", "")
	cur := strings.ToLower(opt.Str("currency", "usd"))
	var res map[string]map[string]float64
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=%s&include_24hr_change=true", coins, cur)
	if err := httpx.GetJSON(ctx, url, nil, &res); err != nil {
		return nil, fmt.Errorf("coingecko: %w", err)
	}
	var rows []priceRow
	for _, id := range strings.Split(coins, ",") {
		v, ok := res[id]
		if !ok {
			continue
		}
		price := v[cur]
		change := v[cur+"_24h_change"]
		p := fmt.Sprintf("%.2f", price)
		if price >= 1000 {
			p = fmt.Sprintf("%.0f", price)
		} else if price < 1 {
			p = fmt.Sprintf("%.4f", price)
		}
		rows = append(rows, priceRow{Name: strings.ToUpper(id[:1]) + id[1:], Price: p + " " + strings.ToUpper(cur), Change: fmt.Sprintf("%.1f%%", abs(change)), Up: change >= 0})
	}
	if len(rows) == 0 {
		return EmptySection("crypto"), nil
	}
	return Exec("crypto", priceTpl, struct {
		Title string
		Rows  []priceRow
	}{opt.Str("title", "Crypto"), rows}, 20+len(rows)*13)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ── People in space ──────────────────────────────────────────────────────────

type spaceModule struct{}

var spaceTpl = Tpl("space", `<div class="h">{{.Title}}</div><div class="c t">There are <span class="big">{{.Count}}</span> people in space right now</div>{{if .Names}}<div class="c s">{{.Names}}</div>{{end}}{{if .ISS}}<div class="c s">The ISS is over {{.ISS}}</div>{{end}}`)

func (spaceModule) Info() Info {
	return Info{
		ID: "space", Name: "People in space", Category: CatFun, DefaultEnabled: false, Source: "open-notify.org",
		Description: "How many humans are in orbit right now, and roughly where the ISS is.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Space report"},
			{Key: "names", Label: "List astronaut names", Type: FieldBool, Default: false},
			{Key: "iss", Label: "Show ISS position", Type: FieldBool, Default: true},
		},
	}
}

func (spaceModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	var astros struct {
		Number int `json:"number"`
		People []struct {
			Name  string `json:"name"`
			Craft string `json:"craft"`
		} `json:"people"`
	}
	if err := httpx.GetJSON(ctx, "http://api.open-notify.org/astros.json", nil, &astros); err != nil {
		return nil, fmt.Errorf("open-notify: %w", err)
	}
	data := struct {
		Title, Names, ISS string
		Count             int
	}{Title: opt.Str("title", "Space report"), Count: astros.Number}
	if opt.Bool("names", false) {
		var names []string
		for _, p := range astros.People {
			names = append(names, p.Name)
		}
		data.Names = strings.Join(names, ", ")
	}
	if opt.Bool("iss", true) {
		var iss struct {
			Pos struct {
				Lat string `json:"latitude"`
				Lng string `json:"longitude"`
			} `json:"iss_position"`
		}
		if err := httpx.GetJSON(ctx, "http://api.open-notify.org/iss-now.json", nil, &iss); err == nil {
			var lat, lng float64
			fmt.Sscanf(iss.Pos.Lat, "%f", &lat)
			fmt.Sscanf(iss.Pos.Lng, "%f", &lng)
			data.ISS = describeLatLng(lat, lng)
		}
	}
	return Exec("space", spaceTpl, data, 20+18+LinesPx(data.Names, 40, 9)+10)
}

// describeLatLng gives a very rough "where on Earth" phrase without a geocoder.
func describeLatLng(lat, lng float64) string {
	ns := "N"
	if lat < 0 {
		ns = "S"
	}
	ew := "E"
	if lng < 0 {
		ew = "W"
	}
	region := "the ocean"
	switch {
	case lat > 15 && lat < 72 && lng > -170 && lng < -50:
		region = "North America"
	case lat > -56 && lat <= 15 && lng > -82 && lng < -34:
		region = "South America"
	case lat > 35 && lat < 72 && lng > -10 && lng < 40:
		region = "Europe"
	case lat > -35 && lat <= 35 && lng > -18 && lng < 52:
		region = "Africa"
	case lat > 5 && lat < 78 && lng >= 40 && lng < 180:
		region = "Asia"
	case lat > -45 && lat < -10 && lng > 112 && lng < 155:
		region = "Australia"
	case lat < -60:
		region = "Antarctica"
	}
	return fmt.Sprintf("%s (%.0f°%s, %.0f°%s)", region, abs(lat), ns, abs(lng), ew)
}

func init() {
	Register(countdownModule{})
	Register(holidayModule{})
	Register(cryptoModule{})
	Register(spaceModule{})
}
