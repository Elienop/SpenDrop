package api

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/elienop/spendrop/internal/database"
)

// B6i widened ?search= from description alone to description + notes +
// category name + the foreign (original) amount.
//
// The predicate lives in ONE place — appendSearchCondition, called by
// buildTransactionWhereClause — and that is load-bearing rather than tidy.
// GET /transactions, update-by-filter and delete-by-filter all serialise the
// same clause, so the count the confirm dialog shows and the rows the write
// touches cannot drift. Every arm below is therefore asserted TWICE: once
// through the list endpoint and once through update-by-filter, with the same
// search term. A fork in the predicate fails the pair, not just one side.

// seedSearchRow seeds a transaction with notes and optional foreign money, the
// two column families the existing seeders do not reach.
func seedSearchRow(
	t *testing.T,
	q *database.Queries,
	userID, categoryID int64,
	date, desc, notes string,
	amount float64,
	origAmount float64,
	origCurrency string,
) database.Transaction {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse seed date %q: %v", date, err)
	}
	params := database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: dollarsToCents(amount),
		Description: desc,
		CategoryID:  categoryID,
		Notes:       sql.NullString{String: notes, Valid: notes != ""},
	}
	if origCurrency != "" {
		params.OriginalAmountCents = sql.NullInt64{Int64: dollarsToCents(origAmount), Valid: true}
		params.OriginalCurrency = sql.NullString{String: origCurrency, Valid: true}
	}
	txn, err := q.CreateTransaction(context.Background(), params)
	if err != nil {
		t.Fatalf("seed search row %q: %v", desc, err)
	}
	return txn
}

// listSearchIDs runs GET /transactions?search=<term> and returns the reported
// total plus the ids actually rendered. Decoding into map[string]any rather
// than a typed struct keeps the assertion honest about what crossed the wire.
func listSearchIDs(t *testing.T, h *Handler, user database.User, term string, extra map[string]string) (int, []int64) {
	t.Helper()
	url := "/api/transactions?search=" + urlQueryEscape(term)
	for k, v := range extra {
		url += "&" + k + "=" + urlQueryEscape(v)
	}
	req := withUser(httptest.NewRequest(http.MethodGet, url, nil), user)
	rec := httptest.NewRecorder()
	h.handleListTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list search %q: expected 200, got %d; body: %s", term, rec.Code, rec.Body.String())
	}

	var body struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	decodeResponse(t, rec, &body)

	ids := make([]int64, 0, len(body.Transactions))
	for _, row := range body.Transactions {
		raw, ok := row["id"].(float64)
		if !ok {
			t.Fatalf("list search %q: row has no numeric id: %#v", term, row)
		}
		ids = append(ids, int64(raw))
	}
	if body.Total != len(ids) {
		t.Fatalf("list search %q: total=%d but %d rows returned — the count and data queries disagree",
			term, body.Total, len(ids))
	}
	return body.Total, ids
}

// urlQueryEscape percent-encodes a search term for the querystring. Written
// out rather than pulled from net/url so the tests read the same way the
// browser builds the request.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~':
			b.WriteByte(r)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", r))
		}
	}
	return b.String()
}

// updateByFilterSearchCount runs POST /api/transactions/update-by-filter with
// the same search term and returns the number of rows the write actually
// touched. The patch sets a description sentinel: it moves no date, category or
// tag, so it cannot disturb the checkpoint or budget-alert machinery the
// assertion is not about.
//
// The method and path match router.go, even though the handler is invoked
// directly here and would accept anything: a fixture that describes a route
// which does not exist is a false landmark for the next reader.
func updateByFilterSearchCount(t *testing.T, h *Handler, user database.User, term string) int64 {
	t.Helper()
	body := strings.NewReader(`{"patch":{"description":"bulk-edited-by-test"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/update-by-filter?search="+urlQueryEscape(term), body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update-by-filter %q: expected 200, got %d; body: %s", term, rec.Code, rec.Body.String())
	}
	var resp map[string]int64
	decodeResponse(t, rec, &resp)
	return resp["updated"]
}

// assertSearchLockstep is the B4 invariant: whatever the list search matches,
// the bulk write scope matches identically. It reads the list first (the write
// rewrites descriptions), then runs the write and compares counts.
func assertSearchLockstep(t *testing.T, h *Handler, user database.User, term string, wantIDs []int64) {
	t.Helper()
	total, ids := listSearchIDs(t, h, user, term, nil)
	assertIDSet(t, term, ids, wantIDs)
	if total != len(wantIDs) {
		t.Errorf("list search %q: total = %d, want %d", term, total, len(wantIDs))
	}
	if updated := updateByFilterSearchCount(t, h, user, term); updated != int64(len(wantIDs)) {
		t.Errorf("update-by-filter %q updated %d rows but the list matched %d — the shared predicate has forked",
			term, updated, len(wantIDs))
	}
}

func assertIDSet(t *testing.T, term string, got, want []int64) {
	t.Helper()
	gotSet := make(map[int64]bool, len(got))
	for _, id := range got {
		gotSet[id] = true
	}
	for _, id := range want {
		if !gotSet[id] {
			t.Errorf("search %q: expected row %d in the results, got %v", term, id, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("search %q: matched %v, want exactly %v", term, got, want)
	}
}

// --- notes arm ---

func TestSearchScope_NotesArm_ListAndFilterAgree(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	catID := seedExpenseCategory(t, h.queries, "Pharmacy-"+t.Name())

	match := seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-01",
		"Card payment", "reimbursed by insurance", 40, 0, "")
	// Control: the term appears in neither its description nor its notes.
	seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-02",
		"Card payment", "paid in cash", 25, 0, "")

	assertSearchLockstep(t, h, admin, "insurance", []int64{match.ID})
}

// --- category name arm ---

func TestSearchScope_CategoryNameArm_ListAndFilterAgree(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	osteo := seedExpenseCategory(t, h.queries, "Osteopathy")
	bakery := seedExpenseCategory(t, h.queries, "Bakery")

	// Neither description nor notes mentions the category, so only the
	// category-name arm can match this row.
	match := seedSearchRow(t, h.queries, admin.ID, osteo, "2026-03-01",
		"Session", "monthly", 60, 0, "")
	seedSearchRow(t, h.queries, admin.ID, bakery, "2026-03-02",
		"Croissant", "morning", 3, 0, "")

	assertSearchLockstep(t, h, admin, "Osteopathy", []int64{match.ID})
}

// --- foreign amount arm ---

func TestSearchScope_ForeignAmountArm_ListAndFilterAgree(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	// The LBP row the user is hunting for by typing its foreign figure.
	match := seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-01",
		"Supermarket", "weekly shop", 16.85, 1_500_000, "LBP")
	// A different foreign amount — must not match.
	seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-02",
		"Supermarket", "top-up", 22.47, 2_000_000, "LBP")
	// A BASE-currency row carrying the same figure as its base amount. The
	// arm reads original_amount_cents, which is NULL here, so this row must
	// stay out no matter how the digits line up.
	seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-03",
		"Property", "base currency row", 1_500_000, 0, "")

	assertSearchLockstep(t, h, admin, "1500000", []int64{match.ID})
}

// TestSearchScope_ForeignAmountArm_MatchesExactlyNotSubstring pins the
// documented matching rule. "1500" must find a row of exactly 1,500 and must
// NOT find one of 1,500,000: a substring match over the digits would make
// every bulk delete filtered by a short number sweep up unrelated rows.
func TestSearchScope_ForeignAmountArm_MatchesExactlyNotSubstring(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	exact := seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-01",
		"Kiosk", "", 0.02, 1_500, "LBP")
	seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-02",
		"Supermarket", "", 16.85, 1_500_000, "LBP")

	assertSearchLockstep(t, h, admin, "1500", []int64{exact.ID})
}

// TestSearchScope_ForeignAmountArm_AcceptsThousandsSeparators covers the other
// half of the documented rule: a user who types the figure the way the UI
// renders it finds the same row.
func TestSearchScope_ForeignAmountArm_AcceptsThousandsSeparators(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	match := seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-01",
		"Supermarket", "", 16.85, 1_500_000, "LBP")
	seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-02",
		"Supermarket", "", 22.47, 2_000_000, "LBP")

	assertSearchLockstep(t, h, admin, "1,500,000", []int64{match.ID})
}

// --- tags stay out of scope ---

// TestSearchScope_DoesNotMatchTags pins the deliberate exclusion. Tags have
// their own ?tags= filter; folding them into ?search= would make one control
// silently widen the other with no way to ask for the narrow behaviour back.
func TestSearchScope_DoesNotMatchTags(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	seedTestTransactionWithTags(t, h.queries, admin.ID, catID,
		"2026-03-01", 12, "Supermarket", "reimbursable")

	total, ids := listSearchIDs(t, h, admin, "reimbursable", nil)
	if total != 0 || len(ids) != 0 {
		t.Errorf("search must not match tags: total = %d, ids = %v", total, ids)
	}

	// The dedicated filter still finds it, so the row really does carry the tag.
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/transactions?tags=reimbursable", nil), admin)
	rec := httptest.NewRecorder()
	h.handleListTransactions(rec, req)
	var body struct {
		Total int `json:"total"`
	}
	decodeResponse(t, rec, &body)
	if body.Total != 1 {
		t.Errorf("?tags= should still match the row: total = %d, want 1", body.Total)
	}
}

// --- LIKE escaping across the new arms ---

// TestSearchScope_NotesArmEscapesWildcards proves the % in a search term stays
// literal on the NOTES arm. Unescaped, "%100%%" matches "100 percent cotton"
// too, so the control row is what makes this test real.
func TestSearchScope_NotesArmEscapesWildcards(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	catID := seedExpenseCategory(t, h.queries, "Shopping-"+t.Name())

	match := seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-01",
		"Shirt", "100% cotton", 30, 0, "")
	seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-02",
		"Sheets", "100 percent cotton", 45, 0, "")

	assertSearchLockstep(t, h, admin, "100%", []int64{match.ID})
}

// TestSearchScope_CategoryNameArmEscapesUnderscore is the same guarantee on the
// category-name arm with the other LIKE metacharacter: unescaped, "_" matches
// any single character and would pull in the control category too.
func TestSearchScope_CategoryNameArmEscapesUnderscore(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	literal := seedExpenseCategory(t, h.queries, "Cash_Only")
	wildcard := seedExpenseCategory(t, h.queries, "CashXOnly")

	match := seedSearchRow(t, h.queries, admin.ID, literal, "2026-03-01",
		"Market", "", 10, 0, "")
	seedSearchRow(t, h.queries, admin.ID, wildcard, "2026-03-02",
		"Market", "", 11, 0, "")

	assertSearchLockstep(t, h, admin, "Cash_Only", []int64{match.ID})
}

// --- composition with the other filters ---

// TestSearchScope_ComposesWithDateRange pins the parentheses around the OR
// group. Without them the first OR rebinds the whole WHERE, and a search
// combined with a date range starts returning rows outside the range.
func TestSearchScope_ComposesWithDateRange(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	catID := seedExpenseCategory(t, h.queries, "Osteopathy-"+t.Name())

	inRange := seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-15",
		"Session", "", 60, 0, "")
	// Same category, so it matches the search — but it is outside the window.
	seedSearchRow(t, h.queries, admin.ID, catID, "2025-01-05",
		"Session", "", 60, 0, "")

	total, ids := listSearchIDs(t, h, admin, "Osteopathy-"+t.Name(), map[string]string{
		"date_from": "2026-03-01",
		"date_to":   "2026-03-31",
	})
	if total != 1 {
		t.Errorf("search + date range: total = %d, want 1 (the OR group must be parenthesised)", total)
	}
	assertIDSet(t, "Osteopathy + date range", ids, []int64{inRange.ID})
}

// --- the export consumer ---

// TestSearchScope_ExportHonoursWidenedSearch covers the fourth caller of the
// shared clause. The export builds its own SELECT and only aliases t and c, so
// an arm naming a column it does not select would fail at RUNTIME on search
// requests alone — a shape no other test reaches, because no existing export
// test passes ?search=.
func TestSearchScope_ExportHonoursWidenedSearch(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	osteo := seedExpenseCategory(t, h.queries, "Osteopathy")
	bakery := seedExpenseCategory(t, h.queries, "Bakery")

	seedSearchRow(t, h.queries, admin.ID, osteo, "2026-03-01", "Session", "", 60, 0, "")
	seedSearchRow(t, h.queries, admin.ID, bakery, "2026-03-02", "Croissant", "", 3, 0, "")

	req := withUser(httptest.NewRequest(http.MethodGet,
		"/api/export/transactions?search=Osteopathy", nil), admin)
	rec := httptest.NewRecorder()
	h.handleExportTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export with a search term: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()
	rows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("read Transactions sheet: %v", err)
	}
	// Header row plus exactly the Osteopathy row.
	if len(rows) != 2 {
		t.Fatalf("expected 1 data row, got %d rows (including header): %v", len(rows), rows)
	}
	if !strings.Contains(strings.Join(rows[1], "|"), "Session") {
		t.Errorf("exported row is not the Osteopathy one: %v", rows[1])
	}
}

// --- soft-delete ---

// TestSearchScope_HidesTombstoned is the CLAUDE.md soft-delete invariant
// applied to every arm B6i added. Each tombstoned row carries the search term
// AND a sentinel amount, so a leak shows up both as a row count and as money.
func TestSearchScope_HidesTombstoned(t *testing.T) {
	const sentinel = 999.0

	cases := []struct {
		name string
		term string
		// seed returns the id of the LIVE row that must be found.
		seed func(t *testing.T, h *Handler, userID int64) int64
	}{
		{
			name: "notes",
			term: "insurance",
			seed: func(t *testing.T, h *Handler, userID int64) int64 {
				cat := seedExpenseCategory(t, h.queries, "Pharmacy-notes")
				live := seedSearchRow(t, h.queries, userID, cat, "2026-03-01",
					"Card payment", "reimbursed by insurance", 40, 0, "")
				dead := seedSearchRow(t, h.queries, userID, cat, "2026-03-02",
					"Card payment", "reimbursed by insurance", sentinel, 0, "")
				softDelete(t, h, userID, dead.ID)
				return live.ID
			},
		},
		{
			name: "category name",
			term: "Osteopathy",
			seed: func(t *testing.T, h *Handler, userID int64) int64 {
				cat := seedExpenseCategory(t, h.queries, "Osteopathy")
				live := seedSearchRow(t, h.queries, userID, cat, "2026-03-01",
					"Session", "", 60, 0, "")
				dead := seedSearchRow(t, h.queries, userID, cat, "2026-03-02",
					"Session", "", sentinel, 0, "")
				softDelete(t, h, userID, dead.ID)
				return live.ID
			},
		},
		{
			name: "foreign amount",
			term: "1500000",
			seed: func(t *testing.T, h *Handler, userID int64) int64 {
				cat := seedExpenseCategory(t, h.queries, "Groceries-foreign")
				live := seedSearchRow(t, h.queries, userID, cat, "2026-03-01",
					"Supermarket", "", 16.85, 1_500_000, "LBP")
				dead := seedSearchRow(t, h.queries, userID, cat, "2026-03-02",
					"Supermarket", "", sentinel, 1_500_000, "LBP")
				softDelete(t, h, userID, dead.ID)
				return live.ID
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := setupHandler(t)
			admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
			liveID := tc.seed(t, h, admin.ID)

			total, ids := listSearchIDs(t, h, admin, tc.term, nil)
			if total != 1 {
				t.Errorf("search %q: total = %d, want 1 — a tombstoned row leaked", tc.term, total)
			}
			assertIDSet(t, tc.term, ids, []int64{liveID})

			if updated := updateByFilterSearchCount(t, h, admin, tc.term); updated != 1 {
				t.Errorf("update-by-filter %q updated %d rows, want 1 — a tombstoned row was rewritten",
					tc.term, updated)
			}
		})
	}
}

// softDelete tombstones a row the way the app does, through the store, so the
// test does not depend on a hand-written UPDATE staying in sync with it.
//
// actorID is a parameter rather than a hardcoded 1: the store writes an audit
// row attributed to it, and every caller here happens to seed its admin first
// only by convention. Reordering a fixture would otherwise silently attribute
// the tombstone to a user id that does not exist.
func softDelete(t *testing.T, h *Handler, actorID, id int64) {
	t.Helper()
	if err := h.txnStore.Delete(context.Background(), actorID, id); err != nil {
		t.Fatalf("soft-delete row %d as actor %d: %v", id, actorID, err)
	}
}

// --- delete-by-filter, the third consumer ---

// deleteByFilterSearchCount runs POST /api/transactions/delete-by-filter with
// the same search term and returns how many rows the write tombstoned. Same
// direct-invocation shape as updateByFilterSearchCount; the endpoint takes no
// body — its scope is the querystring filter alone.
func deleteByFilterSearchCount(t *testing.T, h *Handler, user database.User, term string) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/delete-by-filter?search="+urlQueryEscape(term), nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDeleteTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete-by-filter %q: expected 200, got %d; body: %s", term, rec.Code, rec.Body.String())
	}
	var resp map[string]int64
	decodeResponse(t, rec, &resp)
	return resp["deleted"]
}

// assertRowTombstoned checks one row's tombstone state directly — the deleted
// count alone cannot say WHICH row a widened search swept up.
func assertRowTombstoned(t *testing.T, h *Handler, id int64, wantTombstoned bool) {
	t.Helper()
	var deletedAt sql.NullString
	if err := h.db.QueryRowContext(context.Background(),
		"SELECT deleted_at FROM transactions WHERE id = ?", id).Scan(&deletedAt); err != nil {
		t.Fatalf("read row %d: %v", id, err)
	}
	if wantTombstoned && !deletedAt.Valid {
		t.Errorf("row %d survived a delete-by-filter whose search matches it", id)
	}
	if !wantTombstoned && deletedAt.Valid {
		t.Errorf("row %d was tombstoned by a delete-by-filter whose search does not match it", id)
	}
}

// TestSearchScope_DeleteByFilterHonoursWidenedSearch pins the third consumer
// this file's header names. The deep review verified the code path with a
// throwaway probe; without an in-tree assertion, a future fork of
// delete-by-filter's predicate would ship green. Deletion is destructive, so
// each arm seeds its own handler rather than sharing the lockstep fixture.
func TestSearchScope_DeleteByFilterHonoursWidenedSearch(t *testing.T) {
	t.Run("notes_arm", func(t *testing.T) {
		h := setupHandler(t)
		admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
		catID := seedExpenseCategory(t, h.queries, "Pharmacy-"+t.Name())
		match := seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-01",
			"Card payment", "reimbursed by insurance", 40, 0, "")
		control := seedSearchRow(t, h.queries, admin.ID, catID, "2026-03-02",
			"Card payment", "paid in cash", 25, 0, "")

		if deleted := deleteByFilterSearchCount(t, h, admin, "insurance"); deleted != 1 {
			t.Errorf("delete-by-filter %q tombstoned %d rows, want exactly 1 — the shared predicate has forked", "insurance", deleted)
		}
		assertRowTombstoned(t, h, match.ID, true)
		assertRowTombstoned(t, h, control.ID, false)
	})

	t.Run("category_name_arm", func(t *testing.T) {
		h := setupHandler(t)
		admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
		osteo := seedExpenseCategory(t, h.queries, "Osteopathy")
		bakery := seedExpenseCategory(t, h.queries, "Bakery")
		match := seedSearchRow(t, h.queries, admin.ID, osteo, "2026-03-01",
			"Session", "monthly", 60, 0, "")
		control := seedSearchRow(t, h.queries, admin.ID, bakery, "2026-03-02",
			"Croissant", "morning", 3, 0, "")

		if deleted := deleteByFilterSearchCount(t, h, admin, "Osteopathy"); deleted != 1 {
			t.Errorf("delete-by-filter %q tombstoned %d rows, want exactly 1 — the shared predicate has forked", "Osteopathy", deleted)
		}
		assertRowTombstoned(t, h, match.ID, true)
		assertRowTombstoned(t, h, control.ID, false)
	})
}
