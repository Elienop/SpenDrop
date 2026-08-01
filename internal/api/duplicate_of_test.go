package api

// This file is named for a wire field that no longer exists, deliberately:
// anyone grepping `duplicate_of` should land on the reason it is gone.
//
// A create response briefly carried `duplicate_of`, the id of a pre-existing
// row with the same content hash, so the client could say "you already have
// this one". It was wrong for this household. ComputeContentHash covers
// (date, amountCents, description, categoryName) — no user_id and no
// time-of-day — and the FIRST row to claim a digest anchors it household-wide.
// Two people share this ledger and both type by hand, so one member's ordinary
// lunch was reported to them as a duplicate of the other member's lunch.
//
// Scoping the lookup to the acting user does not repair it: when one member's
// row anchors the digest, the other member's genuine retry-double also resolves
// to that row, so a same-user filter goes silent exactly when the real bug
// fires. Answering "did I just submit this twice" needs a client-minted
// idempotency key, not a content hash.
//
// The tests below therefore guard an ABSENCE — no create path may emit a
// duplicate verdict — plus the identity behaviour that survives and that import
// dedupe still rests on: each distinct row anchors its own content_hash, and a
// repeat stores NULL (earliest-id-wins).

import (
	"bytes"
	"encoding/json"
	"fmt"
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
// "present and zero" — the exact distinction these tests turn on.
func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	return m
}

func idOf(t *testing.T, m map[string]any) int64 {
	t.Helper()
	n, ok := m["id"].(float64)
	if !ok {
		t.Fatalf("id = %#v, want a JSON number", m["id"])
	}
	return int64(n)
}

// assertNoDuplicateVerdict fails if a create response carries a duplicate
// verdict. Decoding into a map rather than a typed struct is load-bearing: a
// typed decode zero-fills a field the handler never emitted and cannot tell
// "absent" from "present and zero".
func assertNoDuplicateVerdict(t *testing.T, m map[string]any) {
	t.Helper()
	if v, ok := m["duplicate_of"]; ok {
		t.Errorf("create response carries duplicate_of = %#v; the content hash "+
			"has no user_id and no time-of-day, so it cannot tell one member's "+
			"identical entry from the other member's retry double", v)
	}
}

// TestHandleCreateTransaction_OtherMembersIdenticalEntryIsNotFlagged is the
// owner's actual case and the one that must never regress.
//
// Two people share this ledger and both type by hand. Maya buys lunch, Elie
// buys the same lunch on the same day for the same price. The digest carries no
// user, so Maya's row anchors it and Elie's create used to come back saying he
// already had this one — next to a one-tap Undo that would soft-delete a real
// expense into a Trash non-admins cannot open.
//
// Non-vacuity: the content_hash assertions at the end are what make this a real
// negative. They prove an anchor genuinely existed and was genuinely found, so
// a stray verdict would have had something concrete to report. A test that
// created one row and checked no verdict appeared would pass against any
// implementation, including one that never probes at all.
func TestHandleCreateTransaction_OtherMembersIdenticalEntryIsNotFlagged(t *testing.T) {
	h := setupHandler(t)
	maya := seedTestUser(t, h.queries, "maya", "member")
	elie := seedTestUser(t, h.queries, "elie", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	mayaRec := createTxnOn(t, h, maya, catID, "2026-07-01", "Lunch", 12.50)
	if mayaRec.Code != http.StatusCreated {
		t.Fatalf("maya's create: status = %d, body = %s", mayaRec.Code, mayaRec.Body.String())
	}
	mayaBody := decodeObject(t, mayaRec)
	assertNoDuplicateVerdict(t, mayaBody)

	elieRec := createTxnOn(t, h, elie, catID, "2026-07-01", "Lunch", 12.50)
	if elieRec.Code != http.StatusCreated {
		t.Fatalf("elie's identical create was rejected: status = %d, body = %s",
			elieRec.Code, elieRec.Body.String())
	}
	elieBody := decodeObject(t, elieRec)
	assertNoDuplicateVerdict(t, elieBody)

	counts, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Live != 2 {
		t.Fatalf("live rows = %d, want 2 — both members' lunches are real expenses", counts.Live)
	}

	// The anchor genuinely exists: Maya's row holds the digest and Elie's row
	// stores NULL (earliest-id-wins). So the probe DID find a pre-existing row
	// with the same content — and still said nothing on the wire.
	if hash := hashOf(t, h, idOf(t, mayaBody)); !hash.Valid {
		t.Error("maya's row should anchor the identity, but its content_hash is NULL — " +
			"without a live anchor this test could not have detected a stray verdict")
	}
	if hash := hashOf(t, h, idOf(t, elieBody)); hash.Valid {
		t.Errorf("elie's row should carry a NULL content_hash (earliest-id-wins), got %q",
			hash.String)
	}
}

// TestHandleCreateTransaction_IdenticalEntryCarriesNoVerdict is the same-user
// half. Even when one person enters the same thing twice, the server does not
// second-guess them: splitting a bill into two equal rows and buying two
// identical coffees are both ordinary, and the hash cannot tell either from a
// retry-induced double.
func TestHandleCreateTransaction_IdenticalEntryCarriesNoVerdict(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Coffee")

	first := createTxnOn(t, h, user, catID, "2026-07-01", "Flat white", 4.50)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, body = %s", first.Code, first.Body.String())
	}
	firstBody := decodeObject(t, first)
	assertNoDuplicateVerdict(t, firstBody)

	second := createTxnOn(t, h, user, catID, "2026-07-01", "Flat white", 4.50)
	if second.Code != http.StatusCreated {
		t.Fatalf("the second identical create was rejected: status = %d, body = %s",
			second.Code, second.Body.String())
	}
	secondBody := decodeObject(t, second)
	assertNoDuplicateVerdict(t, secondBody)

	counts, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Live != 2 {
		t.Fatalf("live rows = %d, want 2 — identical entries are still permitted", counts.Live)
	}

	// Same non-vacuity guard as the two-member case: the identity really was
	// taken by the time the second create ran.
	if hash := hashOf(t, h, idOf(t, firstBody)); !hash.Valid {
		t.Error("the first row should anchor the identity, but its content_hash is NULL")
	}
	if hash := hashOf(t, h, idOf(t, secondBody)); hash.Valid {
		t.Errorf("the repeat should carry a NULL content_hash, got %q", hash.String)
	}
}

// TestHandleCreateTransaction_DistinctRowsStillStoreTheirOwnHash keeps the
// coverage that varying any single hash input yields a DISTINCT identity, which
// is what import dedupe rests on.
//
// This replaces the old "distinct rows report no duplicate_of" negative: once
// no verdict is ever emitted, that assertion is vacuous against every possible
// implementation. Asserting that each distinct row anchors its own hash is the
// invariant that still has teeth.
func TestHandleCreateTransaction_DistinctRowsStillStoreTheirOwnHash(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Coffee")

	seedRec := createTxnOn(t, h, user, catID, "2026-07-01", "Flat white", 4.50)
	if seedRec.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body = %s", seedRec.Code, seedRec.Body.String())
	}
	seedHash := hashOf(t, h, idOf(t, decodeObject(t, seedRec)))
	if !seedHash.Valid {
		t.Fatal("the seed row stored a NULL content_hash — import dedupe cannot see it")
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
			body := decodeObject(t, rec)
			assertNoDuplicateVerdict(t, body)

			hash := hashOf(t, h, idOf(t, body))
			if !hash.Valid {
				t.Fatal("a genuinely new row stored a NULL content_hash — " +
					"a later import of this content would duplicate it")
			}
			if hash.String == seedHash.String {
				t.Errorf("content_hash matches the seed row's; %s must produce a "+
					"distinct identity", tc.name)
			}
		})
	}
}

// TestHandleBatchCreateTransactions_CarriesNoVerdict covers the other create
// path. Batch-create runs the same identity probe against its own transaction,
// so the second of two identical items is recognised there too — and must stay
// just as silent about it.
func TestHandleBatchCreateTransactions_CarriesNoVerdict(t *testing.T) {
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
	for i, item := range items {
		t.Run(fmt.Sprintf("item %d", i), func(t *testing.T) {
			assertNoDuplicateVerdict(t, item)
		})
	}

	counts, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Live != 3 {
		t.Fatalf("live rows = %d, want 3", counts.Live)
	}

	// Non-vacuity, same as the single-create tests: the probe really did see
	// the identity taken by the time item 1 was inserted.
	if hash := hashOf(t, h, idOf(t, items[0])); !hash.Valid {
		t.Error("item 0 should anchor the identity, but its content_hash is NULL")
	}
	if hash := hashOf(t, h, idOf(t, items[1])); hash.Valid {
		t.Errorf("item 1 repeats item 0 and should carry a NULL content_hash, got %q",
			hash.String)
	}
	if hash := hashOf(t, h, idOf(t, items[2])); !hash.Valid {
		t.Error("item 2 is distinct and should anchor its own content_hash")
	}
}
