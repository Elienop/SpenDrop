package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

		// Fresh cross. Build the dollars payload and fan out. The latch is set
		// regardless of whether any subscriber exists, so a later subscribe +
		// re-eval does not retroactively double-fire.
		limitDollars := centsToDollars(limitCents)
		spentDollars := centsToDollars(spentCents)
		payload := pushAlertPayload{
			Title: fmt.Sprintf("Over budget: %s", catName),
			Body: fmt.Sprintf("%s is over budget — $%.2f spent of your $%.2f limit this month.",
				catName, spentDollars, limitDollars),
			URL:          "/budgets",
			Type:         "budget_over",
			CategoryID:   cell.CategoryID,
			CategoryName: catName,
			Year:         cell.Year,
			Month:        cell.Month,
			LimitDollars: limitDollars,
			SpentDollars: spentDollars,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("budget alert: marshal payload cat=%d: %v", cell.CategoryID, err)
			continue
		}
		// state alert: notify everyone, incl. actor. Base over_budget opts only;
		// real per-cell tag/topic is wired in the OverBudgetSummary area.
		obTag, obTopic, obUrgency := pushOptionsFor("over_budget")
		h.fanOutPush(ctx, "over_budget", body, 0,
			pushOpts{Tag: obTag, Topic: obTopic, Urgency: obUrgency})
	}
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
func (h *Handler) fanOutPush(ctx context.Context, notifType string, payload []byte, excludeUserID int64, opts pushOpts) {
	d := h.dispatcher()
	if d == nil {
		return // push disabled — no-op
	}
	// Household gate: skip the entire fan-out when this type is switched off.
	// The settings row is seeded at id=1 by migration 015, so a read error here
	// is a real fault, not a missing row — log it and fail closed (no send).
	settings, err := h.queries.GetNotificationSettings(ctx)
	if err != nil {
		log.Printf("push fan-out: read notification settings: %v", err)
		return
	}
	if !notifTypeEnabled(settings, notifType) {
		return // type disabled household-wide — no-op
	}
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
