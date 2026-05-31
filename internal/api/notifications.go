package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/elienop/spendrop/internal/database"
)

// Typed transaction-activity notifications. Each helper is POST-COMMIT and
// BEST-EFFORT: it is invoked after the mutation has already committed (same
// contract as evaluateBudgetAlerts), so it NEVER returns an error and never
// blocks or rolls back the request. All gating + the actual send live in
// fanOutPush, which no-ops when the household has the type switched off — so a
// disabled type costs one settings read and nothing else.
//
// Money in bodies is DOLLARS via centsToDollars (never a raw *_cents value).
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
func (h *Handler) emit(ctx context.Context, notifType, title, body, url string, excludeUserID int64) {
	payload := pushAlertPayload{
		Title: title,
		Body:  body,
		URL:   url,
		Type:  notifType,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		log.Printf("notify: marshal %s payload: %v", notifType, err)
		return
	}
	h.fanOutPush(ctx, notifType, b, excludeUserID)
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
func (h *Handler) notifyTxnAdded(ctx context.Context, txn database.Transaction, excludeUserID int64) {
	if largeEnabled, threshold := h.largeTxnEnabledAndThreshold(ctx); largeEnabled && txn.AmountCents >= threshold {
		h.emitLarge(ctx, txn, "added", excludeUserID)
		return
	}
	dollars := centsToDollars(txn.AmountCents)
	cat := h.categoryLabel(ctx, txn.CategoryID)
	h.emit(ctx, "txn_added",
		"Transaction added",
		fmt.Sprintf("$%.2f in %s — %s", dollars, cat, txn.Description),
		"/transactions", excludeUserID)
}

// notifyTxnEdited fires after a single transaction update. Same large-txn
// precedence as create.
func (h *Handler) notifyTxnEdited(ctx context.Context, txn database.Transaction, excludeUserID int64) {
	if largeEnabled, threshold := h.largeTxnEnabledAndThreshold(ctx); largeEnabled && txn.AmountCents >= threshold {
		h.emitLarge(ctx, txn, "edited", excludeUserID)
		return
	}
	dollars := centsToDollars(txn.AmountCents)
	cat := h.categoryLabel(ctx, txn.CategoryID)
	h.emit(ctx, "txn_edited",
		"Transaction edited",
		fmt.Sprintf("$%.2f in %s — %s", dollars, cat, txn.Description),
		"/transactions", excludeUserID)
}

// notifyTxnDeleted fires after a single transaction delete. No large-txn
// precedence on delete — a removal is activity, not a large spend signal.
func (h *Handler) notifyTxnDeleted(ctx context.Context, txn database.Transaction, excludeUserID int64) {
	dollars := centsToDollars(txn.AmountCents)
	cat := h.categoryLabel(ctx, txn.CategoryID)
	h.emit(ctx, "txn_deleted",
		"Transaction deleted",
		fmt.Sprintf("$%.2f in %s — %s", dollars, cat, txn.Description),
		"/transactions", excludeUserID)
}

// emitLarge sends the "large_txn" payload. verb is the originating op
// ("added"/"edited") purely for the body wording.
func (h *Handler) emitLarge(ctx context.Context, txn database.Transaction, verb string, excludeUserID int64) {
	dollars := centsToDollars(txn.AmountCents)
	cat := h.categoryLabel(ctx, txn.CategoryID)
	h.emit(ctx, "large_txn",
		"Large transaction",
		fmt.Sprintf("$%.2f %s in %s — %s", dollars, verb, cat, txn.Description),
		"/transactions", excludeUserID)
}

// notifyTxnBatch fires once per batch operation (create/delete), aggregating
// the count into a single push — never one-per-row. kind is "added" or
// "deleted" and selects the txn_added / txn_deleted type. Batches send only the
// activity aggregate; large_txn is intentionally NOT evaluated for batches in
// v1 (a bulk import should not fan out N large alerts).
func (h *Handler) notifyTxnBatch(ctx context.Context, kind string, count int, excludeUserID int64) {
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
		fmt.Sprintf("%d %s %s", count, noun, kind),
		"/transactions", excludeUserID)
}
