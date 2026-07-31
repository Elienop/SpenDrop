package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// createTxnOn posts a single transaction on an explicit date and returns the
// recorder. createTxnViaAPI (manual_content_hash_test.go) pins the date, which
// is exactly the field these tests need to vary to build a NON-duplicate.
func createTxnOn(t *testing.T, h *Handler, user database.User, catID int64, date, desc string, amount float64) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"date":        date,
		"amount":      amount,
		"description": desc,
		"category_id": catID,
	})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)
	return rec
}

// decodeObject decodes a 201 body into an untyped map. A typed struct would
// zero-fill a field the handler never emitted, so it cannot tell "absent" from
// "present and zero" — the exact distinction duplicate_of is built on.
func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	return m
}

// duplicateOf reads the duplicate_of field, reporting whether it was present.
func duplicateOf(t *testing.T, m map[string]any) (int64, bool) {
	t.Helper()
	raw, ok := m["duplicate_of"]
	if !ok {
		return 0, false
	}
	n, isNum := raw.(float64)
	if !isNum {
		t.Fatalf("duplicate_of = %#v, want a JSON number", raw)
	}
	return int64(n), true
}

func idOf(t *testing.T, m map[string]any) int64 {
	t.Helper()
	n, ok := m["id"].(float64)
	if !ok {
		t.Fatalf("id = %#v, want a JSON number", m["id"])
	}
	return int64(n)
}

// TestHandleCreateTransaction_DuplicateResponseIdentifiesTheOriginal is the
// regression test for the silent-double-entry bug.
//
// The owner types every transaction by hand on a phone. When the network stalls
// mid-save he taps Retry, and the same expense lands twice. The server already
// recognises the second one — contentHashForManualEntry finds the identity
// taken and stores NULL rather than colliding — but the 201 it returns is
// byte-identical to the 201 for a genuinely new row, so no client can tell the
// user "you already have this one" or offer to undo it.
//
// Creating the duplicate must still SUCCEED: two identical coffees on one day
// are real. This is about telling the user, not blocking them.
func TestHandleCreateTransaction_DuplicateResponseIdentifiesTheOriginal(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Coffee")

	first := createTxnOn(t, h, user, catID, "2026-07-01", "Flat white", 4.50)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, body = %s", first.Code, first.Body.String())
	}
	firstBody := decodeObject(t, first)
	if _, present := duplicateOf(t, firstBody); present {
		t.Error("the first row of its kind reported duplicate_of; nothing preceded it")
	}

	second := createTxnOn(t, h, user, catID, "2026-07-01", "Flat white", 4.50)
	if second.Code != http.StatusCreated {
		t.Fatalf("the duplicate was rejected: status = %d, body = %s",
			second.Code, second.Body.String())
	}
	secondBody := decodeObject(t, second)

	got, present := duplicateOf(t, secondBody)
	if !present {
		t.Fatal("the second identical create reported no duplicate_of — the client " +
			"cannot tell a retry-induced double from a new expense")
	}
	if want := idOf(t, firstBody); got != want {
		t.Errorf("duplicate_of = %d, want %d (the pre-existing row)", got, want)
	}
	if selfID := idOf(t, secondBody); got == selfID {
		t.Errorf("duplicate_of points at the new row (%d), not the original", selfID)
	}

	// The deliberate allow-duplicates behaviour is unchanged: both rows live.
	counts, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Live != 2 {
		t.Fatalf("live rows = %d, want 2 — reporting the duplicate must not block it", counts.Live)
	}
}

// TestHandleCreateTransaction_DistinctRowsNeverReportDuplicateOf is the
// negative half. An "always emit duplicate_of" implementation would pass the
// positive test above and make the client shout "you already have this one" at
// every single entry.
func TestHandleCreateTransaction_DistinctRowsNeverReportDuplicateOf(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Coffee")

	if rec := createTxnOn(t, h, user, catID, "2026-07-01", "Flat white", 4.50); rec.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Each of these differs from the seed in exactly one hash input.
	cases := []struct {
		name   string
		date   string
		desc   string
		amount float64
	}{
		{"different amount", "2026-07-01", "Flat white", 4.75},
		{"different date", "2026-07-02", "Flat white", 4.50},
		{"different description", "2026-07-01", "Long black", 4.50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := createTxnOn(t, h, user, catID, tc.date, tc.desc, tc.amount)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if id, present := duplicateOf(t, decodeObject(t, rec)); present {
				t.Errorf("a genuinely new row reported duplicate_of = %d", id)
			}
		})
	}
}

// TestHandleBatchCreateTransactions_ReportsDuplicateOfForIdenticalItems covers
// the other create path. Batch-create runs the same identity probe against its
// own transaction, so the second of two identical items in one request is
// already recognised as a duplicate — the wire must say so there too.
func TestHandleBatchCreateTransactions_ReportsDuplicateOfForIdenticalItems(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "member", "member")

	body := strings.NewReader(`[
		{"date":"2026-04-06","amount":4.50,"description":"coffee","category_id":1},
		{"date":"2026-04-06","amount":4.50,"description":"coffee","category_id":1},
		{"date":"2026-04-06","amount":9.00,"description":"lunch","category_id":1}
	]`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body), user)
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d results, want 3", len(items))
	}

	if _, present := duplicateOf(t, items[0]); present {
		t.Error("the anchoring item reported duplicate_of")
	}
	got, present := duplicateOf(t, items[1])
	if !present {
		t.Fatal("the second identical item reported no duplicate_of")
	}
	if want := idOf(t, items[0]); got != want {
		t.Errorf("duplicate_of = %d, want %d (the item that anchored the identity)", got, want)
	}
	if _, present := duplicateOf(t, items[2]); present {
		t.Error("a distinct item in the same batch reported duplicate_of")
	}
}

// seedRowWithDigest inserts a row holding `digest` directly, bypassing the
// handlers, so anchorIDForDigest can be exercised without racing anything.
func seedRowWithDigest(t *testing.T, h *Handler, userID, catID int64, desc, digest string, tombstoned bool) int64 {
	t.Helper()
	deletedAt := "NULL"
	if tombstoned {
		deletedAt = "CURRENT_TIMESTAMP"
	}
	res, err := h.db.Exec(
		`INSERT INTO transactions (user_id, date, amount_cents, description, category_id, content_hash, deleted_at)
		 VALUES (?, '2026-07-01', 450, ?, ?, ?, `+deletedAt+`)`,
		userID, desc, catID, digest)
	if err != nil {
		t.Fatalf("seed row: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// TestAnchorIDForDigest covers the lookup handleCreateTransaction falls back to
// when it LOSES the insert race: the advisory probe said the identity was free,
// a concurrent create claimed it first, and the UNIQUE violation is recovered
// by dropping the hash. At that point the winner's id is the only thing that
// can populate duplicate_of, and it has to be re-read.
//
// The race itself is not deterministic, so the lookup is tested directly here
// and its wiring is covered by the concurrency test in manual_content_hash_test.go.
func TestAnchorIDForDigest(t *testing.T) {
	t.Run("returns the id of the live row holding the digest", func(t *testing.T) {
		h := setupHandler(t)
		user := seedTestUser(t, h.queries, "owner", "member")
		catID := seedExpenseCategory(t, h.queries, "Coffee")
		want := seedRowWithDigest(t, h, user.ID, catID, "Flat white", "digest-a", false)

		if got := anchorIDForDigest(t.Context(), h.queries, "digest-a"); got != want {
			t.Errorf("anchorIDForDigest = %d, want %d", got, want)
		}
	})

	t.Run("returns 0 when the digest is unclaimed", func(t *testing.T) {
		h := setupHandler(t)
		user := seedTestUser(t, h.queries, "owner", "member")
		catID := seedExpenseCategory(t, h.queries, "Coffee")
		seedRowWithDigest(t, h, user.ID, catID, "Flat white", "digest-a", false)

		if got := anchorIDForDigest(t.Context(), h.queries, "digest-b"); got != 0 {
			t.Errorf("anchorIDForDigest = %d, want 0", got)
		}
	})

	t.Run("ignores a tombstoned holder", func(t *testing.T) {
		h := setupHandler(t)
		user := seedTestUser(t, h.queries, "owner", "member")
		catID := seedExpenseCategory(t, h.queries, "Coffee")
		seedRowWithDigest(t, h, user.ID, catID, "Flat white", "digest-a", true)

		// A trashed row does not exist from the user's perspective, and
		// GetTransactionByContentHash skips it, so re-adding its content is not
		// a duplicate of anything the user can see.
		if got := anchorIDForDigest(t.Context(), h.queries, "digest-a"); got != 0 {
			t.Errorf("anchorIDForDigest = %d, want 0 — a tombstoned row was reported "+
				"as the duplicate the user already has", got)
		}
	})

	// The partial UNIQUE index is `WHERE content_hash IS NOT NULL`, so an empty
	// string is a legal stored value even though nothing should ever write one.
	// Without the empty-digest guard, any row that reached that state would be
	// reported as the duplicate of every create whose digest failed to compute.
	t.Run("an empty digest matches nothing", func(t *testing.T) {
		h := setupHandler(t)
		user := seedTestUser(t, h.queries, "owner", "member")
		catID := seedExpenseCategory(t, h.queries, "Coffee")
		seedRowWithDigest(t, h, user.ID, catID, "Flat white", "", false)

		if got := anchorIDForDigest(t.Context(), h.queries, ""); got != 0 {
			t.Errorf("anchorIDForDigest = %d, want 0", got)
		}
	})
}
