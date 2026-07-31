package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// --- The transaction-date window ---
//
// validateDate had no year bound at all for the app's whole life: 0001-01-01,
// 1850-06-01 and 3021-01-02 all parsed and stored, while every report endpoint
// rejected anything outside its year window. The ledger could therefore hold
// rows no report would ever display, silently.
//
// Bounding it is the fix, but a naive bound TRAPS existing data. PUT
// /api/transactions/{id} is a full replace, so the client resends the row's
// own date on every edit — a strict bound would 400 a description-only edit of
// a legacy 3021 row, locking the user out of correcting or even recategorising
// their own data. That is worse than the hole.
//
// Chosen semantics: an out-of-window date is accepted on an update ONLY when
// it is byte-identical to the date already stored on the row being replaced.
// Concretely:
//
//	create with an out-of-window date          -> 400 (the hole is closed)
//	update, date unchanged and out of window   -> allowed (no trap)
//	update, out-of-window -> in-window         -> allowed (correction always works)
//	update, out-of-window -> a DIFFERENT
//	  out-of-window date                       -> 400 (no keeping the hole alive)
//	bulk patch date, out of window             -> 400 (always an explicit new value)
//
// The rule is "the bound applies to the date you are MOVING to". A row already
// outside the window may stay where it is; nothing may be put there afresh.

// dateWindowFixture builds a handler plus a seeded member and category.
func dateWindowFixture(t *testing.T) (*Handler, *database.Queries, database.User, database.Category) {
	t.Helper()
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")
	return h, q, user, cat
}

// TestValidateDate_WindowBoundaries pins the bound itself, off-by-one on both
// ends. validateDate is the shared helper; a bound applied in only one of its
// two callers would leave this green, so the four write PATHS are driven
// separately below.
func TestValidateDate_WindowBoundaries(t *testing.T) {
	cases := []struct {
		date string
		ok   bool
		why  string
	}{
		{fmt.Sprintf("%d-12-31", MinDataYear-1), false, "one day below the window"},
		{fmt.Sprintf("%d-01-01", MinDataYear), true, "the first day of the window"},
		{fmt.Sprintf("%d-12-31", MaxDataYear), true, "the last day of the window"},
		{fmt.Sprintf("%d-01-01", MaxDataYear+1), false, "one day above the window"},
		{"0001-01-01", false, "Go's zero date, which parsed and stored before the bound"},
		{"1850-06-01", false, "a plausible-looking typo that parsed and stored before the bound"},
		{"3021-01-02", false, "a fat-fingered year that parsed and stored before the bound"},
	}

	for _, tc := range cases {
		t.Run(tc.date, func(t *testing.T) {
			got, err := validateDate(tc.date)
			if tc.ok {
				if err != nil {
					t.Fatalf("validateDate(%q) = %v, want accepted (%s)", tc.date, err, tc.why)
				}
				if got != tc.date {
					t.Errorf("validateDate(%q) = %q, want the canonical form unchanged", tc.date, got)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateDate(%q) returned nil, want an error (%s)", tc.date, tc.why)
			}
		})
	}
}

// TestValidateDate_BoundsEveryWritePath drives all four paths that can set a
// transaction date. Each has its own validation entry point, so a bound fitted
// to only one of them is a live hole.
func TestValidateDate_BoundsEveryWritePath(t *testing.T) {
	tooOld := fmt.Sprintf("%d-12-31", MinDataYear-1)
	tooNew := fmt.Sprintf("%d-01-01", MaxDataYear+1)
	inRange := fmt.Sprintf("%d-01-01", MinDataYear)

	t.Run("create", func(t *testing.T) {
		for _, date := range []string{tooOld, tooNew} {
			h, _, user, cat := dateWindowFixture(t)
			body := strings.NewReader(fmt.Sprintf(
				`{"date":%q,"amount":10,"description":"x","category_id":%d}`, date, cat.ID))
			req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
			rec := httptest.NewRecorder()
			h.handleCreateTransaction(rec, withUser(req, user))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("POST date=%s: got %d, want 400 — an unbounded create is exactly the hole this closes; body: %s",
					date, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("create at the boundary is accepted", func(t *testing.T) {
		h, _, user, cat := dateWindowFixture(t)
		body := strings.NewReader(fmt.Sprintf(
			`{"date":%q,"amount":10,"description":"x","category_id":%d}`, inRange, cat.ID))
		req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
		rec := httptest.NewRecorder()
		h.handleCreateTransaction(rec, withUser(req, user))

		if rec.Code != http.StatusCreated {
			t.Errorf("POST date=%s: got %d, want 201 — the bound must be inclusive at %d; body: %s",
				inRange, rec.Code, MinDataYear, rec.Body.String())
		}
	})

	t.Run("batch create", func(t *testing.T) {
		h, _, user, cat := dateWindowFixture(t)
		// Item 0 is valid so a failure here can only come from item 1.
		body := strings.NewReader(fmt.Sprintf(
			`[{"date":"2026-01-01","amount":10,"description":"ok","category_id":%d},`+
				`{"date":%q,"amount":10,"description":"bad","category_id":%d}]`,
			cat.ID, tooNew, cat.ID))
		req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
		rec := httptest.NewRecorder()
		h.handleBatchCreateTransactions(rec, withUser(req, user))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("batch create with date=%s: got %d, want 400; body: %s", tooNew, rec.Code, rec.Body.String())
		}
	})

	t.Run("update", func(t *testing.T) {
		h, q, user, cat := dateWindowFixture(t)
		txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-01", 10, "in range")

		body := strings.NewReader(fmt.Sprintf(
			`{"date":%q,"amount":10,"description":"x","category_id":%d}`, tooNew, cat.ID))
		req := httptest.NewRequest(http.MethodPut, "/api/transactions/1", body)
		req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
		rec := httptest.NewRecorder()
		h.handleUpdateTransaction(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT moving an in-range row to %s: got %d, want 400 — grandfathering must apply to the "+
				"row's OWN stored date only, never license a fresh out-of-window date; body: %s",
				tooNew, rec.Code, rec.Body.String())
		}
	})

	t.Run("bulk patch", func(t *testing.T) {
		h, q, user, cat := dateWindowFixture(t)
		txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-01", 10, "in range")

		body := strings.NewReader(fmt.Sprintf(
			`{"ids":[%d],"patch":{"date":%q}}`, txn.ID, tooOld))
		req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-update", body)
		rec := httptest.NewRecorder()
		h.handleBatchUpdateTransactions(rec, withUser(req, user))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("bulk patch date=%s: got %d, want 400 — a bulk date is always an explicit new value, "+
				"so nothing is grandfathered; body: %s", tooOld, rec.Code, rec.Body.String())
		}
	})
}

// TestValidateDate_ExistingOutOfWindowRowIsNotTrapped is the case the whole T3
// design exists for. The bound has been absent for the app's entire life, so
// any deployment may already hold rows outside the window. PUT is a full
// replace: the client resends the stored date on every edit.
//
// Deliberately NOT vacuous: the row is seeded straight into the DB (seeds
// bypass validation, exactly like a row created before the bound shipped), and
// each sub-case asserts a DIFFERENT status, so a handler that blanket-allowed
// or blanket-rejected out-of-window updates fails at least one of them.
func TestValidateDate_ExistingOutOfWindowRowIsNotTrapped(t *testing.T) {
	const legacy = "3021-01-02" // above MaxDataYear; stored before the bound existed

	t.Run("editing another field keeps the stored date", func(t *testing.T) {
		h, q, user, cat := dateWindowFixture(t)
		txn := seedTestTransaction(t, q, user.ID, cat.ID, legacy, 10, "typo'd year")

		body := strings.NewReader(fmt.Sprintf(
			`{"date":%q,"amount":10,"description":"recategorised","category_id":%d}`, legacy, cat.ID))
		req := httptest.NewRequest(http.MethodPut, "/api/transactions/1", body)
		req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
		rec := httptest.NewRecorder()
		h.handleUpdateTransaction(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("PUT resending the row's own stored date %s: got %d, want 200 — the user would be locked "+
				"out of editing their own row; body: %s", legacy, rec.Code, rec.Body.String())
		}

		// The edit must actually have landed, not merely returned 200.
		after, err := q.GetTransactionByID(req.Context(), txn.ID)
		if err != nil {
			t.Fatalf("re-read transaction: %v", err)
		}
		if after.Description != "recategorised" {
			t.Errorf("description = %q, want %q — the update was accepted but did not apply", after.Description, "recategorised")
		}
	})

	t.Run("correcting into the window is always possible", func(t *testing.T) {
		h, q, user, cat := dateWindowFixture(t)
		txn := seedTestTransaction(t, q, user.ID, cat.ID, legacy, 10, "typo'd year")

		body := strings.NewReader(fmt.Sprintf(
			`{"date":"2021-01-02","amount":10,"description":"fixed","category_id":%d}`, cat.ID))
		req := httptest.NewRequest(http.MethodPut, "/api/transactions/1", body)
		req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
		rec := httptest.NewRecorder()
		h.handleUpdateTransaction(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("PUT correcting %s -> 2021-01-02: got %d, want 200; body: %s", legacy, rec.Code, rec.Body.String())
		}

		after, err := q.GetTransactionByID(req.Context(), txn.ID)
		if err != nil {
			t.Fatalf("re-read transaction: %v", err)
		}
		if got := after.Date.Format("2006-01-02"); got != "2021-01-02" {
			t.Errorf("date = %s, want 2021-01-02 — the correction path must actually move the row", got)
		}
	})

	t.Run("moving to a different out-of-window date is refused", func(t *testing.T) {
		h, q, user, cat := dateWindowFixture(t)
		txn := seedTestTransaction(t, q, user.ID, cat.ID, legacy, 10, "typo'd year")

		// One day later — still out of the window. Grandfathering is exact-match
		// on the stored date, not "this row is legacy so anything goes".
		body := strings.NewReader(fmt.Sprintf(
			`{"date":"3021-01-03","amount":10,"description":"x","category_id":%d}`, cat.ID))
		req := httptest.NewRequest(http.MethodPut, "/api/transactions/1", body)
		req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
		rec := httptest.NewRecorder()
		h.handleUpdateTransaction(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT moving %s -> 3021-01-03: got %d, want 400 — an out-of-window row may STAY where it is, "+
				"but the hole must not stay writable; body: %s", legacy, rec.Code, rec.Body.String())
		}
	})

	t.Run("bulk-editing another field leaves the stored date alone", func(t *testing.T) {
		h, q, user, cat := dateWindowFixture(t)
		txn := seedTestTransaction(t, q, user.ID, cat.ID, legacy, 10, "typo'd year")

		// No `date` key at all: patch semantics are "unset = do not touch", so a
		// legacy row is bulk-recategorisable without the date ever being revalidated.
		body := strings.NewReader(fmt.Sprintf(`{"ids":[%d],"patch":{"description":"bulk renamed"}}`, txn.ID))
		req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-update", body)
		rec := httptest.NewRecorder()
		h.handleBatchUpdateTransactions(rec, withUser(req, user))

		if rec.Code != http.StatusOK {
			t.Fatalf("bulk description edit of a legacy row: got %d, want 200; body: %s", rec.Code, rec.Body.String())
		}

		after, err := q.GetTransactionByID(req.Context(), txn.ID)
		if err != nil {
			t.Fatalf("re-read transaction: %v", err)
		}
		if after.Description != "bulk renamed" {
			t.Errorf("description = %q, want %q", after.Description, "bulk renamed")
		}
		if got := after.Date.Format("2006-01-02"); got != legacy {
			t.Errorf("date = %s, want %s — a patch without a `date` key must not move the row", got, legacy)
		}
	})
}
