package modules

import (
	"strconv"
	"strings"
	"time"
)

// matchesCronPart checks one field of a "DOM MONTH DOW" reminder expression.
// Supported syntax: "*", "5", "1,15", "*/2", "1-5" and combinations thereof.
func matchesCronPart(value int, part string) bool {
	part = strings.TrimSpace(part)
	if part == "*" || part == "" {
		return true
	}
	for _, v := range strings.Split(part, ",") {
		v = strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(v, "*/"):
			n, err := strconv.Atoi(strings.TrimPrefix(v, "*/"))
			if err == nil && n > 0 && value%n == 0 {
				return true
			}
		case strings.Contains(v, "-"):
			lo, hi, ok := strings.Cut(v, "-")
			a, errA := strconv.Atoi(strings.TrimSpace(lo))
			b, errB := strconv.Atoi(strings.TrimSpace(hi))
			if ok && errA == nil && errB == nil && value >= a && value <= b {
				return true
			}
		default:
			n, err := strconv.Atoi(v)
			if err == nil && n == value {
				return true
			}
		}
	}
	return false
}

// MatchCron reports whether date matches a "DOM MONTH DOW" expression
// (day of month, month number, weekday with 0=Sunday). Missing trailing
// fields are treated as wildcards.
func MatchCron(date time.Time, expr string) bool {
	parts := strings.Fields(expr)
	for len(parts) < 3 {
		parts = append(parts, "*")
	}
	return matchesCronPart(date.Day(), parts[0]) &&
		matchesCronPart(int(date.Month()), parts[1]) &&
		matchesCronPart(int(date.Weekday()), parts[2])
}
