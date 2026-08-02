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
// digest_time boundary AND the last digest predates that boundary — so it fires
// exactly once per day, anchored to the user-chosen digest_time. The digest owns
// its own schedule, decoupled from quiet hours; quiet_tz only provides the local
// clock the digest_time is read in. digest_time carries a NOT NULL default so it
// is normally present; a malformed value yields no anchor and we never fire.
func shouldSendDigest(now time.Time, s database.NotificationSettings) bool {
	if s.DigestMode == "off" {
		return false
	}
	loc, err := time.LoadLocation(s.QuietTz)
	if err != nil {
		loc = time.UTC
	}
	nowLocal := now.In(loc)
	boundary, ok := atHHMMOn(nowLocal, s.DigestTime, loc)
	if !ok {
		return false // no/invalid digest_time -> no daily anchor
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
	// fanOutGated is the right zero value for "n == 0": no rollup was owed this
	// pass, so there is nothing to lose by advancing the cursor.
	outcome := fanOutGated
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
			Tag:   "digest",
		}
		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("digest: marshal payload: %v", err)
			return
		}
		outcome = h.fanOutPush(ctx, "digest", body, 0, pushOpts{
			Tag: "digest", Topic: "digest", Urgency: push.UrgencyLow,
		})
	}

	// The cursor means "the household has been told about everything up to
	// `now`", so it must only move when that is true. Delivery is asynchronous,
	// and fanOutPush returns as soon as it has ACCEPTED the rollup — so the two
	// non-accepting outcomes have to be told apart:
	//
	//   fanOutQueued — handed to the transport. Advance. Per-device delivery is
	//     best-effort from here, exactly as it was when the loop ran inline, and
	//     re-sending the whole day because one phone was unreachable would be
	//     worse than the miss.
	//   fanOutGated  — nothing was owed. n == 0, or the household switched the
	//     digest off between this pass's settings read and fanOutPush's. Advance:
	//     no rollup was lost, and refusing to advance would make every tick from
	//     here on repeat the count query forever.
	//   fanOutDropped — a rollup WAS owed and was refused before reaching the
	//     transport (the in-flight cap, or a shutdown drain). Nothing sent it and
	//     nothing else will retry it. Advancing here would push the window past a
	//     digest that never existed and shouldSendDigest would go false for the
	//     rest of the day: the digest is silently gone until tomorrow. Leaving
	//     the cursor makes the next tick, one minute later, rebuild the same
	//     window and try again.
	if outcome == fanOutDropped {
		log.Printf("digest: delivery refused, holding last_digest_at so the next tick retries")
		return
	}

	// Advance the cursor on a context INDEPENDENT of the caller's. The caller
	// bounds `ctx` to digestPerRunTimeout and cancels it at shutdown; if `ctx`
	// goes Done anywhere above, database/sql returns DeadlineExceeded BEFORE
	// running the UPDATE — last_digest_at would never advance and the digest
	// would re-fire every tick until recovery. WithoutCancel keeps the
	// deadline/cancel off this write while preserving any request-scoped values
	// on the context.
	if err := h.queries.SetLastDigestAt(context.WithoutCancel(ctx), now); err != nil {
		log.Printf("digest: set last_digest_at: %v", err)
	}
}
