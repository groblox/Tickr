package connectors

import (
	"testing"
	"time"
)

func TestExtractTimeTagEvents(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	page := []byte(`<html><body><script>var x = "<h1>not me</h1>";</script>
<article class="eventlist-event"><h1 class="eventlist-title"><a href="/events/heritage" class="eventlist-title-link">Southbound Heritage Dinner at Pepper Place</a></h1>
<ul class="eventlist-meta"><li><time class="event-date" datetime="2026-09-18">Friday, September 18, 2026</time></li>
<li><time class="event-time-localized-start" datetime="2026-09-18">6:00 PM</time> – <time class="event-time-localized-end" datetime="2026-09-18">10:00 PM</time></li></ul></article>
<article class="eventlist-event"><h1 class="eventlist-title"><a href="https://pepperplace.com/events/okt">Oktoberfest — See You September 20!</a></h1>
<time class="event-date" datetime="2026-09-20">Sunday, September 20, 2026</time><time datetime="2026-09-20">1:00 PM</time><time datetime="2026-09-20">5:00 PM</time></article>
<article><h2>Story Hour</h2><time datetime="2026-09-21">Mon, Sep 21, 2026</time><time datetime="2026-09-21">5:00 PM</time><time datetime="2026-09-25">Fri, Sep 25, 2026</time></article>
</body></html>`)
	evs := ExtractTimeTagEvents(page, "https://pepperplace.com/events/", loc)
	if len(evs) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(evs), evs)
	}
	e := evs[0]
	if e.Title != "Southbound Heritage Dinner at Pepper Place" || e.URL != "https://pepperplace.com/events/heritage" || e.Start.Hour() != 18 || e.End.Hour() != 22 || e.Start.Day() != 18 || e.AllDay {
		t.Fatalf("event 1 wrong: %+v", e)
	}
	if evs[1].Start.Day() != 20 || evs[1].Start.Hour() != 13 || evs[1].End.Hour() != 17 {
		t.Fatalf("event 2 wrong: %+v", evs[1])
	}
	if evs[2].Title != "Story Hour" || evs[2].Start.Day() != 21 || evs[2].Start.Hour() != 17 || evs[2].End.Day() != 25 {
		t.Fatalf("event 3 wrong: %+v", evs[2])
	}
	if ExtractTimeTagEvents([]byte(`<h1>Blog post</h1><time datetime="2026-09-18">today</time>`), "https://x", loc) != nil {
		t.Fatal("a single dated heading must not count as a listing")
	}
}

func TestExtractHeadingDateEvents(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	from := time.Date(2026, 9, 18, 9, 0, 0, 0, loc)
	to := from.AddDate(0, 0, 30)
	page := []byte(`<article>
<h2>Birmingham events 9/17–9/20</h2><p>intro</p>
<h2>Fri. 9/18–Sat. 9/19</h2>
<h3>10. Friends of Birmingham Botanical Gardens Fall Plant Sale</h3><p>Stock up on garden essentials. <b>Where:</b> Birmingham Botanical Gardens</p>
<h3>11. Live at McWane presents Skyview</h3><p>This weekend, Sept. 18 + 19, 2026 at the IMAX dome.</p>
<h2>Sun. 9/20</h2>
<h3>17. Oktoberfest at Pepper Place</h3><p>Pepper Place will host its 4th annual Oktoberfest this Sunday, September 20, 2026 <b>When:</b> 1PM–5PM</p>
<h2>Ongoing events</h2>
<h3>21. Southbound Food Festival</h3><p>Celebrate the food scene. <b>When:</b> Sept. 18–27</p>
<h3>22. Alabama State Fair</h3><p><b>When:</b> Fri, Sept. 18–Sun, Sept. 27 | 4PM</p>
<h3>23. Fall Fest</h3><p><b>When:</b> Saturday, Oct. 3 – Sunday, Oct. 4, 10AM–5PM</p>
<h2>You might also like</h2>
<h3>21 Birmingham home listings (Sept. 18-20)</h3><p>September 18, 2026</p>
</article>`)
	evs := ExtractHeadingDateEvents(page, "https://bhamnow.com/x", loc, from, to)
	got := map[string]Event{}
	for _, e := range evs {
		got[e.Title] = e
	}
	if len(evs) != 6 {
		t.Fatalf("expected 6 events, got %d: %v", len(evs), keys(got))
	}
	if e := got["Fall Fest"]; e.Start.Month() != 10 || e.Start.Day() != 3 || e.End.Day() != 4 {
		t.Fatalf("weekday-in-range parse wrong: %+v", e)
	}
	if e := got["Friends of Birmingham Botanical Gardens Fall Plant Sale"]; e.Start.Day() != 18 || e.End.Day() != 19 {
		t.Fatalf("plant sale should inherit the Fri–Sat divider: %+v", e)
	}
	if e := got["Oktoberfest at Pepper Place"]; e.Start.Day() != 20 || e.Start.Hour() != 13 {
		t.Fatalf("oktoberfest wrong: %+v", e)
	}
	if e := got["Southbound Food Festival"]; e.Start.Day() != 18 || e.End.Day() != 27 {
		t.Fatalf("southbound range wrong: %+v", e)
	}
	if e := got["Alabama State Fair"]; e.Start.Day() != 18 || e.End.Day() != 27 {
		t.Fatalf("state fair range wrong: %+v", e)
	}
	if _, bad := got["21 Birmingham home listings (Sept. 18-20)"]; bad {
		t.Fatal("trailer section should not be parsed")
	}
}

func keys(m map[string]Event) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPageAsEvent(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 9, 18, 0, 0, 0, 0, loc)
	page := []byte(`<html><head><title>Alabama State Fair | September 18-27, 2026 | Birmingham Race Course</title></head><body><p>Come see us!</p></body></html>`)
	e, ok := pageAsEvent(page, "https://alabamastatefair.com/", loc, from, from.AddDate(0, 0, 30))
	if !ok || e.Title != "Alabama State Fair" || e.Start.Day() != 18 || e.End.Day() != 27 || !e.AllDay {
		t.Fatalf("state fair parse: %v %+v", ok, e)
	}
	fb := []byte(`<html><head><meta property="og:title" content="Fall Plant Sale"><meta property="event:start_time" content="2026-10-03T09:00:00-05:00"></head></html>`)
	e, ok = pageAsEvent(fb, "https://x", loc, from, from.AddDate(0, 0, 30))
	if !ok || e.Title != "Fall Plant Sale" || e.Start.UTC().Hour() != 14 {
		t.Fatalf("fb meta parse: %v %+v", ok, e)
	}
}

func TestExtractJSONLDEvents(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	page := []byte(`<html><head>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"ItemList","itemListElement":[
 {"@type":"ListItem","position":1,"item":{"@type":"Event","name":"Oktoberfest at the Park &amp; Fest","startDate":"2026-09-19T12:00:00-05:00","endDate":"2026-09-19T20:00:00-05:00","url":"https://x/1","location":{"@type":"Place","name":"Avondale Park","address":{"@type":"PostalAddress","addressLocality":"Birmingham"}}}},
 {"@type":"ListItem","position":2,"item":{"@type":"MusicEvent","name":"Cancelled show","startDate":"2026-09-19T19:00","eventStatus":"https://schema.org/EventCancelled"}},
 {"@type":"ListItem","position":3,"item":{"@type":"FestivalEvent","name":"Fall Fest","startDate":"2026-10-03","location":"Railroad Park"}}
]}</script>
<script type="application/ld+json">[{"@type":"Event","name":"Oktoberfest at the Park & Fest","startDate":"2026-09-19T12:00:00-05:00"},{"@type":"Organization","name":"nope"}]</script>
</head></html>`)
	evs := ExtractJSONLDEvents(page, loc)
	if len(evs) != 2 {
		t.Fatalf("expected 2 unique events (cancelled dropped, duplicate merged), got %d: %+v", len(evs), evs)
	}
	e := evs[0]
	if e.Title != "Oktoberfest at the Park & Fest" || e.Location != "Avondale Park, Birmingham" || e.Start.Hour() != 12 || e.End.Hour() != 20 || e.Category != "" {
		t.Fatalf("event 1 wrong: %+v", e)
	}
	if evs[1].Title != "Fall Fest" || !evs[1].AllDay || evs[1].Location != "Railroad Park" || evs[1].Category != "Festival" {
		t.Fatalf("event 2 wrong: %+v", evs[1])
	}
}
