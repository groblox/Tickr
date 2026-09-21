package modules

import (
	"testing"
	"time"
)

func TestCalendarHelpers(t *testing.T) {
	loc := time.UTC
	if d := nthWeekday(2026, time.November, time.Thursday, 4, loc); d.Day() != 26 {
		t.Fatalf("Thanksgiving 2026 should be Nov 26, got %v", d)
	}
	if d := nthWeekday(2026, time.May, time.Monday, -1, loc); d.Day() != 25 {
		t.Fatalf("Memorial Day 2026 should be May 25, got %v", d)
	}
	if d := nthWeekday(2026, time.March, time.Sunday, 2, loc); d.Day() != 8 {
		t.Fatalf("DST start 2026 should be Mar 8, got %v", d)
	}
	if e := easter(2026, loc); e.Month() != time.April || e.Day() != 5 {
		t.Fatalf("Easter 2026 should be Apr 5, got %v", e)
	}
	if e := easter(2024, loc); e.Month() != time.March || e.Day() != 31 {
		t.Fatalf("Easter 2024 should be Mar 31, got %v", e)
	}
	obs := usObservances(2026, loc)
	var found bool
	for _, o := range obs {
		if o.label == "Mother's Day" && o.when.Month() == time.May && o.when.Day() == 10 {
			found = true
		}
	}
	if !found {
		t.Fatal("Mother's Day 2026 should be May 10")
	}
}
