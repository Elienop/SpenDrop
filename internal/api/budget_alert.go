package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
)

// pushDispatcher is the narrow seam evaluateBudgetAlerts and the household
// fan-out use to reach the push transport. *push.Sender satisfies it in
// production; budget_alert_test.go injects a recording fake. The Handler
// field pushSender (*push.Sender) is the production wire; pushTesterForBudgetAlerts
// is a test-only override consulted first so unit tests never need a real
// VAPID keypair or HTTP round-trip.
type pushDispatcher interface {
	Send(ctx context.Context, sub push.Subscription, payload []byte, opts push.Options) (prune bool, err error)
}

// pushOpts bundles the per-send transport + SW-collapse knobs threaded from the
// emit site through fanOutPush. Tag is copied into the payload by the caller
// BEFORE marshal; Topic/Urgency are forwarded to push.Options. fanOutPush itself
// reads only Topic/Urgency — Tag is documentation of what the caller baked in.
type pushOpts struct {
	Tag     string
	Topic   string
	Urgency push.Urgency
}

// pushOptionsFor maps a notification type to its base SW collapse tag, Web Push
// Topic, and delivery Urgency. PURE — no receiver, no I/O — so it is unit-tested
// directly. over_budget returns the BASE ("budget"/"ob"); per-cell sends override
// via budgetCellTag/budgetCellTopic or budgetSummaryTag/budgetSummaryTopic.
func pushOptionsFor(notifType string) (tag, topic string, urgency push.Urgency) {
	switch notifType {
	case "over_budget":
		return "budget", "ob", push.UrgencyNormal
	case "txn_added", "txn_edited", "txn_deleted", "large_txn":
		return "activity", "act", push.UrgencyLow
	default:
		return "", "", push.UrgencyLow // fail-safe low
	}
}

// budgetCellTag / budgetCellTopic format a single freshly-crossed over-budget cell's
// SW collapse tag and Web Push Topic. PURE. The Topic stays <=32 url-safe chars:
// "ob-" + up to a 19-digit int64 + "-" + 6-digit YYYYMM = 29 max.
func budgetCellTag(catID int64, year, month int) string {
	return fmt.Sprintf("budget-%d-%04d%02d", catID, year, month)
}

func budgetCellTopic(catID int64, year, month int) string {
	return fmt.Sprintf("ob-%d-%04d%02d", catID, year, month)
}

// budgetSummaryTag / budgetSummaryTopic collapse the multi-cell over-budget summary
// push (>=2 categories crossed in one request) into a single notification row.
const (
	budgetSummaryTag   = "budget-summary"
	budgetSummaryTopic = "ob-summary"
)

// budgetCell identifies one (category, calendar-month) over-budget evaluation
// unit. Year/Month are derived from the AFFECTED transaction's own date parsed
// UTC (Task 17), never from the wall clock, so a back-dated edit re-evaluates
// the month it actually lands in.
type budgetCell struct {
	CategoryID int64
	Year       int
	Month      int
}

// pushAlertPayload is the JSON body delivered to the service worker's 'push'
// handler. Title/Body are the human-readable strings the SW renders directly
// (it reads data.title / data.body); without them an over-budget push shows a
// blank "SpenDrop" notification. URL is the deep-link the notificationclick
// handler opens. The structured fields (category, dollars) are kept for any
// future client-side use. Every money figure is DOLLARS via centsToDollars —
// never a raw *_cents value (Money Wire-Edge DTO discipline).
type pushAlertPayload struct {
	Title        string  `json:"title"`
	Body         string  `json:"body"`
	URL          string  `json:"url"`
	Type         string  `json:"type"`          // "budget_over"
	Tag          string  `json:"tag,omitempty"` // SW collapse key, copied from pushOpts.Tag
	CategoryID   int64   `json:"category_id"`
	CategoryName string  `json:"category_name"`
	Year         int     `json:"year"`
	Month        int     `json:"month"`
	LimitDollars float64 `json:"limit_dollars"`
	SpentDollars float64 `json:"spent_dollars"`
}

// dispatcher resolves the test override first, then the production sender.
// Returns nil when the feature is disabled (no sender wired) so the caller
// can cheaply skip all fan-out work.
func (h *Handler) dispatcher() pushDispatcher {
	if h.pushTesterForBudgetAlerts != nil {
		return h.pushTesterForBudgetAlerts
	}
	if h.pushSender == nil {
		return nil
	}
	return h.pushSender
}

// crossedCell is one freshly-crossed (category, month) cell captured during the
// latch loop so the actual push fan-out can happen ONCE after the loop, not
// per-iteration. Dollars are pre-converted (Money Wire-Edge DTO discipline).
type crossedCell struct {
	cell         budgetCell
	name         string
	spentDollars float64
	limitDollars float64
}

// sendOverBudgetCell fans out one push for a single freshly-crossed cell,
// collapsing on the per-cell SW tag budget-<categoryID>-<YYYYMM> and the
// matching Web Push Topic ob-<categoryID>-<YYYYMM>. Urgency comes from the
// over_budget mapping (Normal).
func (h *Handler) sendOverBudgetCell(ctx context.Context, c crossedCell) {
	tag := budgetCellTag(c.cell.CategoryID, c.cell.Year, c.cell.Month)
	topic := budgetCellTopic(c.cell.CategoryID, c.cell.Year, c.cell.Month)
	payload := pushAlertPayload{
		Title: fmt.Sprintf("Over budget: %s", c.name),
		Body: fmt.Sprintf("%s is over budget — $%.2f spent of your $%.2f limit this month.",
			c.name, c.spentDollars, c.limitDollars),
		URL:          "/budgets",
		Type:         "budget_over",
		Tag:          tag,
		CategoryID:   c.cell.CategoryID,
		CategoryName: c.name,
		Year:         c.cell.Year,
		Month:        c.cell.Month,
		LimitDollars: c.limitDollars,
		SpentDollars: c.spentDollars,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("budget alert: marshal cell payload cat=%d: %v", c.cell.CategoryID, err)
		return
	}
	_, _, urgency := pushOptionsFor("over_budget")
	h.fanOutPush(ctx, "over_budget", body, 0, pushOpts{Tag: tag, Topic: topic, Urgency: urgency})
}

// evaluateBudgetAlerts is the post-commit over-budget hook. It mirrors the
// error contract of verifyAffectedCheckpoints exactly: the caller's mutation
// is already committed, so THIS FUNCTION NEVER RETURNS AN ERROR and never
// blocks the request — every failure is logged and the loop continues.
//
// Latch semantics (budget_alert_state, migration 014): a row present for
// (category, year, month) means "currently in an alerted-over state". On a
// fresh cross we INSERT ... ON CONFLICT DO NOTHING; rows-affected==1 means we
// were the one to set the latch, so we fan out exactly one push. A second
// evaluation while still over finds the row already present (rows-affected==0)
// and stays silent — that is the dedup. When spend drops back under we DELETE
// the latch row, which re-arms the cell so a later re-cross sends AGAIN.
func (h *Handler) evaluateBudgetAlerts(ctx context.Context, cells []budgetCell) {
	if len(cells) == 0 {
		return
	}
	// Quiet-hours/bypass gate (mirrors fanOutPush): if an over_budget send would
	// be suppressed by quiet hours RIGHT NOW (window active AND the household
	// disabled the bypass), do NOT write the dedup latch for a fresh cross. The
	// latch is otherwise committed BEFORE fanOutPush drops the suppressed send,
	// so later evals see rows-affected==0 and the crossing is permanently lost
	// instead of deferred. Skipping the latch leaves the cell un-latched so the
	// next post-quiet evaluation re-fires it. Only the transient quiet-hours
	// suppression is consulted here — the over_budget TOGGLE being off keeps its
	// deliberate latch-anyway behavior (see notifTypeEnabled).
	suppressOverBudget := false
	if settings, err := h.queries.GetNotificationSettings(ctx); err != nil {
		log.Printf("budget alert: read notification settings: %v", err)
	} else {
		suppressOverBudget = inQuietHours(h.clock.Now(), settings.QuietStart, settings.QuietEnd, settings.QuietTz) &&
			!settings.QuietAllowOverBudget
	}
	var crossed []crossedCell
	for _, cell := range cells {
		over, limitCents, spentCents, catName, err := h.cellOverBudget(ctx, cell)
		if err != nil {
			log.Printf("budget alert: evaluate cell cat=%d %04d-%02d: %v",
				cell.CategoryID, cell.Year, cell.Month, err)
			continue
		}

		if !over {
			// Resolve the latch (idempotent: 0 rows when not currently latched).
			if _, err := h.queries.ClearBudgetAlertState(ctx, database.ClearBudgetAlertStateParams{
				CategoryID: cell.CategoryID,
				Year:       int64(cell.Year),
				Month:      int64(cell.Month),
			}); err != nil {
				log.Printf("budget alert: clear latch cat=%d %04d-%02d: %v",
					cell.CategoryID, cell.Year, cell.Month, err)
			}
			continue
		}

		// Over budget. If an over_budget send is currently suppressed by quiet
		// hours, do NOT latch — leaving the cell un-latched lets the next
		// post-quiet evaluation re-fire this cross instead of dropping it.
		if suppressOverBudget {
			continue
		}

		// Over budget: try to set the latch. rows-affected==1 means a fresh
		// cross (we won the INSERT); 0 means the latch was already set (dedup).
		res, err := h.queries.SetBudgetAlertState(ctx, database.SetBudgetAlertStateParams{
			CategoryID: cell.CategoryID,
			Year:       int64(cell.Year),
			Month:      int64(cell.Month),
		})
		if err != nil {
			log.Printf("budget alert: set latch cat=%d %04d-%02d: %v",
				cell.CategoryID, cell.Year, cell.Month, err)
			continue
		}
		inserted, err := res.RowsAffected()
		if err != nil || inserted == 0 {
			continue // already latched (dedup) or driver hiccup — stay silent
		}

		// Fresh cross: collect it; the send happens once after the loop so two
		// categories crossing in one request can collapse into a single push.
		// The latch is set regardless of whether any subscriber exists, so a
		// later subscribe + re-eval does not retroactively double-fire.
		crossed = append(crossed, crossedCell{
			cell:         cell,
			name:         catName,
			spentDollars: centsToDollars(spentCents),
			limitDollars: centsToDollars(limitCents),
		})
	}

	switch len(crossed) {
	case 0:
		return
	case 1:
		h.sendOverBudgetCell(ctx, crossed[0])
	default:
		h.sendOverBudgetSummary(ctx, crossed)
	}
}

// sendOverBudgetSummary fans out ONE collapsing push when two or more cells
// crossed in a single request. It uses the fixed budget-summary tag / ob-summary
// Topic so repeated multi-cross bursts replace rather than stack. The per-category
// dollar fields are intentionally left zero — the summary is a list, and the SW
// renders title/body/url, so no *_cents value is ever marshalled here.
func (h *Handler) sendOverBudgetSummary(ctx context.Context, crossed []crossedCell) {
	names := make([]string, 0, len(crossed))
	for _, c := range crossed {
		names = append(names, c.name)
	}
	payload := pushAlertPayload{
		Title: "Over budget",
		Body:  fmt.Sprintf("%d categories over budget: %s", len(crossed), strings.Join(names, ", ")),
		URL:   "/budgets",
		Type:  "budget_over",
		Tag:   budgetSummaryTag,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("budget alert: marshal summary payload: %v", err)
		return
	}
	_, _, urgency := pushOptionsFor("over_budget")
	h.fanOutPush(ctx, "over_budget", body, 0,
		pushOpts{Tag: budgetSummaryTag, Topic: budgetSummaryTopic, Urgency: urgency})
}

// cellOverBudget computes whether one (category, month) is over budget by
// REUSING overBudgetByCategory over the same ListCategoryBudgetsByMonth +
// SumByCategoryForMonth pairing that the homepage widget uses — it is the
// single source of truth for "over budget". Returns the category's limit and
// month-to-date spend (both cents) plus its name for the payload.
//
// SumByCategoryForMonth already filters c.type='expense' AND t.deleted_at IS
// NULL, so a tombstoned sentinel row cannot inflate the spend (the
// *_HidesTombstoned invariant). ListCategoryBudgetsByMonth keys on int64
// year/month; SumByCategoryForMonth keys on the strftime TEXT form, matching
// the existing over-budget pairing.
func (h *Handler) cellOverBudget(ctx context.Context, cell budgetCell) (over bool, limitCents, spentCents int64, name string, err error) {
	limits, err := h.queries.ListCategoryBudgetsByMonth(ctx, database.ListCategoryBudgetsByMonthParams{
		Year:  int64(cell.Year),
		Month: int64(cell.Month),
	})
	if err != nil {
		return false, 0, 0, "", err
	}
	if len(limits) == 0 {
		return false, 0, 0, "", nil // no limit set for this month -> never over
	}

	spend, err := h.queries.SumByCategoryForMonth(ctx, database.SumByCategoryForMonthParams{
		Year:  fmtYear(cell.Year),
		Month: fmtMonth(cell.Month),
	})
	if err != nil {
		return false, 0, 0, "", err
	}

	status, ok := overBudgetByCategory(spend, limits)[cell.CategoryID]
	if !ok || !status.Over {
		// Not over (or this category has a limit but no spend row at all).
		// Surface the limit when we have it so an under-eval still clears the latch.
		return false, status.LimitCents, 0, "", nil
	}

	// Over: pull the matching spend + name from the rows we already loaded.
	for _, s := range spend {
		if s.ID == cell.CategoryID {
			return true, status.LimitCents, s.TotalCents, s.Name, nil
		}
	}
	// Defensive: Over==true implies a spend row exists, but never panic.
	return true, status.LimitCents, 0, "", nil
}

// fmtYear / fmtMonth format the cell's calendar fields into the strftime TEXT
// shape SumByCategoryForMonth keys on ("2006" / "01"). Kept tiny and local so
// the cents/strftime boundary lives next to its only caller.
func fmtYear(y int) string  { return fmt.Sprintf("%04d", y) }
func fmtMonth(m int) string { return fmt.Sprintf("%02d", m) }

// cellForDate derives the (category, calendar-month) cell from a transaction's
// OWN date — read in UTC so a back-dated row maps to the month it actually
// lands in, NOT h.clock.Now(). Used by every commit site to feed
// evaluateBudgetAlerts.
func cellForDate(categoryID int64, date time.Time) budgetCell {
	d := date.UTC()
	return budgetCell{CategoryID: categoryID, Year: d.Year(), Month: int(d.Month())}
}

// cellsForCreate / cellsForDelete: a single affected cell.
func cellsForCreate(categoryID int64, date time.Time) []budgetCell {
	return []budgetCell{cellForDate(categoryID, date)}
}

func cellsForDelete(categoryID int64, date time.Time) []budgetCell {
	return []budgetCell{cellForDate(categoryID, date)}
}

// cellsForUpdate returns the OLD and NEW cells deduped. An edit that moves a
// row between categories or months can both clear the old cell's latch (spend
// dropped there) and trip the new cell — both must be evaluated.
func cellsForUpdate(oldCat int64, oldDate time.Time, newCat int64, newDate time.Time) []budgetCell {
	oldCell := cellForDate(oldCat, oldDate)
	newCell := cellForDate(newCat, newDate)
	if oldCell == newCell {
		return []budgetCell{newCell}
	}
	return []budgetCell{oldCell, newCell}
}

// notifTypeEnabled reads the household notification_settings row and reports
// whether the given notification type is currently switched on. Unknown types
// are treated as DISABLED (fail-closed) so a typo'd type id can never spam
// every device. The over-budget LATCH is set regardless of this toggle (see
// evaluateBudgetAlerts) — only the SEND is gated here, so toggling a type back
// on does not retroactively re-fire a cross that already latched while off.
func notifTypeEnabled(s database.NotificationSettings, notifType string) bool {
	switch notifType {
	case "over_budget":
		return s.OverBudget
	case "txn_added":
		return s.TxnAdded
	case "txn_deleted":
		return s.TxnDeleted
	case "txn_edited":
		return s.TxnEdited
	case "large_txn":
		return s.LargeTxn
	case "digest":
		return s.DigestMode != "off"
	default:
		return false
	}
}

// fanOutPush delivers one already-marshalled payload to EVERY push
// subscription in the household (shared visibility, same model as transactions
// and categories — over-budget is a household signal, not a per-user one). It
// is best-effort: any per-send error is counted and the loop continues.
//
// Household gate: the SEND is gated on the household notification_settings
// toggle for notifType — when that type is switched off the entire fan-out is
// a no-op. The over-budget latch (evaluateBudgetAlerts) is unaffected by the
// toggle, so flipping a type back on never retroactively re-fires a cross that
// already latched while it was off.
//
// Send-time pruning: when Send reports prune==true (the transport saw HTTP 404
// or 410 — the endpoint is permanently gone) the stale row is deleted by
// endpoint so it is not retried on the next alert. Transient failures (401/
// 403/429/5xx) never prune — they are counted as failed and the row is kept.
//
// Logging: a SINGLE bounded summary line per fan-out, "push fan-out: type=T
// sent=N pruned=M failed=K". Endpoint URLs are a bearer-grade secret (anyone
// holding one can push to that device) and are NEVER logged. No per-row
// logging, so a household with hundreds of devices cannot flood the log.
//
// Actor exclusion (WhatsApp "don't echo the sender"): excludeUserID is the
// acting user's id for ACTIVITY notifications — every subscription belonging to
// that account is skipped (all the actor's devices, not just one) so the author
// is not notified of their own action. excludeUserID==0 means exclude nobody
// (real user ids are positive); over_budget passes 0 because a state alert must
// reach everyone including the actor. Skipped subscriptions are not counted as
// sent/pruned/failed.
//
// SYNCHRONOUS PREFIX / ASYNCHRONOUS DELIVERY: everything down to and including
// the quiet-hours gate runs on the caller's goroutine — those are two cheap
// local SQLite reads, and keeping them here means a household that has the type
// switched off costs one settings read and nothing else (no goroutine, no
// scheduling). The moment a send is actually owed, delivery is handed to
// deliverInBackground and this function returns. NOTHING that talks to a push
// gateway runs on the request goroutine: a user's save must never wait on a
// third-party network call. See deliverInBackground for the context lifetime.
//
// The returned fanOutResult tells a caller that keeps a cursor whether a send it
// owed was actually accepted; callers with nothing to reconcile ignore it.
func (h *Handler) fanOutPush(ctx context.Context, notifType string, payload []byte, excludeUserID int64, opts pushOpts) fanOutResult {
	d := h.dispatcher()
	if d == nil {
		return fanOutGated // push disabled — no-op
	}
	// Household gate: skip the entire fan-out when this type is switched off.
	// The settings row is seeded at id=1 by migration 015, so a read error here
	// is a real fault, not a missing row — log it and fail closed (no send).
	settings, err := h.queries.GetNotificationSettings(ctx)
	if err != nil {
		log.Printf("push fan-out: read notification settings: %v", err)
		return fanOutGated
	}
	if !notifTypeEnabled(settings, notifType) {
		return fanOutGated // type disabled household-wide — no-op
	}
	// Quiet-hours gate (household-wide). Inside the window, real-time ACTIVITY
	// pushes are suppressed; over_budget bypasses unless the household disabled
	// the bypass toggle. The digest uses a non-activity type, so it passes this
	// gate untouched even when digest_time falls inside quiet hours — the digest
	// owns its own schedule. Reuses the settings row already read above (one
	// GetNotificationSettings call).
	if inQuietHours(h.clock.Now(), settings.QuietStart, settings.QuietEnd, settings.QuietTz) {
		if tag, _, _ := pushOptionsFor(notifType); tag == "activity" {
			return fanOutGated // activity suppressed during quiet hours
		}
		if notifType == "over_budget" && !settings.QuietAllowOverBudget {
			return fanOutGated // over_budget suppressed by the bypass toggle
		}
	}
	return h.deliverInBackground(ctx, d, notifType, payload, excludeUserID, opts)
}

// fanOutResult reports what fanOutPush did with one emit. Most callers ignore
// it: an over-budget cross latches in the database before the fan-out, and an
// activity notification has nothing to reconcile. The digest is the caller that
// must tell these apart, because it advances a once-a-day cursor and must not
// move it past a rollup that was owed and then thrown away.
type fanOutResult int

const (
	// fanOutGated: no send was owed. Push is disabled, the household has this
	// type switched off, quiet hours suppressed it, or the settings read failed
	// and we failed closed. A caller with a cursor should treat the pass as
	// complete — nothing was lost, so retrying would never produce a send.
	//
	// Deliberately the zero value: a caller that never reaches fanOutPush at all
	// (nothing to report this tick) is in exactly this state.
	fanOutGated fanOutResult = iota
	// fanOutQueued: delivery was accepted and is running in the background. It
	// may still fail per device — delivery is best-effort, as it was when it ran
	// inline — but it reached the transport, which is as far as any caller can
	// observe.
	fanOutQueued
	// fanOutDropped: a send WAS owed and was refused before reaching the
	// transport (the in-flight cap, or a shutdown drain). Nothing was sent and
	// nothing will retry it, so a caller with a cursor must leave the cursor
	// where it is.
	fanOutDropped
)

// pushFanOutTimeout bounds ONE background fan-out end to end. It is a
// goroutine-leak guard, not a delivery SLA: push.defaultHTTPTimeout (30 s)
// already bounds each individual send, and this caps the serial loop over every
// household subscription so a wedged gateway cannot pin a goroutine (and the
// payload it captured) for the life of the process. Five minutes covers nine
// consecutive full 30 s stalls, an order of magnitude more devices than a
// household registers.
const pushFanOutTimeout = 5 * time.Minute

// maxInFlightFanOuts caps how many fan-outs may be delivering at once. Each one
// is a goroutine holding a payload plus the subscription list, and each can live
// for pushFanOutTimeout, so without a cap a client hammering a mutation endpoint
// while the gateway is unreachable would accumulate them without bound. At
// household scale (a handful of devices, sub-second sends) the cap is never
// approached; it exists so the failure mode under a pathological burst is a
// bounded number of dropped notifications with a log line, not unbounded memory.
const maxInFlightFanOuts = 64

// admitDelivery reserves one of the maxInFlightFanOuts delivery slots, reporting
// whether the caller got one. Both refusal reasons are logged, and neither ever
// names an endpoint.
//
// The check and the increment happen together under pushMu so two concurrent
// emitters cannot both see room that only one of them has, and so a fan-out
// cannot slip in between a drain setting pushDraining and the drain parking on
// pushIdle.
func (h *Handler) admitDelivery(notifType string) bool {
	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	if h.pushDraining {
		log.Printf("push fan-out: type=%s dropped, delivery is draining for shutdown", notifType)
		return false
	}
	if h.pushInFlight >= maxInFlightFanOuts {
		log.Printf("push fan-out: type=%s dropped, %d deliveries already in flight",
			notifType, maxInFlightFanOuts)
		return false
	}
	h.pushInFlight++
	return true
}

// inFlightDeliveries reports how many fan-outs are admitted and not yet
// finished. Package-private: the number is a test observation point for the
// admission cap, not something a caller outside this package should branch on.
func (h *Handler) inFlightDeliveries() int {
	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	return h.pushInFlight
}

// finishDelivery releases the slot admitDelivery took and wakes a parked drain
// once the last worker is done.
func (h *Handler) finishDelivery() {
	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	h.pushInFlight--
	if h.pushInFlight == 0 && h.pushIdle != nil {
		close(h.pushIdle)
		h.pushIdle = nil
	}
}

// deliverInBackground runs one already-gated fan-out off the caller's goroutine.
//
// CONTEXT LIFETIME is the subtle part. The caller's ctx is almost always
// r.Context(), which net/http cancels the instant the response is written — so
// carrying it into the goroutine would abort the very sends this moves off the
// request path, silently deleting the feature while every existing test still
// passed. context.WithoutCancel keeps any request-scoped values while dropping
// the cancellation, and WithTimeout then re-bounds it with a deadline of our own
// so the detached goroutine still cannot run forever.
//
// The goroutine also recovers its own panics: it runs outside chi's Recoverer,
// so an unrecovered panic here would take the whole process down rather than
// failing one request.
func (h *Handler) deliverInBackground(ctx context.Context, d pushDispatcher, notifType string, payload []byte, excludeUserID int64, opts pushOpts) fanOutResult {
	if !h.admitDelivery(notifType) {
		return fanOutDropped
	}
	// The slot is taken on THIS goroutine, before we return, so a caller that
	// has finished emitting can drain (WaitForPushDelivery) without racing the
	// worker it is waiting for.
	go func() {
		// Registered first so it runs LAST: the count only drops once the
		// delivery, its context cleanup and any panic logging are all complete.
		defer h.finishDelivery()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("push fan-out: panic delivering type=%s: %v\n%s", notifType, r, debug.Stack())
			}
		}()
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pushFanOutTimeout)
		defer cancel()
		h.deliverPush(sendCtx, d, notifType, payload, excludeUserID, opts)
	}()
	return fanOutQueued
}

// deliverPush is the delivery half of fanOutPush: it reads the household's
// subscriptions and sends to each one, applying actor exclusion, send-time
// pruning and the single bounded summary log documented on fanOutPush. It runs
// on the background goroutine deliverInBackground starts; every gate has already
// been evaluated by the time it is called.
func (h *Handler) deliverPush(ctx context.Context, d pushDispatcher, notifType string, payload []byte, excludeUserID int64, opts pushOpts) {
	subs, err := h.queries.ListAllPushSubscriptions(ctx)
	if err != nil {
		log.Printf("push fan-out: list subscriptions: %v", err)
		return
	}
	var sent, pruned, failed int
	for _, s := range subs {
		// Skip the actor's own subscriptions for activity notifications — do not
		// count them as sent/pruned/failed (excludeUserID==0 disables this).
		if excludeUserID != 0 && s.UserID == excludeUserID {
			continue
		}
		prune, err := d.Send(ctx, push.Subscription{
			Endpoint: s.Endpoint,
			P256dh:   s.P256dh,
			Auth:     s.Auth,
		}, payload, push.Options{Topic: opts.Topic, Urgency: opts.Urgency})
		switch {
		case prune:
			// 404/410: endpoint permanently gone — delete by endpoint so we
			// stop retrying it. DeletePushSubscriptionByEndpoint is idempotent.
			if derr := h.queries.DeletePushSubscriptionByEndpoint(ctx, s.Endpoint); derr != nil {
				log.Printf("push fan-out: prune stale subscription: %v", derr)
			}
			pruned++
		case err != nil:
			failed++ // transient — keep the row, do not log the endpoint
		default:
			sent++
		}
	}
	// One bounded summary line. NEVER log endpoint URLs.
	log.Printf("push fan-out: type=%s sent=%d pruned=%d failed=%d", notifType, sent, pruned, failed)
}
