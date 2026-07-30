package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// contentHashesByDescription reads content_hash straight from the table. The
// column is not on the wire, so a handler-response assertion could not see it.
func contentHashesByDescription(t *testing.T, h *Handler) map[string][]sql.NullString {
	t.Helper()
	rows, err := h.db.Query(`SELECT description, content_hash FROM transactions ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	out := map[string][]sql.NullString{}
	for rows.Next() {
		var desc string
		var hash sql.NullString
		if err := rows.Scan(&desc, &hash); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[desc] = append(out[desc], hash)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestHandleBatchCreateTransactions_AssignsContentHash is the regression test
// for the dedupe fix reaching only one of the two create paths.
//
// Single-create computes a content_hash so import dedupe can see hand-entered
// rows. Batch-create did not, leaving content_hash NULL — and batch-create is
// what the mobile quick-add and the offline queue drain into. Re-importing a
// spreadsheet containing those entries therefore duplicated every one of them,
// which is the exact bug the single-path fix was written to stop.
func TestHandleBatchCreateTransactions_AssignsContentHash(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "member", "member")

	body := strings.NewReader(`[
		{"date":"2026-04-06","amount":10.00,"description":"groceries","category_id":1},
		{"date":"2026-04-07","amount":20.00,"description":"fuel","category_id":1}
	]`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body), user)
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	hashes := contentHashesByDescription(t, h)
	for _, desc := range []string{"groceries", "fuel"} {
		got := hashes[desc]
		if len(got) != 1 {
			t.Fatalf("%s: got %d rows, want 1", desc, len(got))
		}
		if !got[0].Valid || got[0].String == "" {
			t.Errorf("%s: content_hash is NULL — rows added through batch create are "+
				"invisible to import dedupe, so re-importing duplicates them", desc)
		}
	}
	if hashes["groceries"][0].String == hashes["fuel"][0].String {
		t.Error("two different transactions share a content_hash")
	}
}

// TestHandleBatchCreateTransactions_ToleratesIdenticalItems is the partner that
// keeps the fix from breaking the batch outright.
//
// The partial UNIQUE index on content_hash rejects a second identical live row.
// Real households do submit genuine duplicates — two identical coffees on one
// day — so the hash probe must run against the SAME transaction the inserts use,
// letting the first item anchor the identity while the second stores NULL. If it
// ran against the outer handle instead, the second insert would violate the index
// and take the whole batch down with it, losing every item the user entered.
func TestHandleBatchCreateTransactions_ToleratesIdenticalItems(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "member", "member")

	body := strings.NewReader(`[
		{"date":"2026-04-06","amount":4.50,"description":"coffee","category_id":1},
		{"date":"2026-04-06","amount":4.50,"description":"coffee","category_id":1}
	]`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body), user)
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — a legitimate duplicate collided on the unique "+
			"index and destroyed the whole batch; body = %s", rec.Code, rec.Body.String())
	}

	got := contentHashesByDescription(t, h)["coffee"]
	if len(got) != 2 {
		t.Fatalf("got %d coffee rows, want 2 — a legitimate duplicate was dropped", len(got))
	}
	// Earliest-id-wins: the first anchors the identity, the second stays NULL and
	// remains fully intact in the ledger.
	if !got[0].Valid {
		t.Error("the first of two identical items has no content_hash; it should anchor the identity")
	}
	if got[1].Valid {
		t.Errorf("the second identical item kept content_hash %q; later duplicates must store NULL",
			got[1].String)
	}
}
