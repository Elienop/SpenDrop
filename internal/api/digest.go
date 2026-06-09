package api

import (
	"strconv"
	"strings"
	"time"
)

// parseHHMM parses a "HH:MM" 24-hour string. ok is false for empty/malformed
// input or out-of-range values, so callers treat a bad value as "no boundary".
func parseHHMM(v string) (hour, minute int, ok bool) {
	hStr, mStr, found := strings.Cut(v, ":")
	if !found {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(hStr)
	m, err2 := strconv.Atoi(mStr)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// inQuietHours reports whether `now`, evaluated in IANA zone `tz`, falls inside
// the [start,end) window. start/end are "HH:MM"; an empty or malformed bound (or
// a zero-length window) means "never quiet". A window whose end is <= start wraps
// across midnight (e.g. 22:00->07:00). An unknown tz falls back to UTC.
func inQuietHours(now time.Time, start, end, tz string) bool {
	sh, sm, ok1 := parseHHMM(start)
	eh, em, ok2 := parseHHMM(end)
	if !ok1 || !ok2 {
		return false
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	n := now.In(loc)
	cur := n.Hour()*60 + n.Minute()
	s := sh*60 + sm
	e := eh*60 + em
	if s == e {
		return false // zero-length window
	}
	if s < e {
		return cur >= s && cur < e // same-day window
	}
	return cur >= s || cur < e // wrap-around past midnight
}
