package database

import "strings"

// IsIdempotencyKeyUniqueViolation reports whether err is a UNIQUE constraint
// violation on the partial index idx_transactions_idempotency_key — that is,
// whether an INSERT lost a race (or repeated an earlier submission) against a
// row already carrying the same client-supplied key.
//
// It is the sibling of IsContentHashUniqueViolation and matches on the same
// principle for the same reasons documented there: the qualifying column name
// is the whole point of the decision, sqlite3.Error does not expose it as a
// struct field, and upstream SQLite emits the message from sqlite3.c as
// "UNIQUE constraint failed: %s.%s" so the format cannot drift on a driver
// bump. A generic "is this a UNIQUE violation" check is forbidden here: a
// users.username or categories.name violation reaching this branch would be
// silently converted into a fake success carrying somebody else's row.
//
// The index is COMPOSITE — (user_id, idempotency_key), see migration 017 — and
// SQLite names every column of a composite index, table-qualifying each one:
//
//	UNIQUE constraint failed: transactions.user_id, transactions.idempotency_key
//
// So the string here is not "transactions.idempotency_key" and matching on that
// alone would silently stop firing, sending every replay to a 500. Anyone
// changing the index's column list must change this string in the same commit;
// TestIdempotencyAndContentHashDetectorsDoNotSwallowEachOther builds its errors
// from real INSERTs against the real index, so a mismatch fails there.
//
// The two detectors must never swallow each other, and by construction they
// cannot: a single INSERT can violate EITHER index (a retried submission
// carrying both the same key and the same content collides on both, and which
// one SQLite reports first is not specified), but the two messages name
// different columns and neither string contains the other. handleCreateTransaction
// relies on exactly that — it tries the content-hash recovery first and then
// re-tests for this violation, so an insert reported under either name still
// lands on the right branch.
//
// The substring check survives fmt.Errorf("create transaction: %w", …) in
// TransactionStore.Create/CreateTx because err.Error() includes the wrapped text.
func IsIdempotencyKeyUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(),
		"UNIQUE constraint failed: transactions.user_id, transactions.idempotency_key")
}
