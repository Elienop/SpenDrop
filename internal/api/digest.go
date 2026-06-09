package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
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

// validQuietBound reports whether a quiet-hours boundary string is acceptable:
// an empty string means "no boundary", otherwise it must be a valid 24h HH:MM.
func validQuietBound(v string) bool {
	if v == "" {
		return true
	}
	_, _, ok := parseHHMM(v)
	return ok
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

// RunDigestTick is the restart-safe daily digest pass, invoked by a ticker. It
// reads the household settings, and when shouldSendDigest is true sends ONE
// rollup built by QUERYING how many live transactions were ADDED (by created_at)
// since the stored last_digest_at (CountTransactionsSince — soft-delete-safe),
// then advances the
// cursor. Best-effort: every failure is logged, never returned. Count-only body,
// so no money crosses the wire (no DTO concern).
func (h *Handler) RunDigestTick(ctx context.Context) {
	if h.dispatcher() == nil {
		return // push disabled
	}
	s, err := h.queries.GetNotificationSettings(ctx)
	if err != nil {
		log.Printf("digest: read settings: %v", err)
		return
	}
	now := h.clock.Now()
	if !shouldSendDigest(now, s) {
		return
	}
	since := now.Add(-24 * time.Hour)
	if s.LastDigestAt.Valid {
		since = s.LastDigestAt.Time
	}
	n, err := h.queries.CountTransactionsSince(ctx, since)
	if err != nil {
		log.Printf("digest: count since %v: %v", since, err)
		return
	}
	if n > 0 {
		noun := "transactions"
		if n == 1 {
			noun = "transaction"
		}
		payload := pushAlertPayload{
			Title: "Your SpenDrop summary",
			Body:  fmt.Sprintf("%d %s since your last summary.", n, noun),
			URL:   "/transactions",
			Type:  "digest",
			Tag:   "activity",
		}
		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("digest: marshal payload: %v", err)
			return
		}
		h.fanOutPush(ctx, "digest", body, 0, pushOpts{
			Tag: "activity", Topic: "act", Urgency: push.UrgencyLow,
		})
	}
	if err := h.queries.SetLastDigestAt(ctx, now); err != nil {
		log.Printf("digest: set last_digest_at: %v", err)
	}
}
