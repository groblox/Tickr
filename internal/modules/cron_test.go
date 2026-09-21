package modules

import (
	"testing"
	"time"
)

func TestMatchesCronPart(t *testing.T) {
	tests := []struct {
		value  int
		part   string
		expect bool
	}{
		{2, "*", true}, {2, "*/2", true}, {2, "*/3", false}, {6, "*/3", true},
		{1, "1,2,3", true}, {4, "1,2,3", false}, {12, "*/5,10", false}, {15, "*/5,10", true},
		{8, "7,8,19", true}, {3, "1-5", true}, {6, "1-5", false}, {6, "1-5,6", true},
	}
	for _, tc := range tests {
		if got := matchesCronPart(tc.value, tc.part); got != tc.expect {
			t.Errorf("value %d part %q: got %v want %v", tc.value, tc.part, got, tc.expect)
		}
	}
}

func TestMatchCron(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		expr   string
		date   time.Time
		expect bool
	}{
		{"*/2,3,5,17 1,2,8 */2,5", d(2023, time.August, 17), true},
		{"*/2,3,5,17 1,2,8 */2,5", d(2023, time.August, 18), true},
		{"*/2,3,5,17 1,2,8 */2,5", d(2023, time.March, 18), false},
		{"*/2,3,5,17 1,2,8 */2,5", d(2023, time.August, 16), false},
		{"1 * *", d(2023, time.August, 1), true},
		{"1 * *", d(2023, time.August, 2), false},
		{"* * 3", d(2023, time.August, 16), true}, // Wednesday
		{"* * 4", d(2023, time.August, 16), false},
		{"* * 1-5", d(2023, time.August, 16), true},
		{"* * 1-5", d(2023, time.August, 19), false}, // Saturday
		{"25 12", d(2023, time.December, 25), true},  // missing DOW = wildcard
		{"", d(2023, time.December, 25), true},
	}
	for _, tc := range tests {
		if got := MatchCron(tc.date, tc.expr); got != tc.expect {
			t.Errorf("expr %q date %v: got %v want %v", tc.expr, tc.date.Format("2006-01-02"), got, tc.expect)
		}
	}
}
