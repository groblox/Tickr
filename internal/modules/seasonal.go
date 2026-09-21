package modules

import (
	"context"
	"sort"
	"strings"
	"time"

	"breaklist/internal/astro"
)

// ── Seasons & sky calendar ───────────────────────────────────────────────────

type seasonalModule struct{}

type seasonalRow struct {
	Label string
	Days  int
	When  string
	Kind  string
}

var seasonalTpl = Tpl("seasonal", `<div class="h">{{.Title}}</div><div class="events">{{range .Rows}}<div class="ev"><div class="ev-t">{{.Label}}</div><div class="ev-d">{{if eq .Days 0}}<b>Today</b>{{else if eq .Days 1}}Tomorrow{{else}}In {{.Days}} days{{end}} · {{.When}}</div></div>{{end}}</div>`)

func (seasonalModule) Info() Info {
	return Info{
		ID: "seasonal", Name: "Seasons & sky calendar", Category: CatCore, DefaultEnabled: false,
		Description: "What's coming up: equinoxes and solstices, full and new moons, meteor showers, daylight-saving changes, holidays and observances, plus any dates you add.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Coming up"},
			{Key: "horizon", Label: "Look ahead (days)", Type: FieldNumber, Default: 45, Min: F64(1), Max: F64(400)},
			{Key: "max", Label: "Max items", Type: FieldNumber, Default: 8, Min: F64(1), Max: F64(30)},
			{Key: "seasons", Label: "Equinoxes & solstices", Type: FieldBool, Default: true},
			{Key: "fullMoons", Label: "Full moons", Type: FieldBool, Default: true},
			{Key: "newMoons", Label: "New moons", Type: FieldBool, Default: false},
			{Key: "meteors", Label: "Meteor shower peaks", Type: FieldBool, Default: true},
			{Key: "dst", Label: "Daylight-saving changes (US)", Type: FieldBool, Default: true},
			{Key: "observances", Label: "US holidays & observances", Type: FieldBool, Default: true},
			{Key: "extra", Label: "Your own dates", Type: FieldList, Help: "One per line: \"YYYY-MM-DD Label\" for a one-off or \"MM-DD Label\" for every year. Example: 03-14 Louella's birthday"},
		},
	}
}

func (seasonalModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	today := time.Date(env.Now.Year(), env.Now.Month(), env.Now.Day(), 0, 0, 0, 0, env.Loc)
	horizon := opt.Int("horizon", 45)
	end := today.AddDate(0, 0, horizon+1)
	var rows []seasonalRow
	add := func(kind, label string, when time.Time) {
		d := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, env.Loc)
		days := int(d.Sub(today).Hours()/24 + 0.5)
		if days < 0 || days > horizon {
			return
		}
		rows = append(rows, seasonalRow{Label: label, Days: days, When: when.Format("Mon Jan 2"), Kind: kind})
	}
	lat, _, hasLoc := env.Cfg.LatLng()
	northern := !hasLoc || lat >= 0

	if opt.Bool("seasons", true) {
		for _, ev := range astro.Seasons(today, end) {
			local := ev.Time.In(env.Loc)
			kind := "equinox"
			if ev.Season == astro.JuneSolstice || ev.Season == astro.DecemberSolstice {
				kind = "solstice"
			}
			add("season", "First day of "+ev.Season.Name(northern)+" · "+kind+" at "+local.Format("3:04 PM"), local)
		}
	}
	if opt.Bool("fullMoons", true) || opt.Bool("newMoons", false) {
		for _, ev := range astro.MoonPhases(today, end) {
			local := ev.Time.In(env.Loc)
			switch {
			case ev.Phase == astro.FullMoon && opt.Bool("fullMoons", true):
				add("moon", "Full moon · "+astro.FullMoonName(local.Month())+" at "+local.Format("3:04 PM"), local)
			case ev.Phase == astro.NewMoon && opt.Bool("newMoons", false):
				add("moon", "New moon", local)
			}
		}
	}
	if opt.Bool("meteors", true) {
		for _, m := range meteorShowers {
			for _, y := range []int{today.Year(), today.Year() + 1} {
				add("meteor", m.name+" meteor shower peak", time.Date(y, m.month, m.day, 12, 0, 0, 0, env.Loc))
			}
		}
	}
	if opt.Bool("dst", true) {
		for _, y := range []int{today.Year(), today.Year() + 1} {
			add("dst", "Clocks spring forward (DST starts)", nthWeekday(y, time.March, time.Sunday, 2, env.Loc))
			add("dst", "Clocks fall back (DST ends)", nthWeekday(y, time.November, time.Sunday, 1, env.Loc))
		}
	}
	if opt.Bool("observances", true) {
		for _, y := range []int{today.Year(), today.Year() + 1} {
			for _, o := range usObservances(y, env.Loc) {
				add("observance", o.label, o.when)
			}
		}
	}
	for _, line := range opt.List("extra") {
		datePart, label, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if t, err := time.ParseInLocation("2006-01-02", datePart, env.Loc); err == nil {
			add("custom", strings.TrimSpace(label), t)
		} else if t, err := time.ParseInLocation("01-02", datePart, env.Loc); err == nil {
			for _, y := range []int{today.Year(), today.Year() + 1} {
				add("custom", strings.TrimSpace(label), time.Date(y, t.Month(), t.Day(), 0, 0, 0, 0, env.Loc))
			}
		}
	}
	if len(rows) == 0 {
		return EmptySection("seasonal"), nil
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Days < rows[j].Days })
	// Deduplicate identical label+day pairs (e.g. yearly extras hitting both years).
	var dedup []seasonalRow
	for _, r := range rows {
		if n := len(dedup); n > 0 && dedup[n-1].Label == r.Label && dedup[n-1].Days == r.Days {
			continue
		}
		dedup = append(dedup, r)
	}
	if m := opt.Int("max", 8); len(dedup) > m {
		dedup = dedup[:m]
	}
	est := 20
	for _, r := range dedup {
		est += LinesPx(r.Label, 34, 10) + 12
	}
	return Exec("seasonal", seasonalTpl, struct {
		Title string
		Rows  []seasonalRow
	}{opt.Str("title", "Coming up"), dedup}, est)
}

var meteorShowers = []struct {
	name  string
	month time.Month
	day   int
}{
	{"Quadrantids", time.January, 3}, {"Lyrids", time.April, 22}, {"Eta Aquariids", time.May, 6},
	{"Perseids", time.August, 12}, {"Orionids", time.October, 21}, {"Leonids", time.November, 17},
	{"Geminids", time.December, 14}, {"Ursids", time.December, 22},
}

// nthWeekday returns the n-th weekday of a month (n = -1 for the last one).
func nthWeekday(year int, month time.Month, wd time.Weekday, n int, loc *time.Location) time.Time {
	if n > 0 {
		first := time.Date(year, month, 1, 0, 0, 0, 0, loc)
		offset := (int(wd) - int(first.Weekday()) + 7) % 7
		return first.AddDate(0, 0, offset+(n-1)*7)
	}
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc)
	offset := (int(last.Weekday()) - int(wd) + 7) % 7
	return last.AddDate(0, 0, -offset)
}

// easter returns Easter Sunday (Gregorian, Anonymous algorithm).
func easter(year int, loc *time.Location) time.Time {
	a := year % 19
	b := year / 100
	c := year % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, loc)
}

type observance struct {
	label string
	when  time.Time
}

func usObservances(y int, loc *time.Location) []observance {
	fixed := func(m time.Month, d int, label string) observance {
		return observance{label, time.Date(y, m, d, 0, 0, 0, 0, loc)}
	}
	e := easter(y, loc)
	tg := nthWeekday(y, time.November, time.Thursday, 4, loc)
	return []observance{
		fixed(time.January, 1, "New Year's Day"),
		{"Martin Luther King Jr. Day", nthWeekday(y, time.January, time.Monday, 3, loc)},
		fixed(time.February, 2, "Groundhog Day"),
		fixed(time.February, 14, "Valentine's Day"),
		{"Presidents' Day", nthWeekday(y, time.February, time.Monday, 3, loc)},
		{"Mardi Gras", e.AddDate(0, 0, -47)},
		fixed(time.March, 17, "St. Patrick's Day"),
		{"Good Friday", e.AddDate(0, 0, -2)},
		{"Easter Sunday", e},
		fixed(time.April, 1, "April Fools' Day"),
		fixed(time.April, 22, "Earth Day"),
		fixed(time.May, 5, "Cinco de Mayo"),
		{"Mother's Day", nthWeekday(y, time.May, time.Sunday, 2, loc)},
		{"Memorial Day", nthWeekday(y, time.May, time.Monday, -1, loc)},
		{"Father's Day", nthWeekday(y, time.June, time.Sunday, 3, loc)},
		fixed(time.June, 19, "Juneteenth"),
		fixed(time.July, 4, "Independence Day"),
		{"Labor Day", nthWeekday(y, time.September, time.Monday, 1, loc)},
		{"Indigenous Peoples' Day", nthWeekday(y, time.October, time.Monday, 2, loc)},
		fixed(time.October, 31, "Halloween"),
		fixed(time.November, 11, "Veterans Day"),
		{"Thanksgiving", tg},
		{"Black Friday", tg.AddDate(0, 0, 1)},
		fixed(time.December, 24, "Christmas Eve"),
		fixed(time.December, 25, "Christmas Day"),
		fixed(time.December, 31, "New Year's Eve"),
	}
}

func init() {
	Register(seasonalModule{})
}
