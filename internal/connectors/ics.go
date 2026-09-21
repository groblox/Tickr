package connectors

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"breaklist/internal/httpx"
)

// Event is one calendar entry from any source.
type Event struct {
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end,omitempty"`
	AllDay   bool      `json:"allDay"`
	Location string    `json:"location,omitempty"`
	URL      string    `json:"url,omitempty"`
	Source   string    `json:"source"`
	Category string    `json:"category,omitempty"`
	Repeats  string    `json:"repeats,omitempty"` // "daily", "weekly"… for instances expanded from a recurrence rule
}

// ── fetching ─────────────────────────────────────────────────────────────────

// FetchICS downloads a calendar. It accepts webcal:// URLs, follows an HTML
// page to a linked .ics feed when given one by mistake, and retries briefly.
func FetchICS(ctx context.Context, rawURL string, loc *time.Location, from, to time.Time) ([]Event, error) {
	u := strings.TrimSpace(rawURL)
	u = strings.Replace(u, "webcal://", "https://", 1)
	body, ctype, err := fetchWithRetry(ctx, u)
	if err != nil {
		return nil, err
	}
	if !looksLikeICS(body) {
		// Self-heal: a page URL was given. Try the link the page advertises,
		// then the WordPress "The Events Calendar" export convention.
		candidates := []string{}
		if alt := discoverICSLink(body, u); alt != "" {
			candidates = append(candidates, alt)
		}
		if !strings.Contains(u, "ical=1") {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			candidates = append(candidates, u+sep+"ical=1")
		}
		for _, alt := range candidates {
			if b, _, err := fetchWithRetry(ctx, alt); err == nil && looksLikeICS(b) {
				body = b
				break
			}
		}
		if !looksLikeICS(body) {
			return nil, fmt.Errorf("%s is not an iCalendar feed (content-type %s)", u, ctype)
		}
	}
	return ParseICS(body, loc, from, to)
}

// FetchSeatGeek lists events near a point via SeatGeek's public API (free client id).
func FetchSeatGeek(ctx context.Context, clientID string, lat, lng, radiusMiles float64, loc *time.Location, from, to time.Time) ([]Event, error) {
	if clientID == "" {
		return nil, fmt.Errorf("SeatGeek client id is not set")
	}
	u := fmt.Sprintf("https://api.seatgeek.com/2/events?client_id=%s&lat=%.4f&lon=%.4f&range=%dmi&sort=datetime_local.asc&per_page=50&datetime_local.gte=%s&datetime_local.lte=%s",
		clientID, lat, lng, int(radiusMiles), from.Format("2006-01-02"), to.Format("2006-01-02"))
	var res struct {
		Events []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			DatetimeLocal string `json:"datetime_local"`
			TimeTBD       bool   `json:"time_tbd"`
			Type          string `json:"type"`
			Venue         struct {
				Name string `json:"name"`
				City string `json:"city"`
			} `json:"venue"`
		} `json:"events"`
	}
	if err := httpx.GetJSON(ctx, u, nil, &res); err != nil {
		return nil, fmt.Errorf("seatgeek: %w", err)
	}
	var out []Event
	for _, ev := range res.Events {
		t, err := time.ParseInLocation("2006-01-02T15:04:05", ev.DatetimeLocal, loc)
		if err != nil {
			continue
		}
		e := Event{Title: ev.Title, URL: ev.URL, Start: t, AllDay: ev.TimeTBD, Source: "seatgeek", Category: ev.Type}
		e.Location = ev.Venue.Name
		if ev.Venue.City != "" {
			e.Location += ", " + ev.Venue.City
		}
		out = append(out, e)
	}
	return out, nil
}

func fetchWithRetry(ctx context.Context, u string) ([]byte, string, error) {
	var lastErr error
	for attempt, wait := 0, time.Second; attempt < 3; attempt, wait = attempt+1, wait*3 {
		body, ctype, err := httpx.GetBytes(ctx, u, map[string]string{"Accept": "text/calendar, application/rss+xml, application/xml, text/html;q=0.8, */*;q=0.5"})
		if err == nil {
			return body, ctype, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	return nil, "", lastErr
}

func looksLikeICS(b []byte) bool {
	head := bytes.TrimPrefix(bytes.TrimSpace(b[:min(len(b), 64)]), []byte("\xef\xbb\xbf"))
	return bytes.HasPrefix(head, []byte("BEGIN:VCALENDAR"))
}

var icsLinkRe = regexp.MustCompile(`(?i)(?:href|src)=["']([^"']+\.ics(?:\?[^"']*)?|[^"']*(?:ical|icalendar|feed=calendar)[^"']*)["']`)

// discoverICSLink finds a calendar link inside an HTML page.
func discoverICSLink(html []byte, base string) string {
	m := icsLinkRe.FindSubmatch(html)
	if m == nil {
		return ""
	}
	link := string(m[1])
	if strings.HasPrefix(link, "http") {
		return link
	}
	if strings.HasPrefix(link, "//") {
		return "https:" + link
	}
	if strings.HasPrefix(link, "/") {
		if i := strings.Index(base[8:], "/"); i > 0 {
			return base[:8+i] + link
		}
		return strings.TrimRight(base, "/") + link
	}
	return strings.TrimRight(base, "/") + "/" + link
}

// ── parsing ──────────────────────────────────────────────────────────────────

// ParseICS extracts events between from and to, expanding simple RRULEs.
func ParseICS(data []byte, loc *time.Location, from, to time.Time) ([]Event, error) {
	lines := unfold(data)
	var out []Event
	var cur map[string][]icsProp
	inEvent := false
	calName := ""
	for _, ln := range lines {
		switch {
		case ln == "BEGIN:VEVENT":
			inEvent = true
			cur = map[string][]icsProp{}
		case ln == "END:VEVENT":
			inEvent = false
			out = append(out, expandEvent(cur, loc, from, to, calName)...)
		case inEvent:
			name, p := parseProp(ln)
			if name != "" {
				cur[name] = append(cur[name], p)
			}
		case strings.HasPrefix(ln, "X-WR-CALNAME:"):
			calName = unescapeICS(strings.TrimPrefix(ln, "X-WR-CALNAME:"))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

type icsProp struct {
	params map[string]string
	value  string
}

func unfold(data []byte) []string {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		ln := strings.TrimRight(sc.Text(), "\r")
		if (strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += ln[1:]
			continue
		}
		lines = append(lines, ln)
	}
	return lines
}

func parseProp(ln string) (string, icsProp) {
	i := strings.Index(ln, ":")
	if i < 0 {
		return "", icsProp{}
	}
	head, value := ln[:i], ln[i+1:]
	parts := strings.Split(head, ";")
	name := strings.ToUpper(parts[0])
	p := icsProp{params: map[string]string{}, value: value}
	for _, kv := range parts[1:] {
		if k, v, ok := strings.Cut(kv, "="); ok {
			p.params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return name, p
}

func first(m map[string][]icsProp, key string) (icsProp, bool) {
	if ps, ok := m[key]; ok && len(ps) > 0 {
		return ps[0], true
	}
	return icsProp{}, false
}

func parseICSTime(p icsProp, loc *time.Location) (time.Time, bool, bool) {
	v := strings.TrimSpace(p.value)
	if p.params["VALUE"] == "DATE" || len(v) == 8 {
		t, err := time.ParseInLocation("20060102", v, loc)
		return t, true, err == nil
	}
	l := loc
	if tz := p.params["TZID"]; tz != "" {
		if tl, err := time.LoadLocation(tz); err == nil {
			l = tl
		} else if tl, err := time.LoadLocation(windowsToIANA(tz)); err == nil {
			l = tl
		}
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t.In(loc), false, err == nil
	}
	t, err := time.ParseInLocation("20060102T150405", v, l)
	return t.In(loc), false, err == nil
}

func windowsToIANA(tz string) string {
	m := map[string]string{
		"Central Standard Time": "America/Chicago", "Eastern Standard Time": "America/New_York",
		"Mountain Standard Time": "America/Denver", "Pacific Standard Time": "America/Los_Angeles",
		"GMT Standard Time": "Europe/London", "W. Europe Standard Time": "Europe/Berlin",
	}
	if v, ok := m[tz]; ok {
		return v
	}
	return tz
}

func unescapeICS(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)
	return strings.TrimSpace(r.Replace(s))
}

func expandEvent(m map[string][]icsProp, loc *time.Location, from, to time.Time, calName string) []Event {
	startP, ok := first(m, "DTSTART")
	if !ok {
		return nil
	}
	start, allDay, ok := parseICSTime(startP, loc)
	if !ok {
		return nil
	}
	var dur time.Duration
	if endP, ok := first(m, "DTEND"); ok {
		if end, _, ok := parseICSTime(endP, loc); ok {
			dur = end.Sub(start)
		}
	} else if dP, ok := first(m, "DURATION"); ok {
		dur = parseICSDuration(dP.value)
	}
	base := Event{AllDay: allDay, Source: calName}
	if p, ok := first(m, "SUMMARY"); ok {
		base.Title = unescapeICS(p.value)
	}
	if p, ok := first(m, "LOCATION"); ok {
		base.Location = unescapeICS(p.value)
	}
	if p, ok := first(m, "URL"); ok {
		base.URL = strings.TrimSpace(p.value)
	}
	if p, ok := first(m, "CATEGORIES"); ok {
		base.Category = unescapeICS(p.value)
	}
	if base.Title == "" {
		return nil
	}
	// Cancelled events are skipped.
	if p, ok := first(m, "STATUS"); ok && strings.EqualFold(strings.TrimSpace(p.value), "CANCELLED") {
		return nil
	}
	exdates := map[string]bool{}
	for _, p := range m["EXDATE"] {
		for _, v := range strings.Split(p.value, ",") {
			if t, _, ok := parseICSTime(icsProp{params: p.params, value: v}, loc); ok {
				exdates[t.Format("20060102T1504")] = true
			}
		}
	}
	emit := func(t time.Time) *Event {
		if exdates[t.Format("20060102T1504")] {
			return nil
		}
		end := t.Add(dur)
		// Include events that are in progress or upcoming within the window.
		if end.Before(from) && !t.After(from) || t.After(to) {
			if end.Before(from) || t.After(to) {
				return nil
			}
		}
		e := base
		e.Start = t
		e.End = end
		return &e
	}
	rr, hasRule := first(m, "RRULE")
	if !hasRule {
		if e := emit(start); e != nil {
			return []Event{*e}
		}
		return nil
	}
	freq := ""
	for _, kv := range strings.Split(rr.value, ";") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "FREQ") {
			freq = strings.ToLower(v)
		}
	}
	instances := expandRRule(rr.value, start, emit, to)
	for i := range instances {
		instances[i].Repeats = freq
	}
	return instances
}

func parseICSDuration(s string) time.Duration {
	// P1D, PT2H30M, P1W
	var d time.Duration
	num := ""
	inTime := false
	for _, c := range strings.ToUpper(s) {
		switch {
		case c >= '0' && c <= '9':
			num += string(c)
		case c == 'T':
			inTime = true
		case c == 'W' || c == 'D' || c == 'H' || c == 'M' || c == 'S':
			n, _ := strconv.Atoi(num)
			num = ""
			switch c {
			case 'W':
				d += time.Duration(n) * 7 * 24 * time.Hour
			case 'D':
				d += time.Duration(n) * 24 * time.Hour
			case 'H':
				d += time.Duration(n) * time.Hour
			case 'M':
				if inTime {
					d += time.Duration(n) * time.Minute
				}
			case 'S':
				d += time.Duration(n) * time.Second
			}
		}
	}
	return d
}

var weekdayCodes = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

// expandRRule handles FREQ=DAILY/WEEKLY/MONTHLY/YEARLY with INTERVAL, COUNT,
// UNTIL and BYDAY (weekly). Anything fancier falls back to the first instance.
func expandRRule(rule string, start time.Time, emit func(time.Time) *Event, to time.Time) []Event {
	parts := map[string]string{}
	for _, kv := range strings.Split(rule, ";") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parts[strings.ToUpper(k)] = strings.ToUpper(v)
		}
	}
	interval := 1
	if n, err := strconv.Atoi(parts["INTERVAL"]); err == nil && n > 0 {
		interval = n
	}
	count := -1
	if n, err := strconv.Atoi(parts["COUNT"]); err == nil {
		count = n
	}
	until := to
	if u := parts["UNTIL"]; u != "" {
		if t, _, ok := parseICSTime(icsProp{params: map[string]string{}, value: u}, start.Location()); ok && t.Before(until) {
			until = t
		}
	}
	var byday []time.Weekday
	if bd := parts["BYDAY"]; bd != "" && parts["FREQ"] == "WEEKLY" {
		for _, code := range strings.Split(bd, ",") {
			code = strings.TrimLeft(code, "+-0123456789")
			if wd, ok := weekdayCodes[code]; ok {
				byday = append(byday, wd)
			}
		}
	}
	var out []Event
	produced := 0
	add := func(t time.Time) bool {
		if count >= 0 && produced >= count {
			return false
		}
		if t.After(until) {
			return false
		}
		produced++
		if e := emit(t); e != nil {
			out = append(out, *e)
		}
		return true
	}
	switch parts["FREQ"] {
	case "DAILY":
		for t, i := start, 0; i < 2000 && add(t); i++ {
			t = t.AddDate(0, 0, interval)
		}
	case "WEEKLY":
		if len(byday) == 0 {
			byday = []time.Weekday{start.Weekday()}
		}
		weekStart := start.AddDate(0, 0, -int(start.Weekday()))
		for w := 0; w < 520; w++ {
			base := weekStart.AddDate(0, 0, w*interval*7)
			stop := false
			for d := 0; d < 7; d++ {
				t := base.AddDate(0, 0, d)
				if t.Before(start) {
					continue
				}
				hit := false
				for _, wd := range byday {
					if t.Weekday() == wd {
						hit = true
					}
				}
				if hit && !add(t) {
					stop = true
					break
				}
			}
			if stop || base.After(until) {
				break
			}
		}
	case "MONTHLY":
		for t, i := start, 0; i < 240 && add(t); i++ {
			t = start.AddDate(0, (i+1)*interval, 0)
		}
	case "YEARLY":
		for t, i := start, 0; i < 50 && add(t); i++ {
			t = start.AddDate((i+1)*interval, 0, 0)
		}
	default:
		add(start)
	}
	return out
}

// ── RSS with dates in the text ───────────────────────────────────────────────

var (
	rssItemRe  = regexp.MustCompile(`(?is)<item>(.*?)</item>`)
	rssTagRe   = regexp.MustCompile(`(?is)<(title|link|description|pubDate|dc:date|content:encoded)[^>]*>(.*?)</`)
	cdataRe    = regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)\]\]>`)
	htmlTagRe  = regexp.MustCompile(`<[^>]+>`)
	monthNames = "January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec"
	longDateRe = regexp.MustCompile(`(?i)\b(` + monthNames + `)\.?\s+(\d{1,2})(?:st|nd|rd|th)?(?:,?\s+(\d{4}))?`)
	numDateRe  = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/(\d{2,4}))?\b`)
	numRangeRe = regexp.MustCompile(`(?i)\b(\d{1,2})/(\d{1,2})\s*(?:-|–|—|to|through|thru)\s*(?:[a-z]+\.?,?\s*)?(\d{1,2})/(\d{1,2})\b`)
	timeRe     = regexp.MustCompile(`(?i)\b(\d{1,2})(?::(\d{2}))?\s*(am|pm|a\.m\.|p\.m\.)`)
)

// FetchRSS reads an RSS/Atom feed and keeps items whose title or description
// mentions a date inside the window. Event pages often only put the date in
// prose, so this is best-effort by design. WordPress feeds only return the
// ten newest posts, so up to `pages` older pages (?paged=N) are read too,
// stopping as soon as a page fails or yields nothing.
func FetchRSS(ctx context.Context, rawURL string, loc *time.Location, from, to time.Time, pages int) ([]Event, error) {
	base := strings.TrimSpace(rawURL)
	if pages < 1 {
		pages = 1
	}
	var out []Event
	var firstErr error
	followed := 0
	seenItems := map[string]bool{}
	for page := 1; page <= pages; page++ {
		u := base
		if page > 1 {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			u += sep + "paged=" + strconv.Itoa(page)
		}
		body, _, err := fetchWithRetry(ctx, u)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		text := string(body)
		if !strings.Contains(text, "<item") && !strings.Contains(text, "<entry") {
			if page == 1 {
				return nil, fmt.Errorf("%s is not an RSS or Atom feed", rawURL)
			}
			break
		}
		text = strings.ReplaceAll(strings.ReplaceAll(text, "<entry>", "<item>"), "</entry>", "</item>")
		items := rssItemRe.FindAllStringSubmatch(text, -1)
		if len(items) == 0 {
			break
		}
		newItems := 0
		for _, im := range items {
			// Some feeds ignore ?paged= and repeat the same posts; count only
			// items not seen on an earlier page and stop when a page adds none.
			itemKey := im[1]
			if m := rssTagRe.FindStringSubmatch(im[1]); m != nil {
				itemKey = cleanRSS(m[2])
			}
			if seenItems[itemKey] {
				continue
			}
			seenItems[itemKey] = true
			newItems++
			fields := map[string]string{}
			for _, tm := range rssTagRe.FindAllStringSubmatch(im[1], -1) {
				name := strings.ToLower(tm[1])
				fields[name] = cleanRSS(tm[2])
				if name == "content:encoded" {
					fields["content:encoded:raw"] = unwrapCDATA(tm[2])
				}
			}
			title := fields["title"]
			if title == "" {
				continue
			}
			// Roundup posts ("22 weekend events: …") list one event per heading
			// in the article body; harvest those individually. The body comes
			// from the feed when it carries full content, otherwise the
			// article itself is fetched (a few per feed, to stay quick).
			if looksLikeRoundup(title) {
				raw := fields["content:encoded:raw"]
				if raw == "" && followed < 6 && fields["link"] != "" {
					followed++
					raw = fetchArticle(ctx, fields["link"])
				}
				if raw != "" {
					if subs := ExtractHeadingDateEvents([]byte(raw), fields["link"], loc, from, to); len(subs) >= 2 {
						out = append(out, subs...)
						continue
					}
				}
				// A roundup headline is an article, not an event; if it could
				// not be split into its events, leave it out entirely.
				continue
			}
			// Otherwise the post itself is the event: date in the title, then
			// the summary, then the article text.
			start, end, ok := findDateSpan(title, from, to, loc)
			if !ok {
				start, end, ok = findDateSpan(fields["description"], from, to, loc)
			}
			if !ok {
				start, end, ok = findDateSpan(fields["content:encoded"], from, to, loc)
			}
			if !ok {
				continue
			}
			ev := Event{Title: cleanEventTitle(title), Start: start, End: end, URL: fields["link"], Source: "rss"}
			ev.AllDay = start.Hour() == 0 && start.Minute() == 0
			out = append(out, ev)
		}
		if newItems == 0 {
			break // the feed is repeating itself
		}
		if page == 1 && !strings.Contains(strings.ToLower(text), "wordpress") && !strings.Contains(text, "wp-") {
			break // only WordPress feeds paginate this way
		}
	}
	_ = firstErr
	return out, nil
}

var (
	articleMu    sync.Mutex
	articleCache = map[string]articleEntry{}
)

type articleEntry struct {
	body string
	at   time.Time
}

// fetchArticle downloads a linked article once per ten minutes, so the same
// roundup reached through two feeds costs one request and one rate-limit slot.
func fetchArticle(ctx context.Context, link string) string {
	link = strings.SplitN(link, "#", 2)[0]
	articleMu.Lock()
	if e, ok := articleCache[link]; ok && time.Since(e.at) < 10*time.Minute {
		articleMu.Unlock()
		return e.body
	}
	articleMu.Unlock()
	body := ""
	if page, _, err := getBytesUA(ctx, link); err == nil {
		body = string(page)
	}
	articleMu.Lock()
	articleCache[link] = articleEntry{body: body, at: time.Now()}
	if len(articleCache) > 200 {
		for k, e := range articleCache {
			if time.Since(e.at) > 10*time.Minute {
				delete(articleCache, k)
			}
		}
	}
	articleMu.Unlock()
	return body
}

// looksLikeRoundup spots "12 things to do this weekend"-style posts.
func looksLikeRoundup(title string) bool {
	l := strings.ToLower(title)
	if strings.Contains(l, "things to do") || strings.Contains(l, "+ more") || strings.Contains(l, "and more") {
		return true
	}
	hasNumber := strings.IndexFunc(l, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0
	return hasNumber && (strings.Contains(l, "events") || strings.Contains(l, "weekend") || strings.Contains(l, "festivals") || strings.Contains(l, "ways to"))
}

// cleanEventTitle trims the article-style padding news sites add to event
// headlines ("5 things to do this weekend, including…", trailing "— Sept. 26").
func cleanEventTitle(t string) string {
	t = strings.TrimSpace(t)
	for _, sep := range []string{" — ", " – ", " | "} {
		if i := strings.LastIndex(t, sep); i > 20 {
			tail := t[i+len(sep):]
			if longDateRe.MatchString(tail) || numDateRe.MatchString(tail) {
				t = strings.TrimSpace(t[:i])
			}
		}
	}
	return t
}

func unwrapCDATA(s string) string {
	if m := cdataRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return html.UnescapeString(s)
}

func cleanRSS(s string) string {
	if m := cdataRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(html.UnescapeString(s))
	return strings.Join(strings.Fields(s), " ")
}

// findDateSpan is findDate plus range awareness: "Sept. 17–20" or "Oct 3 to
// Nov 1" yields a start and an inclusive end, and an event that started a few
// days ago but is still running counts as upcoming.
func findDateSpan(text string, from, to time.Time, loc *time.Location) (start, end time.Time, ok bool) {
	// Numeric ranges: "9/18–9/19", "9/18 – Sat. 9/19".
	if m := numRangeRe.FindStringSubmatch(text); m != nil {
		m1, _ := strconv.Atoi(m[1])
		d1, _ := strconv.Atoi(m[2])
		m2, _ := strconv.Atoi(m[3])
		d2, _ := strconv.Atoi(m[4])
		if m1 >= 1 && m1 <= 12 && m2 >= 1 && m2 <= 12 {
			s := time.Date(from.Year(), time.Month(m1), d1, 0, 0, 0, 0, loc)
			e := time.Date(from.Year(), time.Month(m2), d2, 23, 59, 0, 0, loc)
			if e.Before(s) {
				e = e.AddDate(1, 0, 0)
			}
			if e.Before(from.AddDate(0, 0, -1)) {
				s, e = s.AddDate(1, 0, 0), e.AddDate(1, 0, 0)
			}
			if !e.Before(from) && !s.After(to) {
				return s, e, true
			}
		}
	}
	if m := dateRangeRe.FindStringSubmatch(text); m != nil {
		y, _ := strconv.Atoi(m[5])
		if y == 0 {
			y = from.Year()
		}
		sm := monthFromName(m[1])
		em := sm
		if m[3] != "" {
			em = monthFromName(m[3])
		}
		d1, _ := strconv.Atoi(m[2])
		d2, _ := strconv.Atoi(m[4])
		s := time.Date(y, sm, d1, 0, 0, 0, 0, loc)
		e := time.Date(y, em, d2, 23, 59, 0, 0, loc)
		if e.Before(s) {
			e = e.AddDate(1, 0, 0)
		}
		if e.Before(from.AddDate(0, 0, -1)) && m[5] == "" {
			s, e = s.AddDate(1, 0, 0), e.AddDate(1, 0, 0)
		}
		if !e.Before(from) && !s.After(to) {
			return s, e, true
		}
	}
	t, ok := findDate(text, from, to, loc)
	return t, time.Time{}, ok
}

// findDate returns the first plausible future date mentioned in text.
func findDate(text string, from, to time.Time, loc *time.Location) (time.Time, bool) {
	year := from.Year()
	try := func(month time.Month, day int, y int) (time.Time, bool) {
		if day < 1 || day > 31 {
			return time.Time{}, false
		}
		if y == 0 {
			y = year
		}
		t := time.Date(y, month, day, 0, 0, 0, 0, loc)
		if t.Before(from.AddDate(0, 0, -1)) && y == year {
			t = t.AddDate(1, 0, 0) // "Jan 5" mentioned in December means next year
		}
		if t.Before(from.AddDate(0, 0, -1)) || t.After(to) {
			return time.Time{}, false
		}
		if tm := timeRe.FindStringSubmatch(text); tm != nil {
			h, _ := strconv.Atoi(tm[1])
			mnt, _ := strconv.Atoi(tm[2])
			pm := strings.HasPrefix(strings.ToLower(tm[3]), "p")
			if pm && h < 12 {
				h += 12
			}
			if !pm && h == 12 {
				h = 0
			}
			t = t.Add(time.Duration(h)*time.Hour + time.Duration(mnt)*time.Minute)
		}
		return t, true
	}
	for _, m := range longDateRe.FindAllStringSubmatch(text, -1) {
		mon := monthFromName(m[1])
		day, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if t, ok := try(mon, day, y); ok {
			return t, true
		}
	}
	for _, m := range numDateRe.FindAllStringSubmatch(text, -1) {
		mo, _ := strconv.Atoi(m[1])
		day, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if y > 0 && y < 100 {
			y += 2000
		}
		if mo >= 1 && mo <= 12 {
			if t, ok := try(time.Month(mo), day, y); ok {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func monthFromName(s string) time.Month {
	s = strings.ToLower(s[:3])
	for m := time.January; m <= time.December; m++ {
		if strings.ToLower(m.String()[:3]) == s {
			return m
		}
	}
	return time.January
}

// ── Ticketmaster Discovery ───────────────────────────────────────────────────

// FetchTicketmaster lists events near a point. A free API key is required.
func FetchTicketmaster(ctx context.Context, apiKey string, lat, lng, radiusMiles float64, loc *time.Location, from, to time.Time) ([]Event, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Ticketmaster API key is not set")
	}
	u := fmt.Sprintf("https://app.ticketmaster.com/discovery/v2/events.json?apikey=%s&latlong=%.4f,%.4f&radius=%d&unit=miles&sort=date,asc&size=50&startDateTime=%s&endDateTime=%s",
		apiKey, lat, lng, int(radiusMiles), from.UTC().Format("2006-01-02T15:04:05Z"), to.UTC().Format("2006-01-02T15:04:05Z"))
	var res struct {
		Embedded struct {
			Events []struct {
				Name  string `json:"name"`
				URL   string `json:"url"`
				Dates struct {
					Start struct {
						DateTime  string `json:"dateTime"`
						LocalDate string `json:"localDate"`
						LocalTime string `json:"localTime"`
					} `json:"start"`
				} `json:"dates"`
				Classifications []struct {
					Segment struct {
						Name string `json:"name"`
					} `json:"segment"`
				} `json:"classifications"`
				Embedded struct {
					Venues []struct {
						Name string `json:"name"`
						City struct {
							Name string `json:"name"`
						} `json:"city"`
					} `json:"venues"`
				} `json:"_embedded"`
			} `json:"events"`
		} `json:"_embedded"`
	}
	if err := httpx.GetJSON(ctx, u, nil, &res); err != nil {
		return nil, fmt.Errorf("ticketmaster: %w", err)
	}
	var out []Event
	for _, ev := range res.Embedded.Events {
		e := Event{Title: ev.Name, URL: ev.URL, Source: "ticketmaster"}
		if t, err := time.Parse(time.RFC3339, ev.Dates.Start.DateTime); err == nil {
			e.Start = t.In(loc)
		} else if t, err := time.ParseInLocation("2006-01-02 15:04:05", ev.Dates.Start.LocalDate+" "+ev.Dates.Start.LocalTime, loc); err == nil {
			e.Start = t
		} else if t, err := time.ParseInLocation("2006-01-02", ev.Dates.Start.LocalDate, loc); err == nil {
			e.Start, e.AllDay = t, true
		} else {
			continue
		}
		if len(ev.Embedded.Venues) > 0 {
			v := ev.Embedded.Venues[0]
			e.Location = v.Name
			if v.City.Name != "" {
				e.Location += ", " + v.City.Name
			}
		}
		if len(ev.Classifications) > 0 {
			e.Category = ev.Classifications[0].Segment.Name
		}
		out = append(out, e)
	}
	return out, nil
}

// ReadAllLimited is a helper for tests.
func ReadAllLimited(r io.Reader) ([]byte, error) { return io.ReadAll(io.LimitReader(r, 8<<20)) }

var _ = http.StatusOK
