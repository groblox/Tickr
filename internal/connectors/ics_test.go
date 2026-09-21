package connectors

import (
	"testing"
	"time"
)

const sampleICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nX-WR-CALNAME:Library\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;TZID=America/Chicago:20260920T100000\r\nDTEND;TZID=America/Chicago:20260920T110000\r\n" +
	"SUMMARY:Toddler Story \r\n Time\r\nLOCATION:Children's Room\\, Hoover Library\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20260925\r\nDTEND;VALUE=DATE:20260926\r\nSUMMARY:Book Sale\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;TZID=America/Chicago:20260901T170000\r\nDURATION:PT1H\r\nRRULE:FREQ=WEEKLY;BYDAY=TU,TH;UNTIL=20261231T000000Z\r\n" +
	"EXDATE;TZID=America/Chicago:20260922T170000\r\nSUMMARY:Lego Club\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART:20260919T140000Z\r\nSUMMARY:Cancelled thing\r\nSTATUS:CANCELLED\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20200101\r\nSUMMARY:Long ago\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseICS(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	from := time.Date(2026, 9, 18, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, 14)
	evs, err := ParseICS([]byte(sampleICS), loc, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, e := range evs {
		titles = append(titles, e.Start.Format("01-02 15:04")+" "+e.Title)
	}
	// Lego Club: Tue/Thu from Sep 1 => within window: Sep 22 (excluded), 24, 29, Oct 1
	want := []string{"09-20 10:00 Toddler Story Time", "09-24 17:00 Lego Club", "09-25 00:00 Book Sale", "09-29 17:00 Lego Club", "10-01 17:00 Lego Club"}
	if len(titles) != len(want) {
		t.Fatalf("got %v want %v", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("got %v want %v", titles, want)
		}
	}
	if evs[0].Location != "Children's Room, Hoover Library" || evs[0].Source != "Library" || evs[2].AllDay != true {
		t.Fatalf("fields wrong: %+v", evs[0])
	}
}

func TestFindDateAndHTMLDiscovery(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 9, 18, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, 30)
	if d, ok := findDate("Fall Festival on Saturday, October 3rd at 6:30 pm", from, to, loc); !ok || d.Month() != 10 || d.Day() != 3 || d.Hour() != 18 || d.Minute() != 30 {
		t.Fatalf("long date parse: %v %v", d, ok)
	}
	if d, ok := findDate("Farmers market 9/26 8am-noon", from, to, loc); !ok || d.Day() != 26 || d.Hour() != 8 {
		t.Fatalf("numeric date parse: %v %v", d, ok)
	}
	if _, ok := findDate("Remembering September 1, 2010", from, to, loc); ok {
		t.Fatal("past dates should not match")
	}
	if _, ok := findDate("No date here", from, to, loc); ok {
		t.Fatal("no date should not match")
	}
	html := []byte(`<html><a href="/calendar/export.ics?x=1">iCal</a></html>`)
	if got := discoverICSLink(html, "https://example.org/events"); got != "https://example.org/calendar/export.ics?x=1" {
		t.Fatalf("discovery: %s", got)
	}
	if parseICSDuration("P1DT2H30M") != 26*time.Hour+30*time.Minute {
		t.Fatal("duration parse wrong")
	}
}
