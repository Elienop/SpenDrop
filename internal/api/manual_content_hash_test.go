package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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

// TestHandleCreateTransaction_ConcurrentIdenticalCreatesAllSucceed is the
// regression test for the check-then-act race in contentHashForManualEntry.
//
// The duplicate pre-check (GetTransactionByContentHash) runs on h.queries,
// which returns its connection to the pool before txnStore.Create opens its
// own transaction. SetMaxOpenConns(1) therefore does NOT serialise probe and
// insert as one unit: several concurrent identical creates can all observe
// "hash free", after which every loser hits the partial UNIQUE index
// idx_transactions_content_hash and has its whole create rolled back with a
// 500. A legitimate duplicate entry must never be lost that way.
//
// Rounds x workers rather than a single burst: the race is probabilistic
// (~25% at 8-way in manual reproduction), so one round could pass by luck
// even with the recovery removed.
//
// rounds is sized so a mutant SURVIVING is not a realistic outcome. A round
// escapes with probability (1-p), so the whole test false-passes with
// (1-p)^rounds — and at rounds=6 that was not small enough. Mutating out the
// IsContentHashUniqueViolation recovery in handleCreateTransaction, rounds=6
// still PASSED 4 of 40 runs under GOMAXPROCS=2 (a 10% false green); rounds=24
// failed 40/40 under the same conditions and 25/25 at default parallelism.
//
// `go test -race` (what CI runs) catches the mutant every time, so the exposure
// is not CI — it is the plain `go test ./...` this project mandates before
// every commit and for mutation-testing, where an engineer verifying this very
// code could have taken a misleading green. Do not lower rounds back.
func TestHandleCreateTransaction_ConcurrentIdenticalCreatesAllSucceed(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Coffee")

	const rounds = 24
	const workers = 8

	for round := 0; round < rounds; round++ {
		desc := fmt.Sprintf("Flat white %d", round)
		recs := make([]*httptest.ResponseRecorder, workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				recs[i] = createTxnViaAPI(t, h, user, catID, desc, 4.50)
			}(i)
		}
		close(start)
		wg.Wait()

		for i, rec := range recs {
			if rec.Code != http.StatusCreated {
				t.Fatalf("round %d worker %d: status = %d, want 201; body = %s",
					round, i, rec.Code, rec.Body.String())
			}
		}

		// Wire contract under contention: losing the race is still not something
		// the server may report. The winner is whoever inserted first, which in
		// this household is as likely to be the other member as the retrying
		// one, so a "you already have this one" verdict derived from the digest
		// accuses the wrong person. See the header of duplicate_of_test.go.
		for _, rec := range recs {
			assertNoDuplicateVerdict(t, decodeObject(t, rec))
		}
	}

	// Status codes alone are not enough — a 201 whose row had been rolled
	// back would still read as success. Assert the rows actually landed.
	counts, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Live != rounds*workers {
		t.Fatalf("live rows = %d, want %d — a concurrent create was lost",
			counts.Live, rounds*workers)
	}

	// Exactly one row per identity may anchor the hash; the rest carry NULL
	// (earliest-id-wins, the same rule the migration-008 backfill uses).
	rows, err := h.db.Query(`
		SELECT description, COUNT(*), SUM(CASE WHEN content_hash IS NOT NULL THEN 1 ELSE 0 END)
		FROM transactions GROUP BY description`)
	if err != nil {
		t.Fatalf("group query: %v", err)
	}
	defer rows.Close()
	groups := 0
	for rows.Next() {
		var desc string
		var total, hashed int
		if err := rows.Scan(&desc, &total, &hashed); err != nil {
			t.Fatalf("scan: %v", err)
		}
		groups++
		if total != workers {
			t.Errorf("%q: %d rows stored, want %d", desc, total, workers)
		}
		if hashed != 1 {
			t.Errorf("%q: %d rows hold a content_hash, want exactly 1", desc, hashed)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if groups != rounds {
		t.Errorf("distinct descriptions stored = %d, want %d", groups, rounds)
	}
}
