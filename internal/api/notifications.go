package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"

	"github.com/elienop/spendrop/internal/database"
)

// Typed transaction-activity notifications. Each helper is POST-COMMIT and
// BEST-EFFORT: it is invoked after the mutation has already committed (same
// contract as evaluateBudgetAlerts), so it NEVER returns an error and never
// blocks or rolls back the request. All gating lives in fanOutPush, which
// no-ops when the household has the type switched off — so a disabled type
// costs one settings read and nothing else. When a send IS owed, fanOutPush
// queues it on a background goroutine and returns, so none of these helpers
// ever puts a push gateway between the user and their response.
//
// Money in bodies is DOLLARS via centsToDollars (never a raw *_cents value),
// and always the MAGNITUDE with the direction carried in words — see
// activityPhrase.
// The category label is looked up via GetCategoryByID; a lookup miss degrades
// to "a category" rather than failing the notification.

// categoryLabel resolves a human category name for a notification body,
// degrading gracefully — a missing/errored category must not drop the push.
func (h *Handler) categoryLabel(ctx context.Context, categoryID int64) string {
	cat, err := h.queries.GetCategoryByID(ctx, categoryID)
	if err != nil || cat.Name == "" {
		return "a category"
	}
	return cat.Name
}

// emit marshals a typed payload and hands it to the household fan-out. Marshal
// failures are logged and swallowed (best-effort). excludeUserID is the acting
// user's id so the action's author is not notified of their own activity (0 =
// exclude nobody); it is threaded straight through to fanOutPush.
// neutralizeForPushBody replaces every character that can fabricate structure
// in a rendered notification with a single space.
//
// THIS IS THE SINK-SIDE HALF OF THE SAME INVARIANT validateDisplayName GUARDS
// AT THE SOURCE, and it exists because the source side cannot cover the whole
// sink. The body is assembled as
// fmt.Sprintf("%s added $%.2f in %s — %s", actor, dollars, category, description)
// and the service worker rolls several activities into one notification with
// lines.join('\n'), so a newline anywhere in that string forges a line that
// reads as a genuine, separately-attributed activity.
//
// Three of those four fields are NOT covered by a source-side gate, and two of
// them cannot be:
//
//   - DESCRIPTION is the strongest vector of the three. Any authenticated
//     member can write it, and it is the LAST field, so a forged line has no
//     trailing residue to give it away — a newline in the actor's name leaves
//     " added $12.34 in Groceries — Milk" dangling after the forgery, while a
//     newline in the description does not. It CANNOT be fixed by refusing the
//     input: descriptions also arrive from xlsx import, an Excel cell may
//     legitimately contain a newline (Alt+Enter), and rejecting those would
//     drop rows the household can import today.
//   - CATEGORY LABEL is admin-written and length-bounded only.
//   - DISPLAY NAME is now gated at every write path, but rows stored BEFORE
//     that gate keep whatever they have; there is no backfill migration. This
//     is what closes that residual.
//
// So the gate at the source stops a name being stored in a forging shape, and
// this stops anything at all reaching the renderer in one — belt and braces on
// purpose, because they fail differently: the source gate cannot reach legacy
// rows or imported text, and this cannot stop a bad name being stored.
//
// A SPACE rather than deletion: deleting joins the words on either side, which
// is its own small forgery ("Rent Milk" from two separate lines). The predicate
// is shared with validateDisplayName so there is exactly one definition of
// "forges structure" — two would drift, and the drift would be invisible until
// something rendered wrong on somebody's phone.
func neutralizeForPushBody(s string) string {
	return strings.Map(func(r rune) rune {
		if forgesStructure(r) {
			return ' '
		}
		return r
	}, s)
}

func (h *Handler) emit(ctx context.Context, notifType, title, body, url string, excludeUserID int64) {
	tag, topic, urgency := pushOptionsFor(notifType)
	payload := pushAlertPayload{
		// Neutralised HERE, at the one chokepoint every push body passes
		// through, rather than at the five call sites that build one. A sixth
		// call site is the way a per-site fix would be missed.
		Title: neutralizeForPushBody(title),
		Body:  neutralizeForPushBody(body),
		URL:   url,
		Type:  notifType,
		Tag:   tag,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		log.Printf("notify: marshal %s payload: %v", notifType, err)
		return
	}
	h.fanOutPush(ctx, notifType, b, excludeUserID, pushOpts{Tag: tag, Topic: topic, Urgency: urgency})
}

// activityPhrase renders the money half of an activity body: "added $12.34", or
// "added a refund of $20.00" when the row is a refund (negative cents, B10).
//
// The MAGNITUDE is what gets formatted, always. Feeding the signed value to
// "$%.2f" produced "added $-20.00", which reads as a typo on a phone and, worse,
// reads as a SMALLER number than it is. The sign is carried by the wording
// instead, so the two facts a reader needs — how much, and which direction —
// arrive as words rather than as a character they might not notice.
//
// verb is the caller's ("added" / "edited" / "deleted"), so there is exactly one
// definition of refund wording across all four bodies. Push copy is what the
// household reads on a lock screen; keep it plain.
func activityPhrase(verb string, cents int64) string {
	if cents < 0 {
		// magnitudeCents, not a bare -cents: negation is not total on int64,
		// and this is the one place the result is shown to a human.
		return fmt.Sprintf("%s a refund of $%.2f", verb, centsToDollars(magnitudeCents(cents)))
	}
	return fmt.Sprintf("%s $%.2f", verb, centsToDollars(cents))
}

// magnitudeCents is the absolute value of a signed cents amount, for the
// threshold comparisons that ask "how big is this" rather than "which way does
// it go".
func magnitudeCents(cents int64) int64 {
	// math.MinInt64 has no positive counterpart, so negating it yields itself:
	// the largest amount int64 can hold would compare BELOW every threshold and
	// silence the alert it most deserves, and print as a negative dollar figure
	// in the push body. Unreachable from any write path today (validateMoneyAmount
	// bounds a stored amount at 1e11 cents) — this keeps the function total, so a
	// future caller reading amount_cents straight from a row cannot be surprised
	// by a negative magnitude. Clamping loses ONE cent of an already absurd value
	// and keeps every comparison and every rendering correctly signed.
	if cents == math.MinInt64 {
		return math.MaxInt64
	}
	if cents < 0 {
		return -cents
	}
	return cents
}

// largeTxnEnabledAndThreshold reads the household large-transaction threshold
// and whether the large_txn type is enabled. A settings read error fails closed
// (large path off) so we fall through to the normal activity type.
func (h *Handler) largeTxnEnabledAndThreshold(ctx context.Context) (enabled bool, thresholdCents int64) {
	s, err := h.queries.GetNotificationSettings(ctx)
	if err != nil {
		return false, 0
	}
	return s.LargeTxn, s.LargeTxnThresholdCents
}

// notifyTxnAdded fires after a single transaction create. LARGE-TXN PRECEDENCE:
// when large_txn is enabled and the amount is at/over the household threshold,
// we send ONE "large_txn" push INSTEAD OF "txn_added" — never both.
//
// The threshold is compared against the MAGNITUDE: a $5,000 refund is exactly
// as worth knowing about as a $5,000 purchase, and it is the shape a mistyped
// minus takes. Comparing the signed value made every refund, however large,
// fall through to the quiet activity type — the household's only alert on a big
// wrong number, silenced by the sign that made it wrong.
func (h *Handler) notifyTxnAdded(ctx context.Context, txn database.Transaction, actor string, excludeUserID int64) {
	if largeEnabled, threshold := h.largeTxnEnabledAndThreshold(ctx); largeEnabled && magnitudeCents(txn.AmountCents) >= threshold {
		h.emitLarge(ctx, txn, "added", actor, excludeUserID)
		return
	}
	cat := h.categoryLabel(ctx, txn.CategoryID)
	h.emit(ctx, "txn_added",
		"Transaction added",
		fmt.Sprintf("%s %s in %s — %s", actor, activityPhrase("added", txn.AmountCents), cat, txn.Description),
		"/transactions", excludeUserID)
}

// notifyTxnEdited fires after a single transaction update. Same large-txn
// precedence as create, on the same magnitude comparison.
func (h *Handler) notifyTxnEdited(ctx context.Context, txn database.Transaction, actor string, excludeUserID int64) {
	if largeEnabled, threshold := h.largeTxnEnabledAndThreshold(ctx); largeEnabled && magnitudeCents(txn.AmountCents) >= threshold {
		h.emitLarge(ctx, txn, "edited", actor, excludeUserID)
		return
	}
	cat := h.categoryLabel(ctx, txn.CategoryID)
	h.emit(ctx, "txn_edited",
		"Transaction edited",
		fmt.Sprintf("%s %s in %s — %s", actor, activityPhrase("edited", txn.AmountCents), cat, txn.Description),
		"/transactions", excludeUserID)
}

// notifyTxnDeleted fires after a single transaction delete. No large-txn
// precedence on delete — a removal is activity, not a large spend signal.
func (h *Handler) notifyTxnDeleted(ctx context.Context, txn database.Transaction, actor string, excludeUserID int64) {
	cat := h.categoryLabel(ctx, txn.CategoryID)
	h.emit(ctx, "txn_deleted",
		"Transaction deleted",
		fmt.Sprintf("%s %s in %s — %s", actor, activityPhrase("deleted", txn.AmountCents), cat, txn.Description),
		"/transactions", excludeUserID)
}

// emitLarge sends the "large_txn" payload. verb is the originating op
// ("added"/"edited") purely for the body wording.
func (h *Handler) emitLarge(ctx context.Context, txn database.Transaction, verb string, actor string, excludeUserID int64) {
	cat := h.categoryLabel(ctx, txn.CategoryID)
	h.emit(ctx, "large_txn",
		"Large transaction",
		fmt.Sprintf("%s %s in %s — %s", actor, activityPhrase(verb, txn.AmountCents), cat, txn.Description),
		"/transactions", excludeUserID)
}

// notifyTxnBatch fires once per batch operation (create/delete), aggregating
// the count into a single push — never one-per-row. kind is "added" or
// "deleted" and selects the txn_added / txn_deleted type. Batches send only the
// activity aggregate; large_txn is intentionally NOT evaluated for batches in
// v1 (a bulk import should not fan out N large alerts).
func (h *Handler) notifyTxnBatch(ctx context.Context, kind string, count int, actor string, excludeUserID int64) {
	if count <= 0 {
		return
	}
	var notifType, title string
	switch kind {
	case "added":
		notifType, title = "txn_added", "Transactions added"
	case "deleted":
		notifType, title = "txn_deleted", "Transactions deleted"
	default:
		return // unknown kind — no-op
	}
	noun := "transactions"
	if count == 1 {
		noun = "transaction"
	}
	h.emit(ctx, notifType, title,
		fmt.Sprintf("%s %s %d %s", actor, kind, count, noun),
		"/transactions", excludeUserID)
}
