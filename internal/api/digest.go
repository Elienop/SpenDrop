package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/database"
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

// atHHMMOn returns the instant of `hhmm` on the calendar day of `day` in `loc`.
func atHHMMOn(day time.Time, hhmm string, loc *time.Location) (time.Time, bool) {
	h, m, ok := parseHHMM(hhmm)
	if !ok {
		return time.Time{}, false
	}
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc), true
}

// shouldSendDigest reports whether the household should send its daily digest at
// `now`. True iff digest is on AND `now` (in quiet_tz) has passed today's
// quiet_end boundary AND the last digest predates that boundary — so it fires
// exactly once per day, anchored to the moment quiet hours end. quiet_end is the
// required anchor; without it there is no daily boundary and we never fire.
func shouldSendDigest(now time.Time, s database.NotificationSettings) bool {
	if s.DigestMode == "off" {
		return false
	}
	loc, err := time.LoadLocation(s.QuietTz)
	if err != nil {
		loc = time.UTC
	}
	nowLocal := now.In(loc)
	boundary, ok := atHHMMOn(nowLocal, s.QuietEnd, loc)
	if !ok {
		return false // no quiet_end -> no daily anchor
	}
	if nowLocal.Before(boundary) {
		return false // today's boundary not reached yet
	}
	if s.LastDigestAt.Valid && !s.LastDigestAt.Time.In(loc).Before(boundary) {
		return false // already digested at/after today's boundary
	}
	return true
}
