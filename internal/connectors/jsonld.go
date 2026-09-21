package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"breaklist/internal/httpx"
)

// browserUA makes listing sites serve the same HTML a browser gets; several
// of them return a stub or a 403 to unknown user agents.
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36"

var ldJSONRe = regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)

// FetchPageEvents extracts schema.org Event objects from the JSON-LD blocks a
// web page embeds. Eventbrite, AllEvents, Meetup, Bandsintown and most venue
// and city sites publish their listings this way.
func FetchPageEvents(ctx context.Context, rawURL string, loc *time.Location, from, to time.Time) ([]Event, error) {
	u := strings.TrimSpace(rawURL)
	var body []byte
	var err error
	for attempt, wait := 0, time.Second; attempt < 3; attempt, wait = attempt+1, wait*3 {
		body, _, err = getBytesUA(ctx, u)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	events := ExtractJSONLDEvents(body, loc)
	if len(events) == 0 {
		// No structured data: read headings paired with <time datetime> tags
		// (Squarespace and many venue sites), then headings followed by a
		// date in prose (news roundups), and failing that treat the page as
		// a single event (a festival or fair's own site).
		events = ExtractTimeTagEvents(body, u, loc)
	}
	if len(events) == 0 {
		if subs := ExtractHeadingDateEvents(body, u, loc, from, to); len(subs) >= 2 {
			events = subs
		}
	}
	if len(events) == 0 {
		if e, ok := pageAsEvent(body, u, loc, from, to); ok {
			return []Event{e}, nil
		}
		return nil, fmt.Errorf("%s has no schema.org event data and no readable date", u)
	}
	var out []Event
	for _, e := range events {
		if e.Start.After(to) || (e.End.IsZero() && e.Start.Before(from)) || (!e.End.IsZero() && e.End.Before(from)) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ExtractJSONLDEvents parses every ld+json block in an HTML document.
func ExtractJSONLDEvents(page []byte, loc *time.Location) []Event {
	var out []Event
	seen := map[string]bool{}
	for _, m := range ldJSONRe.FindAllSubmatch(page, -1) {
		raw := strings.TrimSpace(string(m[1]))
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			// Some sites leave HTML comments or trailing commas; try a light repair.
			raw = strings.TrimPrefix(strings.TrimSuffix(raw, "-->"), "<!--")
			if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &v); err != nil {
				continue
			}
		}
		walkLD(v, loc, func(e Event) {
			k := strings.ToLower(e.Title) + "|" + e.Start.Format("2006-01-02T15:04")
			if !seen[k] {
				seen[k] = true
				out = append(out, e)
			}
		}, 0)
	}
	return out
}

func walkLD(v any, loc *time.Location, emit func(Event), depth int) {
	if depth > 12 {
		return
	}
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			walkLD(x, loc, emit, depth+1)
		}
	case map[string]any:
		if isEventType(t["@type"]) {
			if e, ok := eventFromLD(t, loc); ok {
				emit(e)
			}
		}
		// Descend into graphs, lists and nested items.
		for _, key := range []string{"@graph", "itemListElement", "item", "subEvent", "events", "mainEntity", "hasPart"} {
			if child, ok := t[key]; ok {
				walkLD(child, loc, emit, depth+1)
			}
		}
	}
}

func isEventType(v any) bool {
	switch t := v.(type) {
	case string:
		return t == "Event" || strings.HasSuffix(t, "Event")
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok && (s == "Event" || strings.HasSuffix(s, "Event")) {
				return true
			}
		}
	}
	return false
}

func ldString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(html.UnescapeString(t))
	case map[string]any:
		for _, k := range []string{"name", "@value", "text"} {
			if s, ok := t[k].(string); ok {
				return strings.TrimSpace(html.UnescapeString(s))
			}
		}
	case []any:
		if len(t) > 0 {
			return ldString(t[0])
		}
	}
	return ""
}

func eventFromLD(m map[string]any, loc *time.Location) (Event, bool) {
	name := ldString(m["name"])
	if name == "" {
		return Event{}, false
	}
	if st, ok := m["eventStatus"].(string); ok && strings.Contains(strings.ToLower(st), "cancelled") {
		return Event{}, false
	}
	start, allDay, ok := parseLDTime(ldString(m["startDate"]), loc)
	if !ok {
		return Event{}, false
	}
	e := Event{Title: name, Start: start, AllDay: allDay, Source: "web"}
	if end, _, ok := parseLDTime(ldString(m["endDate"]), loc); ok && end.After(start) {
		e.End = end
	}
	if u, ok := m["url"].(string); ok {
		e.URL = u
	}
	e.Location = ldLocation(m["location"])
	if s, ok := m["@type"].(string); ok && s != "Event" {
		e.Category = strings.TrimSuffix(s, "Event")
	}
	return e, true
}

func ldLocation(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(html.UnescapeString(t))
	case []any:
		if len(t) > 0 {
			return ldLocation(t[0])
		}
	case map[string]any:
		name := ldString(t["name"])
		city := ""
		switch a := t["address"].(type) {
		case string:
			city = strings.TrimSpace(a)
		case map[string]any:
			city = ldString(a["addressLocality"])
			if name == "" {
				name = ldString(a["streetAddress"])
			}
		}
		switch {
		case name != "" && city != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(city)):
			return name + ", " + city
		case name != "":
			return name
		default:
			return city
		}
	}
	return ""
}

// parseLDTime accepts the ISO 8601 shapes seen in the wild.
func parseLDTime(s string, loc *time.Location) (time.Time, bool, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, false
	}
	if len(s) == 10 {
		t, err := time.ParseInLocation("2006-01-02", s, loc)
		return t, true, err == nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.In(loc), false, true
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, false, true
		}
	}
	return time.Time{}, false, false
}

var (
	headingOrTimeRe = regexp.MustCompile(`(?is)<(h[1-6])[^>]*>(.*?)</h[1-6]>|<time[^>]*\bdatetime=["']([^"']+)["'][^>]*>(.*?)</time>`)
	clockRe         = regexp.MustCompile(`(?i)^\s*(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*$`)
	hrefRe          = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["']`)
)

// ExtractTimeTagEvents reads pages that list events as a heading followed by
// <time datetime="…"> tags (date, start, end). Squarespace's event list and
// many hand-built venue pages look like this.
func ExtractTimeTagEvents(page []byte, base string, loc *time.Location) []Event {
	src := scriptRe.ReplaceAll(page, []byte(" "))
	var out []Event
	var cur *Event
	var curHeading string
	flush := func() {
		if cur != nil && cur.Title != "" && !cur.Start.IsZero() {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, m := range headingOrTimeRe.FindAllSubmatch(src, -1) {
		if len(m[1]) > 0 { // heading
			flush()
			text := strings.Join(strings.Fields(html.UnescapeString(string(htmlTagRe.ReplaceAll(m[2], []byte(" "))))), " ")
			curHeading = text
			ev := Event{Title: text, Source: "web"}
			if h := hrefRe.FindSubmatch(m[2]); h != nil {
				ev.URL = absoluteURL(string(h[1]), base)
			}
			cur = &ev
			continue
		}
		if cur == nil || curHeading == "" {
			continue
		}
		attr := strings.TrimSpace(string(m[3]))
		text := strings.TrimSpace(html.UnescapeString(string(htmlTagRe.ReplaceAll(m[4], []byte(" ")))))
		t, allDay, ok := parseLDTime(attr, loc)
		if !ok {
			continue
		}
		if cm := clockRe.FindStringSubmatch(text); cm != nil {
			// A clock time refines the day already known from the date tag.
			h, _ := strconv.Atoi(cm[1])
			mnt, _ := strconv.Atoi(cm[2])
			if strings.EqualFold(cm[3], "pm") && h < 12 {
				h += 12
			}
			if strings.EqualFold(cm[3], "am") && h == 12 {
				h = 0
			}
			day := t
			if !cur.Start.IsZero() && cur.End.IsZero() && !cur.AllDay {
				day = cur.Start
			} else if !cur.Start.IsZero() && cur.AllDay {
				day = cur.Start
			}
			stamp := time.Date(day.Year(), day.Month(), day.Day(), h, mnt, 0, 0, loc)
			switch {
			case cur.Start.IsZero() || cur.AllDay:
				cur.Start, cur.AllDay = stamp, false
			case cur.End.IsZero():
				if stamp.Before(cur.Start) {
					stamp = stamp.AddDate(0, 0, 1)
				}
				cur.End = stamp
			}
			continue
		}
		switch {
		case cur.Start.IsZero():
			cur.Start, cur.AllDay = t, allDay
		case cur.End.IsZero() && t.After(cur.Start):
			cur.End = t
			if allDay {
				cur.End = t.Add(24*time.Hour - time.Minute) // inclusive end date
			}
		}
	}
	flush()
	// Only trust the pattern when it produced a handful of dated headings;
	// a lone <time> on a blog post is not an event listing.
	if len(out) < 2 {
		return nil
	}
	return out
}

var (
	headingRe    = regexp.MustCompile(`(?is)<(h[2-4])[^>]*>(.*?)</h[2-4]>`)
	listNumberRe = regexp.MustCompile(`^\s*\d{1,3}[.)]\s+`)
)

// nonDateWords strips weekday and month names, numbers, ordinals and range
// glue ("to", "through", "&") from a heading and returns whatever wording is
// left, so "Fri. 9/18–Sat. 9/19" becomes "" and "Oktoberfest at Pepper Place"
// stays intact.
func nonDateWords(title string) string {
	isGlue := map[string]bool{"to": true, "through": true, "thru": true, "and": true, "am": true, "pm": true, "at": true, "from": true, "until": true, "til": true}
	dayNames := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday", "mon", "tue", "tues", "wed", "thu", "thur", "thurs", "fri", "sat", "sun"}
	var kept []string
	for _, tok := range strings.FieldsFunc(title, func(r rune) bool {
		return r == ' ' || r == '	' || r == '/' || r == '.' || r == ',' || r == ':' || r == '&' || r == '+' || r == '-' || r == '–' || r == '—' || r == '|' || r == '(' || r == ')'
	}) {
		l := strings.ToLower(tok)
		if isGlue[l] {
			continue
		}
		digits := strings.TrimRight(l, "stndrh") // 18th, 3rd, 21st, 22nd
		if digits != "" && strings.Trim(digits, "0123456789") == "" {
			continue
		}
		skip := false
		for _, d := range dayNames {
			if l == d {
				skip = true
				break
			}
		}
		if !skip {
			for m := time.January; m <= time.December; m++ {
				name := strings.ToLower(m.String())
				if l == name || l == name[:3] || (l == "sept" && m == time.September) {
					skip = true
					break
				}
			}
		}
		if !skip {
			kept = append(kept, tok)
		}
	}
	return strings.Join(kept, " ")
}

// genericHeading spots section labels that are not events themselves.
func genericHeading(rest string) bool {
	for _, w := range strings.Fields(strings.ToLower(rest)) {
		switch w {
		case "events", "event", "ongoing", "weekend", "this", "birmingham", "things", "do", "in", "the", "week", "upcoming", "more", "other", "also", "happening", "around", "town":
		default:
			return false
		}
	}
	return true
}

func isTrailerHeading(lower string) bool {
	for _, p := range []string{"you might also like", "related", "more stories", "more from", "read more", "recommended", "trending", "latest news", "comments", "leave a reply"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// ExtractHeadingDateEvents reads article-style pages and feed bodies where
// each event is a heading followed by a paragraph that names its date
// ("Fall Plant Sale" … "Saturday, Sept. 20, 9AM–2PM"). Roundup posts on local
// news sites are written exactly this way.
func ExtractHeadingDateEvents(body []byte, base string, loc *time.Location, from, to time.Time) []Event {
	src := scriptRe.ReplaceAll(body, []byte(" "))
	var out []Event
	var contextStart, contextEnd time.Time // from day-divider headings like "Sat. 9/19"
	idx := headingRe.FindAllSubmatchIndex(src, -1)
	for i, loc0 := range idx {
		headingHTML := src[loc0[4]:loc0[5]]
		blockEnd := len(src)
		if i+1 < len(idx) {
			blockEnd = idx[i+1][0]
		}
		blockHTML := src[loc0[1]:blockEnd]
		title := strings.Join(strings.Fields(html.UnescapeString(string(htmlTagRe.ReplaceAll(headingHTML, []byte(" "))))), " ")
		title = listNumberRe.ReplaceAllString(title, "")
		if len(title) < 4 || len(title) > 120 {
			continue
		}
		lower := strings.ToLower(title)
		if isTrailerHeading(lower) {
			break // "You might also like", "Related stories": the article is over
		}
		if strings.Contains(lower, "newsletter") || strings.Contains(lower, "subscribe") || strings.Contains(lower, "home listings") {
			continue
		}
		// A heading that is nothing but a date ("Fri. 9/18–Sat. 9/19") is a day
		// divider: remember it for events beneath it that omit their date.
		// Generic section headings ("Ongoing events") are skipped too.
		rest := nonDateWords(title)
		if rest == "" || genericHeading(rest) {
			if s, e, ok := findDateSpan(title, from.AddDate(0, 0, -7), to, loc); ok && rest == "" {
				contextStart, contextEnd = s, e
			} else {
				contextStart, contextEnd = time.Time{}, time.Time{}
			}
			continue
		}
		block := strings.Join(strings.Fields(html.UnescapeString(string(htmlTagRe.ReplaceAll(blockHTML, []byte(" "))))), " ")
		if len(block) > 900 {
			block = block[:900] // the date is always near the top of the blurb
		}
		start, end, ok := findDateSpan(title+" "+block, from, to, loc)
		if !ok && !contextStart.IsZero() && !contextStart.After(to) {
			// The divider names a day; anything on that day still counts today.
			dayStart := from.AddDate(0, 0, -1)
			if (contextEnd.IsZero() && !contextStart.Before(dayStart)) || (!contextEnd.IsZero() && !contextEnd.Before(dayStart)) {
				start, end, ok = contextStart, contextEnd, true
			}
		}
		if !ok {
			continue
		}
		// A dated blurb under a multi-day divider ("Fri. 9/18–Sat. 9/19")
		// usually runs the whole span; carry the divider's end over.
		if end.IsZero() && !contextEnd.IsZero() && contextEnd.After(start) && !contextStart.After(start) {
			end = contextEnd
		}
		ev := Event{Title: cleanEventTitle(title), Start: start, End: end, Source: "web", URL: base}
		ev.AllDay = start.Hour() == 0 && start.Minute() == 0
		if h := hrefRe.FindSubmatch(headingHTML); h != nil {
			ev.URL = absoluteURL(string(h[1]), base)
		} else if h := hrefRe.FindSubmatch(blockHTML); h != nil {
			ev.URL = absoluteURL(string(h[1]), base)
		}
		out = append(out, ev)
	}
	return out
}

// LongRunningTitle reports whether a title itself advertises a span of more
// than three weeks ("Dino Safari - March 4 to November 1").
func LongRunningTitle(title string) bool {
	m := dateRangeRe.FindStringSubmatch(title)
	if m == nil || m[3] == "" {
		return false // same-month ranges are short by definition
	}
	start, end := monthFromName(m[1]), monthFromName(m[3])
	d1, _ := strconv.Atoi(m[2])
	d2, _ := strconv.Atoi(m[4])
	a := time.Date(2001, start, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2001, end, d2, 0, 0, 0, 0, time.UTC)
	if b.Before(a) {
		b = b.AddDate(1, 0, 0)
	}
	return b.Sub(a) > 21*24*time.Hour
}

func absoluteURL(link, base string) string {
	switch {
	case strings.HasPrefix(link, "http"):
		return link
	case strings.HasPrefix(link, "//"):
		return "https:" + link
	case strings.HasPrefix(link, "/"):
		if i := strings.Index(base[8:], "/"); i > 0 {
			return base[:8+i] + link
		}
		return strings.TrimRight(base, "/") + link
	}
	return link
}

var (
	metaRe      = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)=["']([^"']+)["'][^>]+content=["']([^"']*)["']`)
	titleTagRe  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	scriptRe    = regexp.MustCompile(`(?is)<(script|style|noscript|svg)[^>]*>.*?</(script|style|noscript|svg)>`)
	dateRangeRe = regexp.MustCompile(`(?i)(?:(?:mon|tue|tues|wed|thu|thur|thurs|fri|sat|sun)[a-z]*\.?,?\s*)?(` + monthNames + `)\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s*(?:-|–|—|to|through|thru)\s*(?:(?:mon|tue|tues|wed|thu|thur|thurs|fri|sat|sun)[a-z]*\.?,?\s*)?(?:(` + monthNames + `)\.?\s+)?(\d{1,2})(?:st|nd|rd|th)?(?:,?\s+(\d{4}))?`)
)

// pageAsEvent turns a festival or fair's own web page into one event, using
// Open Graph or Facebook-event meta tags when present and otherwise the
// page title plus the first date (or date range) in the visible text.
func pageAsEvent(page []byte, u string, loc *time.Location, from, to time.Time) (Event, bool) {
	meta := map[string]string{}
	for _, m := range metaRe.FindAllSubmatch(page, -1) {
		meta[strings.ToLower(string(m[1]))] = html.UnescapeString(string(m[2]))
	}
	title := strings.TrimSpace(meta["og:title"])
	if title == "" {
		if m := titleTagRe.FindSubmatch(page); m != nil {
			title = strings.TrimSpace(html.UnescapeString(string(m[1])))
		}
	}
	// "Alabama State Fair | Home" -> "Alabama State Fair"
	for _, sep := range []string{" | ", " – ", " — ", " - "} {
		if i := strings.Index(title, sep); i > 3 {
			title = title[:i]
		}
	}
	if title == "" {
		return Event{}, false
	}
	e := Event{Title: title, URL: u, Source: "web", Category: "site"}
	if st := meta["event:start_time"]; st != "" {
		if t, allDay, ok := parseLDTime(st, loc); ok && !t.After(to) {
			e.Start, e.AllDay = t, allDay
			if et, _, ok := parseLDTime(meta["event:end_time"], loc); ok {
				e.End = et
			}
			return e, !e.End.Before(from) || e.End.IsZero() && !e.Start.Before(from)
		}
	}
	text := scriptRe.ReplaceAll(page, []byte(" "))
	text = htmlTagRe.ReplaceAll(text, []byte(" "))
	plain := strings.Join(strings.Fields(html.UnescapeString(string(text))), " ")
	// A range like "October 3 – 13" or "Oct 3 to Nov 1" wins over a single date.
	if m := dateRangeRe.FindStringSubmatch(plain); m != nil {
		y, _ := strconv.Atoi(m[5])
		if y == 0 {
			y = from.Year()
		}
		startMon := monthFromName(m[1])
		endMon := startMon
		if m[3] != "" {
			endMon = monthFromName(m[3])
		}
		d1, _ := strconv.Atoi(m[2])
		d2, _ := strconv.Atoi(m[4])
		start := time.Date(y, startMon, d1, 0, 0, 0, 0, loc)
		end := time.Date(y, endMon, d2, 23, 59, 0, 0, loc)
		if end.Before(start) {
			end = end.AddDate(1, 0, 0)
		}
		if start.Before(from.AddDate(0, 0, -60)) && y == from.Year() {
			start, end = start.AddDate(1, 0, 0), end.AddDate(1, 0, 0)
		}
		if !end.Before(from) && !start.After(to) {
			e.Start, e.End, e.AllDay = start, end, true
			return e, true
		}
	}
	if t, ok := findDate(plain, from, to, loc); ok {
		e.Start, e.AllDay = t, t.Hour() == 0
		return e, true
	}
	return Event{}, false
}

func getBytesUA(ctx context.Context, u string) ([]byte, string, error) {
	return httpx.GetBytesHeaders(ctx, u, map[string]string{
		"User-Agent":      browserUA,
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "en-US,en;q=0.9",
	})
}
