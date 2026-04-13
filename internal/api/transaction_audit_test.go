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
	if r.Before["amount"].(float64) != 10.0 {
		t.Errorf("before.amount=%v, want 10", r.Before["amount"])
	}
	if r.After["amount"].(float64) != 25.0 {
		t.Errorf("after.amount=%v, want 25", r.After["amount"])
	}
	assertAllActorsEqual(t, rows, user.ID)
}

func TestAudit_DeleteTransaction_WritesDeleteRowWithBeforeOnly(t *testing.T) {
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
	// after_json is NULL pre-Tier-2 (hard delete). The AfterRaw check
	// below locks the contract in: if a future change accidentally writes
	// after_json on a hard delete, the test catches it.
	if r.AfterRaw != "" {
		t.Errorf("after_json should be NULL on pre-Tier-2 delete, got %q", r.AfterRaw)
	}
	if r.Before["description"] != "soon-to-be-gone" {
		t.Errorf("before.description=%v", r.Before["description"])
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
		gotIDs[r.TransactionID] = true
	}
	for _, id := range []int64{t1.ID, t2.ID, t3.ID} {
		if !gotIDs[id] {
			t.Errorf("missing audit row for deleted transaction id=%d", id)
		}
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

	// Snapshot the transaction row count BEFORE the handler runs so the
	// audit assertion can compare against the real SQL delta rather than
	// a hardcoded `2`. handleDeleteTransactionsByFilter resolves its
	// WHERE clause through buildTransactionWhereClause, which relies on
	// lexicographic DATETIME comparison against the go-sqlite3 TEXT
	// layout ("2026-04-05 00:00:00+00:00"). Pinning the expected count
	// to a literal 2 would break silently on any change to how the
	// driver serialises time.Time. Deriving it from the row delta
	// asserts the real Phase 4.2 invariant: "the audit summary's count
	// equals whatever the filter actually deleted" — robust to SQL
	// plumbing changes underneath.
	var txBefore int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txBefore); err != nil {
		t.Fatalf("count transactions before: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions?date_from=2026-04-01&date_to=2026-04-05", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDeleteTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete by filter: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var txAfter int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txAfter); err != nil {
		t.Fatalf("count transactions after: %v", err)
	}
	wantDeleted := txBefore - txAfter
	// Guard against the "filter matched nothing" degenerate case: if
	// wantDeleted is zero the test below would accept a count-of-zero
	// summary row as "correct", completely hiding any regression in the
	// filter semantics. Fail loudly so a future SQL/driver change that
	// breaks the lex comparison shows up here instead of a silent pass.
	if wantDeleted <= 0 {
		t.Fatalf("handler deleted %d rows, expected > 0 (seeded 3 with filter bracketing 2)", wantDeleted)
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
		Amount:      7.50,
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
