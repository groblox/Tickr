package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"breaklist/internal/connectors"
)

// ── Local events aggregator ──────────────────────────────────────────────────
//
// Pulls from several sources (iCalendar feeds, RSS feeds, Ticketmaster,
// Home Assistant calendars), each isolated so one broken feed never hides the
// others. Every source keeps its last good result on disk; when a fetch fails
// the cached events are used, and a source that keeps failing is paused with
// a growing back-off instead of slowing every morning's print.

type eventsModule struct{}

type eventRow struct {
	Title, When, Where, Source string
}

var eventsTpl = Tpl("events", `<div class="h">{{.Title}}</div><div class="events">{{range .Rows}}<div class="ev"><div class="ev-t">{{.Title}}</div><div class="ev-d">{{.When}}{{if .Where}} · {{.Where}}{{end}}{{if .Source}} <span class="src">[{{.Source}}]</span>{{end}}</div></div>{{end}}</div>{{if .Note}}<div class="s">{{.Note}}</div>{{end}}`)

func (eventsModule) Info() Info {
	return Info{
		ID: "events", Name: "Local events", Category: CatCore, DefaultEnabled: false,
		Description: "The next few things happening nearby, merged from calendar feeds (library, city, school, church…), RSS feeds, Ticketmaster and Home Assistant calendars. Broken feeds fall back to their last good copy.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Around town"},
			{Key: "count", Label: "Events to show", Type: FieldNumber, Default: 3, Min: F64(1), Max: F64(15)},
			{Key: "horizon", Label: "Look ahead (days)", Type: FieldNumber, Default: 14, Min: F64(1), Max: F64(90)},
			{Key: "icsUrls", Label: "Calendar feeds (iCal / .ics / webcal)", Type: FieldList, Help: "One URL per line. Add a name after a | to label it: https://…/events.ics | Library. A page URL usually works too: the linked .ics is found automatically."},
			{Key: "pageUrls", Label: "Event listing pages", Type: FieldList, Help: "Eventbrite, AllEvents, Meetup, venue and city pages: one URL per line, optional | label. The schema.org event data embedded in the page is used. Example: https://allevents.in/birmingham-al | AllEvents"},
			{Key: "rssUrls", Label: "RSS feeds", Type: FieldList, Help: "One URL per line, optional | label. Items are kept when a date within the window appears in the title or text. Single-event sites (a festival's own homepage) can go under listing pages."},
			{Key: "rssPages", Label: "RSS pages to read", Type: FieldNumber, Default: 3, Min: F64(1), Max: F64(10), Help: "WordPress feeds only return 10 posts per page; reading 3 pages covers about a month of a busy blog."},
			{Key: "haCalendars", Label: "Home Assistant calendars", Type: FieldList, Help: "calendar.* entity ids, one per line, optional | label."},
			{Key: "ticketmaster", Label: "Ticketmaster (concerts, sports, shows)", Type: FieldBool, Default: false, Help: "Needs the Ticketmaster key on the Connectors tab."},
			{Key: "seatgeek", Label: "SeatGeek (concerts, sports, theatre)", Type: FieldBool, Default: false, Help: "Needs the SeatGeek client id on the Connectors tab."},
			{Key: "radius", Label: "Ticketmaster / SeatGeek radius (miles)", Type: FieldNumber, Default: 30, Min: F64(5), Max: F64(200)},
			{Key: "include", Label: "Only keep events containing", Type: FieldText, Help: "Comma-separated words; blank keeps everything. Example: kids, story, family"},
			{Key: "exclude", Label: "Drop events containing", Type: FieldText, Default: "listings, cancelled", Help: "Comma-separated words. Example: closed, cancelled, 21+, listings"},
			{Key: "boost", Label: "Show these first", Type: FieldText, Default: "festival, oktoberfest, kids, family, market, parade, fireworks", Help: "Comma-separated words. Matching events are listed before the rest, soonest first."},
			{Key: "hideLongRunning", Label: "Hide season-long and every-day events", Type: FieldBool, Default: true, Help: "Drops things that run for more than a week or repeat daily (a zoo exhibition, 'VIP nights every Friday') that would otherwise sit in the top slot every day."},
			{Key: "showWhere", Label: "Show location", Type: FieldBool, Default: true},
			{Key: "showSource", Label: "Show source label", Type: FieldBool, Default: false},
			{Key: "oneSource", Label: "Max events per source", Type: FieldNumber, Default: 0, Min: F64(0), Max: F64(10), Help: "0 = no limit. Use 1–2 so a busy library feed does not crowd out everything else."},
		},
	}
}

// ── per-source cache & health ────────────────────────────────────────────────

type sourceState struct {
	Events    []connectors.Event `json:"events"`
	FetchedAt time.Time          `json:"fetchedAt"`
	Failures  int                `json:"failures"`
	NextTry   time.Time          `json:"nextTry"`
	LastError string             `json:"lastError,omitempty"`
}

var eventsCacheMu sync.Mutex

func loadEventsCache(path string) map[string]*sourceState {
	m := map[string]*sourceState{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func saveEventsCache(path string, m map[string]*sourceState) {
	if data, err := json.MarshalIndent(m, "", " "); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, data, 0o600)
	}
}

type eventSource struct {
	key, label string
	fetch      func(ctx context.Context) ([]connectors.Event, error)
}

func splitLabel(line, fallback string) (string, string) {
	v, label, ok := strings.Cut(line, "|")
	v = strings.TrimSpace(v)
	label = strings.TrimSpace(label)
	if !ok || label == "" {
		label = fallback
	}
	return v, label
}

func (eventsModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	now := env.Now
	from := now.Add(-time.Hour)
	to := now.AddDate(0, 0, opt.Int("horizon", 14))
	var sources []eventSource
	for _, line := range opt.List("icsUrls") {
		u, label := splitLabel(line, "")
		if u == "" {
			continue
		}
		sources = append(sources, eventSource{key: "ics:" + u, label: label, fetch: func(ctx context.Context) ([]connectors.Event, error) {
			return connectors.FetchICS(ctx, u, env.Loc, from, to.AddDate(0, 0, 60))
		}})
	}
	for _, line := range opt.List("rssUrls") {
		u, label := splitLabel(line, "")
		if u == "" {
			continue
		}
		sources = append(sources, eventSource{key: "rss:" + u, label: label, fetch: func(ctx context.Context) ([]connectors.Event, error) {
			return connectors.FetchRSS(ctx, u, env.Loc, from, to.AddDate(0, 0, 60), opt.Int("rssPages", 3))
		}})
	}
	for _, line := range opt.List("pageUrls") {
		u, label := splitLabel(line, "")
		if u == "" {
			continue
		}
		sources = append(sources, eventSource{key: "page:" + u, label: label, fetch: func(ctx context.Context) ([]connectors.Event, error) {
			return connectors.FetchPageEvents(ctx, u, env.Loc, from, to.AddDate(0, 0, 60))
		}})
	}
	for _, line := range opt.List("haCalendars") {
		ent, label := splitLabel(line, "")
		if ent == "" {
			continue
		}
		sources = append(sources, eventSource{key: "ha:" + ent, label: label, fetch: func(ctx context.Context) ([]connectors.Event, error) {
			ha, err := connectors.NewHomeAssistant(env.Cfg.Connectors.HomeAssistant)
			if err != nil {
				return nil, err
			}
			evs, err := ha.CalendarEvents(ctx, ent, from, to.AddDate(0, 0, 60))
			if err != nil {
				return nil, err
			}
			var out []connectors.Event
			for _, e := range evs {
				out = append(out, connectors.Event{Title: e.Summary, Start: e.Start, End: e.End, AllDay: e.AllDay, Source: ent})
			}
			return out, nil
		}})
	}
	if opt.Bool("ticketmaster", false) {
		lat, lng, ok := env.Cfg.LatLng()
		if ok {
			radius := floatOpt(opt, "radius", 30)
			sources = append(sources, eventSource{key: "ticketmaster", label: "Ticketmaster", fetch: func(ctx context.Context) ([]connectors.Event, error) {
				return connectors.FetchTicketmaster(ctx, env.Cfg.Connectors.Events.TicketmasterKey, lat, lng, radius, env.Loc, from, to.AddDate(0, 0, 60))
			}})
		} else {
			env.Log("events: Ticketmaster needs a location in General settings")
		}
	}
	if opt.Bool("seatgeek", false) {
		if lat, lng, ok := env.Cfg.LatLng(); ok {
			radius := floatOpt(opt, "radius", 30)
			sources = append(sources, eventSource{key: "seatgeek", label: "SeatGeek", fetch: func(ctx context.Context) ([]connectors.Event, error) {
				return connectors.FetchSeatGeek(ctx, env.Cfg.Connectors.Events.SeatGeekClientID, lat, lng, radius, env.Loc, from, to.AddDate(0, 0, 60))
			}})
		}
	}
	if len(sources) == 0 {
		return EmptySection("events"), nil
	}

	cachePath := filepath.Join(env.DataDir, "output", "events-cache.json")
	eventsCacheMu.Lock()
	cache := loadEventsCache(cachePath)
	eventsCacheMu.Unlock()

	type result struct {
		src    eventSource
		events []connectors.Event
		note   string
	}
	results := make([]result, len(sources))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, src := range sources {
		wg.Add(1)
		go func(i int, src eventSource) {
			defer wg.Done()
			mu.Lock()
			st := cache[src.key]
			if st == nil {
				st = &sourceState{}
				cache[src.key] = st
			}
			mu.Unlock()
			res := result{src: src}
			if now.Before(st.NextTry) && len(st.Events) > 0 {
				res.events = st.Events
				res.note = fmt.Sprintf("paused after %d failures (retry %s); using cached copy", st.Failures, st.NextTry.In(env.Loc).Format("Jan 2 3:04 PM"))
				results[i] = res
				return
			}
			fctx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			evs, err := src.fetch(fctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				st.Failures++
				st.LastError = err.Error()
				// 10 min, 30 min, 90 min … capped at 12 h.
				backoff := 10 * time.Minute * time.Duration(1<<min(st.Failures-1, 6))
				if backoff > 12*time.Hour {
					backoff = 12 * time.Hour
				}
				st.NextTry = now.Add(backoff)
				if len(st.Events) > 0 && now.Sub(st.FetchedAt) < 21*24*time.Hour {
					res.events = st.Events
					res.note = "fetch failed, using copy from " + st.FetchedAt.In(env.Loc).Format("Jan 2") + ": " + err.Error()
				} else {
					res.note = "fetch failed: " + err.Error()
				}
				results[i] = res
				return
			}
			st.Events = evs
			st.FetchedAt = now
			st.Failures = 0
			st.NextTry = time.Time{}
			st.LastError = ""
			res.events = evs
			results[i] = res
		}(i, src)
	}
	wg.Wait()
	eventsCacheMu.Lock()
	saveEventsCache(cachePath, cache)
	eventsCacheMu.Unlock()

	include := keywordList(opt.Str("include", ""))
	exclude := keywordList(opt.Str("exclude", ""))
	perSource := opt.Int("oneSource", 0)
	var all []connectors.Event
	failed := 0
	for _, r := range results {
		if r.note != "" {
			env.Log("events %s: %s", sourceName(r.src), r.note)
			if len(r.events) == 0 {
				failed++
			}
		} else {
			env.Log("events %s: %d events", sourceName(r.src), len(r.events))
		}
		for _, e := range r.events {
			if r.src.label != "" {
				e.Source = r.src.label
			}
			if e.Source == "" {
				e.Source = strings.TrimPrefix(strings.TrimPrefix(r.src.key, "ics:"), "rss:")
			}
			if e.Start.After(to) || e.End.Before(from) && !e.End.IsZero() || (e.End.IsZero() && e.Start.Before(from)) {
				continue
			}
			if !e.AllDay && e.Start.Before(now) && (e.End.IsZero() || e.End.Before(now)) {
				continue
			}
			if opt.Bool("hideLongRunning", true) && ((!e.End.IsZero() && e.End.Sub(e.Start) > 21*24*time.Hour) || e.Repeats == "daily" || connectors.LongRunningTitle(e.Title)) {
				continue
			}
			text := strings.ToLower(e.Title + " " + e.Location + " " + e.Category)
			if len(include) > 0 && !containsAny(text, include) {
				continue
			}
			if containsAny(text, exclude) {
				continue
			}
			all = append(all, e)
		}
	}
	// Series detection: a title that shows up on five or more different days
	// in the fetched window is an exhibition or a nightly thing, not an event.
	if opt.Bool("hideLongRunning", true) {
		days := map[string]map[string]bool{}
		for _, r := range results {
			for _, e := range r.events {
				k := titleKey(e.Title)
				if days[k] == nil {
					days[k] = map[string]bool{}
				}
				days[k][e.Start.Format("2006-01-02")] = true
			}
		}
		var kept []connectors.Event
		for _, e := range all {
			if len(days[titleKey(e.Title)]) >= 5 {
				continue
			}
			kept = append(kept, e)
		}
		all = kept
	}
	boost := keywordList(opt.Str("boost", ""))
	score := func(e connectors.Event) int {
		if containsAny(strings.ToLower(e.Title+" "+e.Category), boost) {
			return 0
		}
		return 1
	}
	sort.SliceStable(all, func(i, j int) bool {
		si, sj := score(all[i]), score(all[j])
		if si != sj {
			return si < sj
		}
		return all[i].Start.Before(all[j].Start)
	})
	// Deduplicate the same event listed by several sources, or by one source
	// under slightly different names: the first three meaningful words of the
	// title on the same day identify an event.
	seen := map[string]bool{}
	perSourceTaken := map[string]int{}
	var rows []eventRow
	for _, e := range all {
		k := titleKey(e.Title) + "|" + e.Start.Format("2006-01-02")
		if seen[k] {
			continue
		}
		seen[k] = true
		if perSource > 0 && perSourceTaken[e.Source] >= perSource {
			continue
		}
		perSourceTaken[e.Source]++
		row := eventRow{Title: e.Title, When: formatEventWhen(e, now)}
		if opt.Bool("showWhere", true) {
			row.Where = shortenLocation(e.Location)
		}
		if opt.Bool("showSource", false) {
			row.Source = e.Source
		}
		rows = append(rows, row)
		if len(rows) == opt.Int("count", 3) {
			break
		}
	}
	if len(rows) == 0 {
		if failed == len(sources) {
			return nil, fmt.Errorf("all %d event sources failed and no cached copies were available", len(sources))
		}
		return EmptySection("events"), nil
	}
	note := ""
	if failed > 0 {
		note = fmt.Sprintf("(%d source(s) unavailable)", failed)
	}
	est := 20
	for _, r := range rows {
		est += LinesPx(r.Title, 34, 10) + 12
	}
	return Exec("events", eventsTpl, struct {
		Title, Note string
		Rows        []eventRow
	}{opt.Str("title", "Around town"), note, rows}, est)
}

func formatEventWhen(e connectors.Event, now time.Time) string {
	day := e.Start
	dayLabel := day.Format("Mon Jan 2")
	switch {
	case sameDay(day, now):
		dayLabel = "Today"
	case sameDay(day, now.AddDate(0, 0, 1)):
		dayLabel = "Tomorrow"
	}
	if e.AllDay {
		if !e.End.IsZero() && e.End.Sub(e.Start) > 24*time.Hour {
			return dayLabel + " – " + e.End.AddDate(0, 0, -1).Format("Mon Jan 2")
		}
		return dayLabel
	}
	s := dayLabel + ", " + e.Start.Format("3:04 PM")
	if !e.End.IsZero() && e.End.After(e.Start) && e.End.Sub(e.Start) < 12*time.Hour {
		s += "–" + e.End.Format("3:04 PM")
	}
	return s
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// shortenLocation keeps the venue name and drops street addresses.
func shortenLocation(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ",")
	first := strings.TrimSpace(parts[0])
	// If the first part looks like a street address, prefer the second.
	if len(first) > 0 && first[0] >= '0' && first[0] <= '9' && len(parts) > 1 {
		first = strings.TrimSpace(parts[1])
	}
	r := []rune(first)
	if len(r) > 34 {
		return string(r[:33]) + "…"
	}
	return first
}

// normalizeTitle lower-cases, strips punctuation and collapses spaces so
// "Oktoberfest 2026!" and "OKTOBERFEST 2026" compare equal.
func normalizeTitle(s string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

var titleStop = map[string]bool{"a": true, "an": true, "the": true, "at": true, "in": true, "on": true, "of": true, "and": true, "with": true, "for": true, "to": true, "by": true, "vs": true, "tickets": true, "presents": true, "featuring": true, "ft": true, "live": true}

// titleKey reduces a title to its first three meaningful words.
func titleKey(title string) string {
	var words []string
	for _, w := range strings.Fields(normalizeTitle(title)) {
		if titleStop[w] || len(w) < 2 {
			continue
		}
		words = append(words, w)
		if len(words) == 3 {
			break
		}
	}
	if len(words) == 0 {
		return normalizeTitle(title)
	}
	return strings.Join(words, " ")
}

func keywordList(s string) []string {
	var out []string
	for _, w := range strings.Split(strings.ToLower(s), ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

func containsAny(text string, words []string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func sourceName(s eventSource) string {
	ref := s.key[strings.Index(s.key, ":")+1:]
	if s.label != "" {
		return s.label + " (" + ref + ")"
	}
	return ref
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func init() {
	Register(eventsModule{})
}
