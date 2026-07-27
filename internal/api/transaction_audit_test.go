package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// mustParseDate parses a YYYY-MM-DD string and fails the test on error.
// The helper is scoped to this file rather than shared with the larger
// transaction_handlers_test.go helpers because only the direct-store
// smoke test needs a parsed time.Time — all HTTP-path tests already
// format dates as strings in their JSON bodies.
func mustParseDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

// The tests in this file verify the append-only audit invariant enforced
// by internal/database/store.go: every successful mutation through a
// transaction handler writes exactly one audit row (per-row for single
// and batch endpoints, one summary row for bulk endpoints), and every
// rolled-back mutation leaves the audit table untouched.
//
// The checks rely on raw SQL queries rather than the sqlc-generated
// helpers so the assertions make the contract obvious: "what exists in
// the transaction_audit table after this handler call, and does it say
// what we expect?"

// countAuditRows returns the total number of rows in transaction_audit.
// Raw SQL rather than sqlc to keep the assertion explicit.
func countAuditRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transaction_audit`).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

// auditRowForAssert mirrors database.TransactionAudit but exposes before/
// after JSON as generic maps so tests can probe fields without knowing
// every column in the serialised transaction.
type auditRowForAssert struct {
	ID            int64
	TransactionID int64
	Action        string
	ActorUserID   sql.NullInt64
	Before        map[string]any
	After         map[string]any
	BeforeRaw     string
	AfterRaw      string
}

// listAuditRows reads every row in the audit table ordered by id. Using
// raw SQL and decoding JSON into map[string]any keeps the test resilient
// to schema churn in the Transaction / GetTransactionByIDRow structs — we
// want to assert "before_json contains this field" without recompiling
// the test every time the struct grows.
func listAuditRows(t *testing.T, db *sql.DB) []auditRowForAssert {
	t.Helper()
	rows, err := db.Query(`SELECT id, transaction_id, action, actor_user_id, before_json, after_json FROM transaction_audit ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("select audit rows: %v", err)
	}
	defer rows.Close()
	var out []auditRowForAssert
	for rows.Next() {
		var (
			r          auditRowForAssert
			beforeJSON sql.NullString
			afterJSON  sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.TransactionID, &r.Action, &r.ActorUserID, &beforeJSON, &afterJSON); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		r.BeforeRaw = beforeJSON.String
		r.AfterRaw = afterJSON.String
		if beforeJSON.Valid && beforeJSON.String != "" {
			if err := json.Unmarshal([]byte(beforeJSON.String), &r.Before); err != nil {
				t.Fatalf("parse before_json: %v", err)
			}
		}
		if afterJSON.Valid && afterJSON.String != "" {
			if err := json.Unmarshal([]byte(afterJSON.String), &r.After); err != nil {
				t.Fatalf("parse after_json: %v", err)
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	return out
}

// assertSingleActorID fails the test if any row has a NULL or mismatched
// actor_user_id. Every audit row written by an authenticated handler must
// record the caller.
func assertAllActorsEqual(t *testing.T, rows []auditRowForAssert, want int64) {
	t.Helper()
	for i, r := range rows {
		if !r.ActorUserID.Valid {
			t.Errorf("audit row %d: actor_user_id is NULL, want %d", i, want)
			continue
		}
		if r.ActorUserID.Int64 != want {
			t.Errorf("audit row %d: actor_user_id=%d, want %d", i, r.ActorUserID.Int64, want)
		}
	}
}

// --- single-row handlers ---

func TestAudit_CreateTransaction_WritesInsertRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"date":"2026-04-06","amount":42.00,"description":"Lunch","category_id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	decodeResponse(t, rec, &created)
	txnID := int64(created["id"].(float64))

	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	r := rows[0]
	if r.Action != database.AuditInsert {
		t.Errorf("action=%q, want %q", r.Action, database.AuditInsert)
	}
	if r.TransactionID != txnID {
		t.Errorf("transaction_id=%d, want %d", r.TransactionID, txnID)
	}
	if r.BeforeRaw != "" {
		t.Errorf("before_json should be NULL on insert, got %q", r.BeforeRaw)
	}
	if r.After == nil {
		t.Fatalf("after_json missing on insert row")
	}
	if want := "Lunch"; r.After["description"] != want {
		t.Errorf("after_json.description=%v, want %q", r.After["description"], want)
	}
	assertAllActorsEqual(t, rows, user.ID)
}

func TestAudit_UpdateTransaction_WritesUpdateRowWithBeforeAndAfter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	txn := seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "original")

	body := strings.NewReader(`{"date":"2026-04-01","amount":25.00,"description":"updated","category_id":1}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/"+fmt.Sprint(txn.ID), body)
	req = withUserAndURLParam(req, user, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handleUpdateTransaction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	r := rows[0]
	if r.Action != database.AuditUpdate {
		t.Errorf("action=%q, want %q", r.Action, database.AuditUpdate)
	}
	if r.TransactionID != txn.ID {
		t.Errorf("transaction_id=%d, want %d", r.TransactionID, txn.ID)
	}
	if r.Before == nil || r.After == nil {
		t.Fatalf("update row must carry both before_json and after_json")
	}
	if r.Before["description"] != "original" {
		t.Errorf("before.description=%v, want %q", r.Before["description"], "original")
	}
	if r.After["description"] != "updated" {
		t.Errorf("after.description=%v, want %q", r.After["description"], "updated")
	}
	// Phase 3.1b: the audit before/after JSON marshals the database.Transaction
	// struct, whose monetary field is amount_cents (int64) since the legacy
	// REAL amount column was dropped in migration 010. The before row was
	// seeded at $10.00 (1000 cents) and the update set $25.00 (2500 cents).
	if r.Before["amount_cents"].(float64) != 1000 {
		t.Errorf("before.amount_cents=%v, want 1000", r.Before["amount_cents"])
	}
	if r.After["amount_cents"].(float64) != 2500 {
		t.Errorf("after.amount_cents=%v, want 2500", r.After["amount_cents"])
	}
	assertAllActorsEqual(t, rows, user.ID)
}

func TestAudit_DeleteTransaction_WritesBeforeAndTombstoneAfter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	txn := seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 15.5, "soon-to-be-gone")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(txn.ID), nil)
	req = withUserAndURLParam(req, user, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handleDeleteTransaction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	r := rows[0]
	if r.Action != database.AuditDelete {
		t.Errorf("action=%q, want %q", r.Action, database.AuditDelete)
	}
	if r.TransactionID != txn.ID {
		t.Errorf("transaction_id=%d, want %d", r.TransactionID, txn.ID)
	}
	if r.Before == nil {
		t.Fatalf("delete row must carry before_json")
	}
	if r.Before["description"] != "soon-to-be-gone" {
		t.Errorf("before.description=%v", r.Before["description"])
	}
	// Post-Phase-2.1: soft-delete writes a before/after pair. before_json
	// is the live row (deleted_at NULL), after_json is the tombstoned row
	// with the same payload plus deleted_at populated. Forensic readers
	// can diff the two to confirm the soft-delete only changed deleted_at
	// and updated_at.
	if r.After == nil {
		t.Fatalf("delete row must carry after_json post Phase 2.1 (tombstoned row)")
	}
	if r.After["description"] != "soon-to-be-gone" {
		t.Errorf("after.description=%v (tombstone should preserve payload)", r.After["description"])
	}
	// deleted_at on the before snapshot must be NULL; on the after snapshot
	// it must be a non-empty timestamp. sqlc's GetTransactionByIDRow tags
	// the field json:"deleted_at" (snake_case), and the json package encodes
	// sql.NullTime as {"Time":"...","Valid":false}, so we read
	// r.Before["deleted_at"]["Valid"].
	if beforeDel, ok := r.Before["deleted_at"].(map[string]any); ok {
		if v, _ := beforeDel["Valid"].(bool); v {
			t.Errorf("before.deleted_at.Valid=true, want false")
		}
	} else {
		t.Errorf("before.deleted_at missing or wrong shape: %v", r.Before["deleted_at"])
	}
	if afterDel, ok := r.After["deleted_at"].(map[string]any); ok {
		if v, _ := afterDel["Valid"].(bool); !v {
			t.Errorf("after.deleted_at.Valid=false, want true (tombstone)")
		}
	} else {
		t.Errorf("after.deleted_at missing or wrong shape: %v", r.After["deleted_at"])
	}
	assertAllActorsEqual(t, rows, user.ID)
}

// --- batch handlers ---

func TestAudit_BatchCreate_WritesOneInsertRowPerItem(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`[
		{"date":"2026-04-01","amount":10,"description":"a","category_id":1},
		{"date":"2026-04-02","amount":20,"description":"b","category_id":1},
		{"date":"2026-04-03","amount":30,"description":"c","category_id":1}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("batch create: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("expected 3 audit rows (one per batch item), got %d", len(rows))
	}
	wantDescs := []string{"a", "b", "c"}
	for i, r := range rows {
		if r.Action != database.AuditInsert {
			t.Errorf("row %d action=%q, want insert", i, r.Action)
		}
		if r.TransactionID == database.BulkAuditTransactionID {
			t.Errorf("row %d has bulk sentinel id — batch create must write per-row rows, not a summary", i)
		}
		if r.After == nil || r.After["description"] != wantDescs[i] {
			t.Errorf("row %d description=%v, want %q", i, r.After["description"], wantDescs[i])
		}
	}
	assertAllActorsEqual(t, rows, user.ID)
}

func TestAudit_BatchCreate_ValidationFailure_WritesNoRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Second item missing description — validation runs before BeginTx, so
	// no audit row should ever be written.
	body := strings.NewReader(`[
		{"date":"2026-04-01","amount":10,"description":"ok","category_id":1},
		{"date":"2026-04-02","amount":20,"description":"","category_id":1}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)
	if rec.Code == http.StatusCreated {
		t.Fatalf("expected 4xx, got 201")
	}

	if n := countAuditRows(t, db); n != 0 {
		t.Errorf("expected 0 audit rows after rolled-back batch, got %d", n)
	}
}

func TestAudit_BatchCreate_CurrencyFailureMidBatch_RollsBackAuditRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Item 1 is a plain USD row that would normally write one audit row;
	// item 2 names a currency we never seeded, which pushes resolveCurrency
	// into "unknown currency" mid-batch. The tx MUST roll back — if the
	// audit row from item 1 remained, we'd have a data-less insert row in
	// the table, violating the "audit row iff data row" invariant.
	body := strings.NewReader(`[
		{"date":"2026-04-01","amount":10,"description":"ok","category_id":1},
		{"date":"2026-04-02","original_amount":50,"original_currency":"ZZZ","description":"bad","category_id":1}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)
	if rec.Code == http.StatusCreated {
		t.Fatalf("expected failure status, got 201")
	}

	if n := countAuditRows(t, db); n != 0 {
		t.Errorf("expected 0 audit rows after mid-batch rollback, got %d", n)
	}

	// And the data rows must also be absent.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 transactions after rollback, got %d", count)
	}
}

func TestAudit_BatchDelete_WritesOneDeleteRowPerID(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	t1 := seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "a")
	t2 := seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "b")
	t3 := seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "c")

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d,%d]}`, t1.ID, t2.ID, t3.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-delete", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchDeleteTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch delete: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("expected 3 audit rows, got %d", len(rows))
	}
	gotIDs := map[int64]bool{}
	for _, r := range rows {
		if r.Action != database.AuditDelete {
			t.Errorf("action=%q, want delete", r.Action)
		}
		if r.TransactionID == database.BulkAuditTransactionID {
			t.Errorf("batch delete must write per-row rows, not a summary")
		}
		// Post-Phase-2.1 each per-row delete audit is a before/after pair
		// where before_json is the live row and after_json is the
		// tombstoned row.
		if r.Before == nil {
			t.Errorf("row %d missing before_json", r.TransactionID)
		}
		if r.After == nil {
			t.Errorf("row %d missing after_json (expected tombstoned row)", r.TransactionID)
			continue
		}
		if afterDel, ok := r.After["deleted_at"].(map[string]any); ok {
			if v, _ := afterDel["Valid"].(bool); !v {
				t.Errorf("row %d after.deleted_at.Valid=false, want true", r.TransactionID)
			}
		} else {
			t.Errorf("row %d after.deleted_at missing/wrong shape: %v", r.TransactionID, r.After["deleted_at"])
		}
		gotIDs[r.TransactionID] = true
	}
	for _, id := range []int64{t1.ID, t2.ID, t3.ID} {
		if !gotIDs[id] {
			t.Errorf("missing audit row for deleted transaction id=%d", id)
		}
	}
	// Physical rows must survive as tombstones.
	if got := countAllTransactions(t, db); got != 3 {
		t.Errorf("expected 3 tombstoned rows to survive, got %d physical rows", got)
	}
	if got := countTransactions(t, db); got != 0 {
		t.Errorf("expected 0 live rows after batch soft-delete, got %d", got)
	}
	assertAllActorsEqual(t, rows, user.ID)
}

// --- bulk handlers ---

func TestAudit_BulkRename_WritesSingleSummaryRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "mr brown coffee")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "mr brown bakery")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "starbucks")

	body := strings.NewReader(`{"search":"mr brown","new_description":"MR BROWN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBulkRename(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk rename: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 summary audit row, got %d", len(rows))
	}
	r := rows[0]
	if r.Action != database.AuditUpdate {
		t.Errorf("action=%q, want update", r.Action)
	}
	if r.TransactionID != database.BulkAuditTransactionID {
		t.Errorf("transaction_id=%d, want BulkAuditTransactionID (%d)", r.TransactionID, database.BulkAuditTransactionID)
	}
	if r.Before == nil {
		t.Fatalf("summary row should carry before_json with bulk metadata")
	}
	if r.Before["bulk"] != true {
		t.Errorf("before.bulk=%v, want true", r.Before["bulk"])
	}
	if r.Before["count"].(float64) != 2 {
		t.Errorf("before.count=%v, want 2", r.Before["count"])
	}
	if filter, _ := r.Before["filter"].(string); !strings.Contains(filter, "mr brown") || !strings.Contains(filter, "MR BROWN") {
		t.Errorf("before.filter=%q, want it to name both search and new_description", filter)
	}
	assertAllActorsEqual(t, rows, user.ID)
}

func TestAudit_BulkRename_ZeroMatches_StillWritesSummaryRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "starbucks")

	body := strings.NewReader(`{"search":"nonexistent","new_description":"ignored"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBulkRename(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk rename: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Even a zero-match bulk rename is operator-visible: we want the
	// audit log to record "Alice ran a bulk rename against <filter> and
	// nothing matched", not silently drop the event.
	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row even on zero match, got %d", len(rows))
	}
	if rows[0].Before["count"].(float64) != 0 {
		t.Errorf("before.count=%v, want 0", rows[0].Before["count"])
	}
}

func TestAudit_DeleteByFilter_WritesSingleSummaryRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	// Dates are chosen so that date_to=2026-04-05 brackets 04-01/04-02 but
	// excludes 04-10 under the lex comparison the sibling filter tests
	// already work around. We still don't hardcode the expected delete
	// count below — see the snapshot logic after the handler call.
	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "a")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "b")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-10", 30.0, "c")

	// Snapshot the LIVE row count BEFORE the handler runs so the audit
	// assertion can compare against the real SQL delta rather than a
	// hardcoded `2`. handleDeleteTransactionsByFilter resolves its WHERE
	// clause through buildTransactionWhereClause, which relies on
	// lexicographic DATETIME comparison against the go-sqlite3 TEXT
	// layout ("2026-04-05 00:00:00+00:00"). Pinning the expected count to
	// a literal 2 would break silently on any change to how the driver
	// serialises time.Time. Deriving it from the live-row delta asserts
	// the real Phase 4.2 invariant — "the audit summary's count equals
	// whatever the filter actually removed from live view" — robust to
	// SQL plumbing changes underneath.
	//
	// Post-Phase-2.1: this must count LIVE rows, not all physical rows.
	// handleDeleteTransactionsByFilter now soft-deletes, so the physical
	// row count is unchanged; only deleted_at flips.
	txBefore := countTransactions(t, db)

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions?date_from=2026-04-01&date_to=2026-04-05", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDeleteTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete by filter: status=%d body=%s", rec.Code, rec.Body.String())
	}

	txAfter := countTransactions(t, db)
	wantDeleted := txBefore - txAfter
	// Guard against the "filter matched nothing" degenerate case: if
	// wantDeleted is zero the test below would accept a count-of-zero
	// summary row as "correct", completely hiding any regression in the
	// filter semantics. Fail loudly so a future SQL/driver change that
	// breaks the lex comparison shows up here instead of a silent pass.
	if wantDeleted <= 0 {
		t.Fatalf("handler soft-deleted %d rows, expected > 0 (seeded 3 with filter bracketing 2)", wantDeleted)
	}
	// Belt-and-suspenders: physical rows (including tombstones) must be
	// unchanged. This guards against a regression where
	// handleDeleteTransactionsByFilter accidentally reverts to a hard
	// DELETE — wantDeleted would still be > 0 but the tombstoned audit
	// log would be unrecoverable because the target rows are gone.
	if got := countAllTransactions(t, db); got != 3 {
		t.Errorf("expected 3 physical rows post-soft-delete, got %d", got)
	}
	if got := countTombstonedTransactions(t, db); got != wantDeleted {
		t.Errorf("expected %d tombstoned rows, got %d", wantDeleted, got)
	}

	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 summary audit row, got %d", len(rows))
	}
	r := rows[0]
	if r.Action != database.AuditDelete {
		t.Errorf("action=%q, want delete", r.Action)
	}
	if r.TransactionID != database.BulkAuditTransactionID {
		t.Errorf("transaction_id=%d, want BulkAuditTransactionID", r.TransactionID)
	}
	if got, _ := r.Before["count"].(float64); int(got) != wantDeleted {
		t.Errorf("before.count=%v, want %d (derived from actual row delta)", got, wantDeleted)
	}
	if filter, _ := r.Before["filter"].(string); !strings.Contains(filter, "date_from=2026-04-01") {
		t.Errorf("before.filter=%q should include the original query string", filter)
	}
	assertAllActorsEqual(t, rows, user.ID)
}

// --- actor tracking ---

func TestAudit_ActorIDMatchesAuthenticatedUser(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "admin")

	// Alice creates. Bob deletes. The two audit rows must carry distinct
	// actor_user_id values matching whoever authenticated the mutation.
	body := strings.NewReader(`{"date":"2026-04-01","amount":10,"description":"alice's row","category_id":1}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	createReq = withUser(createReq, alice)
	createRec := httptest.NewRecorder()
	h.handleCreateTransaction(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", createRec.Code, createRec.Body.String())
	}
	var created map[string]any
	decodeResponse(t, createRec, &created)
	txnID := int64(created["id"].(float64))

	delReq := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(txnID), nil)
	delReq = withUserAndURLParam(delReq, bob, "id", fmt.Sprint(txnID))
	delRec := httptest.NewRecorder()
	h.handleDeleteTransaction(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", delRec.Code, delRec.Body.String())
	}

	rows := listAuditRows(t, db)
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit rows (insert by alice, delete by bob), got %d", len(rows))
	}
	if !rows[0].ActorUserID.Valid || rows[0].ActorUserID.Int64 != alice.ID {
		t.Errorf("insert row actor=%v, want %d (alice)", rows[0].ActorUserID, alice.ID)
	}
	if !rows[1].ActorUserID.Valid || rows[1].ActorUserID.Int64 != bob.ID {
		t.Errorf("delete row actor=%v, want %d (bob)", rows[1].ActorUserID, bob.ID)
	}
}

// --- direct store smoke test ---
// Exercising TransactionStore.Create outside of a handler catches bugs in
// the chokepoint itself — e.g. a future refactor that accidentally
// decouples the audit insert from the data insert would still pass the
// handler tests (which go through HTTP) if the decoupling happens to fall
// on a code path the HTTP layer doesn't exercise.

func TestAudit_StoreCreate_CoCommitsDataAndAudit(t *testing.T) {
	q, db := setupTestDB(t)
	user := seedTestUser(t, q, "alice", "member")
	store := database.NewTransactionStore(db, q)

	created, err := store.Create(context.Background(), user.ID, database.CreateTransactionParams{
		UserID:      user.ID,
		Date:        mustParseDate(t, "2026-04-01"),
		AmountCents: dollarsToCents(7.50),
		Description: "store direct",
		CategoryID:  1,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	// Data row committed.
	var desc string
	if err := db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, created.ID).Scan(&desc); err != nil {
		t.Fatalf("load data row: %v", err)
	}
	if desc != "store direct" {
		t.Errorf("description=%q, want %q", desc, "store direct")
	}

	// Audit row committed.
	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	if rows[0].TransactionID != created.ID {
		t.Errorf("audit txn id=%d, want %d", rows[0].TransactionID, created.ID)
	}
	if rows[0].Action != database.AuditInsert {
		t.Errorf("action=%q, want insert", rows[0].Action)
	}
}

// --- Bulk-edit Task 3 audit invariants -------------------------------------

func TestAudit_BatchUpdate_WritesUpdateRowPerID_WithBeforeAndAfter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	catA := seedTestCategory(t, q, "A", "expense")
	catB := seedTestCategory(t, q, "B", "expense")
	t1 := seedTestTransaction(t, q, user.ID, catA.ID, "2026-04-01", 10.0, "X")
	t2 := seedTestTransaction(t, q, user.ID, catA.ID, "2026-04-02", 10.0, "Y")

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d],"patch":{"category_id":%d}}`, t1.ID, t2.ID, catB.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-update", body)
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchUpdateTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch update: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	var perRow []auditRowForAssert
	for _, r := range rows {
		if r.Action == database.AuditUpdate && r.TransactionID != database.BulkAuditTransactionID {
			perRow = append(perRow, r)
		}
	}
	if len(perRow) != 2 {
		t.Fatalf("expected 2 per-row update audit rows, got %d (all rows: %d)", len(perRow), len(rows))
	}
	for _, r := range perRow {
		if r.Before == nil {
			t.Errorf("audit row id=%d missing before_json", r.TransactionID)
		}
		if r.After == nil {
			t.Errorf("audit row id=%d missing after_json", r.TransactionID)
		}
	}
	assertAllActorsEqual(t, perRow, user.ID)
}

func TestAudit_BatchUpdate_WithSkips_WritesSummaryRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	t1 := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "X")

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,99999],"patch":{"category_id":%d}}`, t1.ID, cat.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-update", body)
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchUpdateTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch update: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	var summary []auditRowForAssert
	for _, r := range rows {
		if r.TransactionID == database.BulkAuditTransactionID && r.Action == database.AuditUpdate {
			summary = append(summary, r)
		}
	}
	if len(summary) != 1 {
		t.Fatalf("expected 1 summary row, got %d (all rows: %d)", len(summary), len(rows))
	}
	filter, _ := summary[0].Before["filter"].(string)
	if !strings.Contains(filter, "skipped_during_batch_update:1_of_2") {
		t.Errorf("summary filter: %q, want it to contain skipped_during_batch_update:1_of_2", filter)
	}
	// scope= distinguishes a member hitting the ownership wall from an admin
	// hitting only tombstones, now that admins bypass ownership. Mirrors the
	// scope marker batch-delete and bulk-rename already record.
	if !strings.Contains(filter, "scope=own") {
		t.Errorf("summary filter: %q, want it to contain scope=own for a member actor", filter)
	}
}

// TestAudit_BatchUpdate_OnRollback_LeavesNoOrphanRows triggers a mid-batch
// FK violation (category 99999 doesn't exist) and asserts the entire
// transaction rolls back: no data rows are mutated and no audit rows
// (per-row or summary) are committed.
//
// PRECONDITION: SQLite's `PRAGMA foreign_keys = ON` must be enabled on the
// connection — without it, an UPDATE that points at a nonexistent
// category silently succeeds (orphan FK), the test passes for the wrong
// reason, and the rollback invariant is unenforced. The api package's
// setupTestDB DSN does NOT include `_foreign_keys=on`, so this test
// enables FKs explicitly via PRAGMA, then verifies the readback. This
// matches the pattern already established by
// TestHandleDeleteCategory_WithTransactions_Returns409 in
// category_handlers_test.go.
func TestAudit_BatchUpdate_OnRollback_LeavesNoOrphanRows(t *testing.T) {
	q, db := setupTestDB(t)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	var fkOn int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fkOn); err != nil || fkOn != 1 {
		t.Fatalf("FK enforcement is OFF (got %d) — rollback test would pass for the wrong reason", fkOn)
	}
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	t1 := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "X")
	t2 := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-02", 10.0, "Y")

	auditsBefore := countAuditRows(t, db)
	var c1Before, c2Before int64
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, t1.ID).Scan(&c1Before)
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, t2.ID).Scan(&c2Before)

	// Poisoned patch: category 99999 doesn't exist → FK violation on UPDATE.
	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d],"patch":{"category_id":99999}}`, t1.ID, t2.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-update", body)
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchUpdateTransactions(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-200 on FK violation, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Both rows must be unchanged.
	var c1After, c2After int64
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, t1.ID).Scan(&c1After)
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, t2.ID).Scan(&c2After)
	if c1After != c1Before {
		t.Errorf("id=%d mutated despite rollback: was %d, got %d", t1.ID, c1Before, c1After)
	}
	if c2After != c2Before {
		t.Errorf("id=%d mutated despite rollback: was %d, got %d", t2.ID, c2Before, c2After)
	}

	// No new audit rows committed.
	auditsAfter := countAuditRows(t, db)
	if auditsAfter != auditsBefore {
		t.Errorf("expected zero new audit rows after rollback, got %d new (before=%d after=%d)",
			auditsAfter-auditsBefore, auditsBefore, auditsAfter)
	}
}

// --- Bulk-edit Task 4 audit invariants -------------------------------------

// TestAudit_UpdateByFilter_WritesOneSummaryRow_WithFilterAndPatch pins spec
// §5.4: update-by-filter emits exactly one summary audit row with
// transaction_id = BulkAuditTransactionID, action='update', and a
// before_json filter string that captures BOTH the original querystring
// and the patch summary (so an operator replaying the audit log can
// reconstruct the user's intent without needing a separate column).
func TestAudit_UpdateByFilter_WritesOneSummaryRow_WithFilterAndPatch(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	catB := seedTestCategory(t, q, "CatB", "expense")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "X")

	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, catB.ID)
	url := fmt.Sprintf("/api/transactions/update-by-filter?category_id=%d", cat.ID)
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update-by-filter: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	var summaries []auditRowForAssert
	for _, a := range rows {
		if a.TransactionID == database.BulkAuditTransactionID && a.Action == database.AuditUpdate {
			summaries = append(summaries, a)
		}
	}
	if len(summaries) != 1 {
		t.Fatalf("expected exactly 1 summary row, got %d (all rows: %d)", len(summaries), len(rows))
	}
	r := summaries[0]
	if r.Before == nil {
		t.Fatalf("summary row should carry before_json with bulk metadata")
	}
	if r.Before["bulk"] != true {
		t.Errorf("before.bulk=%v, want true", r.Before["bulk"])
	}
	filter, _ := r.Before["filter"].(string)
	if !strings.Contains(filter, fmt.Sprintf("category_id=%d", cat.ID)) {
		t.Errorf("before.filter=%q should include the original querystring (category_id=%d)", filter, cat.ID)
	}
	if !strings.Contains(filter, fmt.Sprintf("category_id=%d", catB.ID)) {
		t.Errorf("before.filter=%q should include the patch summary (category_id=%d)", filter, catB.ID)
	}
	assertAllActorsEqual(t, summaries, user.ID)
}

// TestAudit_UpdateByFilter_ZeroMatches_StillEmitsSummary pins spec §5.4:
// the summary row is emitted **unconditionally**, even when the filter
// matched zero rows. The audit table records the user's *attempt* against
// filter X, not just successful work — same precedent as
// handleBulkRename's zero-match behaviour and handleDeleteTransactionsByFilter.
func TestAudit_UpdateByFilter_ZeroMatches_StillEmitsSummary(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	catB := seedTestCategory(t, q, "CatB", "expense")
	// No transactions seeded — filter matches zero rows.

	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, catB.ID)
	url := "/api/transactions/update-by-filter?search=nomatch"
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update-by-filter: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	var count int
	for _, a := range rows {
		if a.TransactionID == database.BulkAuditTransactionID && a.Action == database.AuditUpdate {
			count++
		}
	}
	if count != 1 {
		t.Errorf("zero-match should still emit summary; got %d summary audit rows (all rows: %d)", count, len(rows))
	}
	// And the count in the summary must be zero, not the seed count.
	for _, a := range rows {
		if a.TransactionID == database.BulkAuditTransactionID && a.Action == database.AuditUpdate {
			if got := a.Before["count"].(float64); got != 0 {
				t.Errorf("zero-match summary count=%v, want 0", got)
			}
		}
	}
}
