package main

import (
	"strconv"
	"strings"
	"time"
)

// matchesCronPart checks if a given value matches a cron part.
// Parameters:
//   - value: The value to match against the cron part.
//   - cronPart: The cron part (DOM, M, or DOW) to match against.
//
// Returns true if the value matches any part of the cronPart, otherwise false.
func matchesCronPart(value int, cronPart string) bool {
	if cronPart == "*" {
		return true // Wildcard always matches
	}

	values := strings.Split(cronPart, ",")
	for _, v := range values {
		if strings.HasPrefix(v, "*/") {
			intervalStr := strings.TrimPrefix(v, "*/")
			interval, err := strconv.Atoi(intervalStr)
			if err != nil {
				return false
			}
			if value%interval == 0 {
				return true // Value matches an interval
			}
		} else {
			expectedValue, err := strconv.Atoi(v)
			if err != nil {
				return false
			}
			if value == expectedValue {
				return true // Value matches an expected specific value
			}
		}
	}

	return false // No match found
}

// matchCronExpression checks if a given date matches a cron expression.
// The cron expression is in the format "DOM M DOW".
// Parameters:
//   - date: The date to check against the cron expression.
//   - cronExpression: The cron expression to match against.
//
// Returns true if the date matches the cron expression, otherwise false.
func matchCronExpression(date time.Time, cronExpression string) bool {
	parts := strings.Split(cronExpression, " ")

	// Check Day of Month (DOM)
	if !matchesCronPart(date.Day(), parts[0]) {
		return false
	}

	// Check Month (M)
	if !matchesCronPart(int(date.Month()), parts[1]) {
		return false
	}

	// Check Day of Week (DOW)
	if !matchesCronPart(int(date.Weekday()), parts[2]) {
		return false
	}

	return true
}
