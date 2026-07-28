package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

func createTxnViaAPI(t *testing.T, h *Handler, user database.User, catID int64, desc string, amount float64) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"date":        "2026-07-01",
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

func hashOf(t *testing.T, h *Handler, id int64) sql.NullString {
	t.Helper()
	var hash sql.NullString
	if err := h.db.QueryRow(`SELECT content_hash FROM transactions WHERE id = ?`, id).Scan(&hash); err != nil {
		t.Fatalf("read content_hash: %v", err)
	}
	return hash
}

// TestHandleCreateTransaction_StoresContentHash is the regression test for
// manual entries being invisible to import dedupe. Every hand-entered row used
// to store content_hash = NULL, so importing a spreadsheet containing a
// transaction the user had already typed in silently created a second copy.
func TestHandleCreateTransaction_StoresContentHash(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	rec := createTxnViaAPI(t, h, user, catID, "Whole Foods", 42.50)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	hash := hashOf(t, h, created.ID)
	if !hash.Valid || hash.String == "" {
		t.Fatal("manual entry stored a NULL content_hash — import dedupe cannot see it")
	}

	// It must be the same identity the import path would compute, or the two
	// sides will never match. Import hashes against the category's canonical
	// name, so read it back rather than assuming the seed's literal.
	cat, err := h.queries.GetCategoryByID(t.Context(), catID)
	if err != nil {
		t.Fatalf("load category: %v", err)
	}
	want := database.ComputeContentHash(
		mustParseDate(t, "2026-07-01"), 4250, "Whole Foods", cat.Name)
	if hash.String != want {
		t.Errorf("content_hash = %s, want %s (import would compute the latter)",
			hash.String, want)
	}
}

// TestHandleCreateTransaction_DuplicateEntryIsAcceptedWithNullHash is the
// other half of the contract, and the one that keeps the fix safe.
//
// Real household data contains legitimate duplicates — two identical coffees
// in a day, a subscription entered twice deliberately. The partial UNIQUE
// index on content_hash would reject the second row outright. Following the
// earliest-id-wins rule the migration-008 backfill uses, the first row keeps
// the identity and later identical rows are stored with NULL, fully intact.
func TestHandleCreateTransaction_DuplicateEntryIsAcceptedWithNullHash(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Coffee")

	first := createTxnViaAPI(t, h, user, catID, "Flat white", 4.50)
	if first.Code >= 300 {
		t.Fatalf("first create failed: %d %s", first.Code, first.Body.String())
	}
	second := createTxnViaAPI(t, h, user, catID, "Flat white", 4.50)
	if second.Code >= 300 {
		t.Fatalf("a legitimate duplicate entry was rejected: %d %s",
			second.Code, second.Body.String())
	}

	counts, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Live != 2 {
		t.Fatalf("expected both rows to survive, got %d live", counts.Live)
	}

	var decodedFirst, decodedSecond struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &decodedFirst)
	_ = json.Unmarshal(second.Body.Bytes(), &decodedSecond)

	if h1 := hashOf(t, h, decodedFirst.ID); !h1.Valid {
		t.Error("the first row should anchor the identity, but its hash is NULL")
	}
	if h2 := hashOf(t, h, decodedSecond.ID); h2.Valid {
		t.Errorf("the duplicate should carry a NULL hash, got %q", h2.String)
	}
}
