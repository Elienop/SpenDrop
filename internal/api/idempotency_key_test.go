package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// These tests cover the client-minted idempotency key on POST /api/transactions:
// a retried submission must not become a second row. The neighbouring
// content_hash tests (manual_content_hash_test.go, duplicate_of_test.go) cover
// the other, deliberately separate question — "is this the same CONTENT" — and
// the two mechanisms must not interfere with each other.

// postCreate posts a single-transaction create from a raw body map. Tests build
// the body by hand rather than through a typed helper because the distinction
// between an ABSENT client_key (a stale service-worker bundle that predates the
// field) and an empty one has to stay expressible.
func postCreate(t *testing.T, h *Handler, user database.User, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(raw)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)
	return rec
}

// lunchBody is the body two household members would each produce for the same
// meal: identical in every content field. Only the key distinguishes a retry
// from a genuine second entry.
func lunchBody(catID int64) map[string]any {
	return map[string]any{
		"date":        "2026-07-01",
		"amount":      12.50,
		"description": "Lunch",
		"category_id": catID,
	}
}

func withKey(body map[string]any, key string) map[string]any {
	out := make(map[string]any, len(body)+1)
	for k, v := range body {
		out[k] = v
	}
	out["client_key"] = key
	return out
}

func countTransactionRows(t *testing.T, h *Handler) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	return n
}

func storedKey(t *testing.T, h *Handler, id int64) sql.NullString {
	t.Helper()
	var key sql.NullString
	if err := h.db.QueryRow(`SELECT idempotency_key FROM transactions WHERE id = ?`, id).Scan(&key); err != nil {
		t.Fatalf("read idempotency_key: %v", err)
	}
	return key
}

func countInsertAudits(t *testing.T, h *Handler, txnID int64) int {
	t.Helper()
	var n int
	err := h.db.QueryRow(
		`SELECT COUNT(*) FROM transaction_audit WHERE transaction_id = ? AND action = 'insert'`,
		txnID).Scan(&n)
	if err != nil {
		t.Fatalf("count insert audits: %v", err)
	}
	return n
}

// assertDollarsNotCents enforces the Money Wire-Edge rule on a create response:
// the money field crosses the wire as dollars under `amount`, and no *_cents
// column may leak. Decoding into a map is load-bearing — a typed struct
// zero-fills a field the handler never emitted, which is exactly how a renamed
// money key has slipped through before.
func assertDollarsNotCents(t *testing.T, m map[string]any, wantDollars float64) {
	t.Helper()
	amount, ok := m["amount"]
	if !ok {
		t.Fatalf("response has no `amount` key; formatCurrency(undefined) blanks the page. body keys: %v", jsonKeys(m))
	}
	got, ok := amount.(float64)
	if !ok {
		t.Fatalf("amount = %#v, want a JSON number", amount)
	}
	if got != wantDollars {
		t.Errorf("amount = %v, want %v (dollars, not cents)", got, wantDollars)
	}
	for k := range m {
		if strings.HasSuffix(k, "_cents") {
			t.Errorf("response leaks storage column %q; the frontend reads dollars", k)
		}
	}
}

func jsonKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestCreateTransaction_ReplayedKeyCreatesNothing is the core contract. The PWA
// posts a create, the response is lost on a flaky connection after the server
// has already committed, and the user taps Retry with the same key. The second
// request must resolve to the row the first one made.
//
// The two responses are compared for full equality, not just matching ids:
// invariant is that a client CANNOT distinguish a replay from a fresh success.
// It retried precisely because it does not know whether the first attempt
// landed, so any observable difference invites it to branch on something it
// must not care about.
func TestCreateTransaction_ReplayedKeyCreatesNothing(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	body := withKey(lunchBody(catID), "11111111-2222-3333-4444-555555555555")

	first := postCreate(t, h, user, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, body = %s", first.Code, first.Body.String())
	}
	firstObj := decodeObject(t, first)
	id := idOf(t, firstObj)

	second := postCreate(t, h, user, body)
	if second.Code < 200 || second.Code > 299 {
		t.Fatalf("replay: status = %d, want 2xx; body = %s", second.Code, second.Body.String())
	}
	secondObj := decodeObject(t, second)

	if n := countTransactionRows(t, h); n != 1 {
		t.Fatalf("transactions rows = %d, want 1 — the retry created a second row", n)
	}
	if got := idOf(t, secondObj); got != id {
		t.Errorf("replay returned id %d, want the original row %d", got, id)
	}
	if second.Code != first.Code {
		t.Errorf("replay status = %d, first = %d — a client can tell a replay from a fresh create",
			second.Code, first.Code)
	}
	if !reflect.DeepEqual(firstObj, secondObj) {
		t.Errorf("replay body differs from the original.\n first: %#v\nsecond: %#v", firstObj, secondObj)
	}

	assertDollarsNotCents(t, secondObj, 12.50)

	// One submission, one audit row. A replay that re-ran the write path would
	// leave a second `insert` row claiming the transaction was created twice.
	if n := countInsertAudits(t, h, id); n != 1 {
		t.Errorf("insert audit rows for txn %d = %d, want 1", id, n)
	}

	if k := storedKey(t, h, id); !k.Valid || k.String != body["client_key"] {
		t.Errorf("stored idempotency_key = %#v, want %q", k, body["client_key"])
	}
}

// TestCreateTransaction_DifferentKeysSameContentBothInsert is the household
// false-positive case, and the reason the content hash cannot answer this
// question at all. Two people share this ledger; both buy the same lunch on the
// same day for the same price and each types it in. Their submissions are
// distinct events with distinct keys and both rows must land.
//
// This is the case that killed the content-hash-derived `duplicate_of` verdict
// (see duplicate_of_test.go). An idempotency key must not reintroduce it.
func TestCreateTransaction_DifferentKeysSameContentBothInsert(t *testing.T) {
	h := setupHandler(t)
	alice := seedTestUser(t, h.queries, "alice", "member")
	bob := seedTestUser(t, h.queries, "bob", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	aliceRec := postCreate(t, h, alice, withKey(lunchBody(catID), "aaaaaaaa-0000-0000-0000-000000000001"))
	if aliceRec.Code != http.StatusCreated {
		t.Fatalf("alice create: status = %d, body = %s", aliceRec.Code, aliceRec.Body.String())
	}
	bobRec := postCreate(t, h, bob, withKey(lunchBody(catID), "bbbbbbbb-0000-0000-0000-000000000002"))
	if bobRec.Code != http.StatusCreated {
		t.Fatalf("bob's identical lunch was rejected: status = %d, body = %s",
			bobRec.Code, bobRec.Body.String())
	}

	if n := countTransactionRows(t, h); n != 2 {
		t.Fatalf("transactions rows = %d, want 2 — one member's entry was swallowed "+
			"as if it were the other's retry", n)
	}
	aliceObj, bobObj := decodeObject(t, aliceRec), decodeObject(t, bobRec)
	if idOf(t, aliceObj) == idOf(t, bobObj) {
		t.Error("both members were handed the same row id")
	}
	assertNoDuplicateVerdict(t, bobObj)

	// The content_hash contract still holds underneath: the first row anchors
	// the identity, the second stores NULL and stays intact (earliest-id-wins).
	if hash := hashOf(t, h, idOf(t, aliceObj)); !hash.Valid {
		t.Error("the first row should anchor the content hash, but it is NULL")
	}
	if hash := hashOf(t, h, idOf(t, bobObj)); hash.Valid {
		t.Errorf("the second identical row should carry a NULL hash, got %q", hash.String)
	}
}

// TestCreateTransaction_SameKeyDifferentUsersBothInsert pins the per-user key
// namespace, and it is the test that would have caught the integrity bug the
// security review found in the first cut of this feature.
//
// With a globally-unique key index, bob's create collided with alice's row, the
// handler answered with ALICE's row, and bob's transaction was discarded — a 201
// reporting success for a write that produced no row and no audit entry. The
// household ledger silently lost an expense.
//
// A key names one user's submission attempt. A retry is always the same user
// retrying, so scoping the namespace per user cannot weaken the retry guarantee;
// it only removes an authorization boundary nobody intended to create.
func TestCreateTransaction_SameKeyDifferentUsersBothInsert(t *testing.T) {
	h := setupHandler(t)
	alice := seedTestUser(t, h.queries, "alice", "member")
	bob := seedTestUser(t, h.queries, "bob", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	// Same key AND same content: the worst case, colliding on both indexes.
	body := withKey(lunchBody(catID), "collision-key-0000000001")

	aliceRec := postCreate(t, h, alice, body)
	if aliceRec.Code != http.StatusCreated {
		t.Fatalf("alice create: status = %d, body = %s", aliceRec.Code, aliceRec.Body.String())
	}
	bobRec := postCreate(t, h, bob, body)
	if bobRec.Code != http.StatusCreated {
		t.Fatalf("bob create: status = %d, body = %s", bobRec.Code, bobRec.Body.String())
	}

	if n := countTransactionRows(t, h); n != 2 {
		t.Fatalf("transactions rows = %d, want 2 — one member's write was discarded "+
			"as if it were the other member's retry", n)
	}

	aliceObj, bobObj := decodeObject(t, aliceRec), decodeObject(t, bobRec)
	aliceID, bobID := idOf(t, aliceObj), idOf(t, bobObj)
	if aliceID == bobID {
		t.Fatal("bob was handed alice's row id")
	}

	// Attribution: each row must belong to the member who submitted it. A row
	// that landed but under the wrong user_id is the same loss wearing a
	// different shape — it appears in the household list crediting the wrong
	// person and counts against the wrong member's activity.
	for _, tc := range []struct {
		name   string
		id     int64
		wantUD int64
	}{
		{"alice", aliceID, alice.ID},
		{"bob", bobID, bob.ID},
	} {
		var owner int64
		if err := h.db.QueryRow(`SELECT user_id FROM transactions WHERE id = ?`, tc.id).Scan(&owner); err != nil {
			t.Fatalf("read user_id for %s's row: %v", tc.name, err)
		}
		if owner != tc.wantUD {
			t.Errorf("%s's row id=%d is owned by user %d, want %d", tc.name, tc.id, owner, tc.wantUD)
		}
	}

	// Both writes are in the audit trail. The discarded-write bug left no trace
	// at all, so a count of insert rows is what proves the loss is gone.
	if n := countInsertAudits(t, h, aliceID) + countInsertAudits(t, h, bobID); n != 2 {
		t.Errorf("insert audit rows = %d, want 2", n)
	}

	// And each member's own retry still replays against their own row.
	replay := postCreate(t, h, bob, body)
	if replay.Code < 200 || replay.Code > 299 {
		t.Fatalf("bob's replay: status = %d, want 2xx; body = %s", replay.Code, replay.Body.String())
	}
	if got := idOf(t, decodeObject(t, replay)); got != bobID {
		t.Errorf("bob's replay returned id %d, want his own row %d", got, bobID)
	}
	if n := countTransactionRows(t, h); n != 2 {
		t.Errorf("transactions rows = %d after bob's replay, want 2", n)
	}
}

// TestCreateTransaction_DegenerateClientKeyRejected pins the shape guard.
//
// Every value here is what a caller sends when its key generator failed rather
// than a key it chose, and each would otherwise CLAIM a name in that caller's
// namespace: once "0" or "undefined" anchors a row, every later submission
// carrying it replays against that row and is dropped as a retry, so the
// caller's ledger silently stops recording. Per-user scoping bounds the damage
// to the caller's own data but does not prevent it — a script reusing one
// constant swallows its own writes.
func TestCreateTransaction_DegenerateClientKeyRejected(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	cases := []struct {
		name string
		key  string
	}{
		{"single space", " "},
		{"spaces only", "                    "},
		{"literal undefined", "undefined"},
		{"literal null", "null"},
		{"zero", "0"},
		{"tab", "\t"},
		{"non-breaking space", " "},
		{"NUL byte", "\x00"},
		{"embedded NUL in a valid key", "abcdefgh\x00ijklmnop"},
		{"one char short of the minimum", strings.Repeat("k", MinClientKeyLength-1)},
		{"internal whitespace", "abcdefgh ijklmnop"},
		{"slash", "abcdefgh/ijklmnop"},
		{"newline suffix", "abcdefghijklmnop\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postCreate(t, h, user, withKey(lunchBody(catID), tc.key))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("key %q: status = %d, want 400; body = %s",
					tc.key, rec.Code, rec.Body.String())
			}
		})
	}

	// No degenerate key may have written a row along the way.
	if n := countTransactionRows(t, h); n != 0 {
		t.Errorf("transactions rows = %d, want 0 — a rejected key still wrote", n)
	}

	// The floor must not reject anything a real client sends: crypto.randomUUID()
	// is 36 characters, and every accepted key round-trips to storage intact.
	uuid := "9f8e7d6c-5b4a-3928-1706-fedcba987654"
	rec := postCreate(t, h, user, withKey(lunchBody(catID), uuid))
	if rec.Code != http.StatusCreated {
		t.Fatalf("a crypto.randomUUID() key was rejected: status = %d, body = %s",
			rec.Code, rec.Body.String())
	}
	if k := storedKey(t, h, idOf(t, decodeObject(t, rec))); !k.Valid || k.String != uuid {
		t.Errorf("stored key = %#v, want %q", k, uuid)
	}
}

// TestCreateTransaction_NoClientKeyBehavesAsBefore pins the compatibility floor.
// A service worker can serve a bundle predating this field indefinitely, so a
// keyless create must behave exactly as it did: it stores NULL, and — because
// the unique index is partial — any number of keyless rows coexist. A
// non-partial index would make the SECOND keyless create fail, breaking every
// stale client at once.
func TestCreateTransaction_NoClientKeyBehavesAsBefore(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	first := postCreate(t, h, user, lunchBody(catID))
	if first.Code != http.StatusCreated {
		t.Fatalf("keyless create: status = %d, body = %s", first.Code, first.Body.String())
	}
	id := idOf(t, decodeObject(t, first))
	if k := storedKey(t, h, id); k.Valid {
		t.Errorf("keyless create stored idempotency_key = %q, want NULL", k.String)
	}

	// Byte-identical keyless resubmission: no key, so no protection, so a second
	// row — the pre-existing behaviour, unchanged.
	second := postCreate(t, h, user, lunchBody(catID))
	if second.Code != http.StatusCreated {
		t.Fatalf("second keyless create: status = %d, body = %s", second.Code, second.Body.String())
	}
	if n := countTransactionRows(t, h); n != 2 {
		t.Fatalf("transactions rows = %d, want 2 — keyless creates must not "+
			"collide with each other", n)
	}

	// An explicitly empty key is the same thing as an absent one: some clients
	// build the payload field-by-field and send "" when they have nothing.
	third := postCreate(t, h, user, withKey(lunchBody(catID), ""))
	if third.Code != http.StatusCreated {
		t.Fatalf("empty-key create: status = %d, body = %s", third.Code, third.Body.String())
	}
	if k := storedKey(t, h, idOf(t, decodeObject(t, third))); k.Valid {
		t.Errorf("empty client_key stored %q, want NULL", k.String)
	}
	if n := countTransactionRows(t, h); n != 3 {
		t.Errorf("transactions rows = %d, want 3", n)
	}
}

// TestCreateTransaction_ConcurrentSameKeyYieldsOneRow pins that the collision is
// resolved by the database, not by a pre-check. Two devices draining the same
// offline queue (or a double-tap on a slow connection) submit the same key at
// once; a SELECT-then-INSERT would let both observe "key free" and both insert.
//
// Not probabilistic, unlike the content_hash race next door: whichever order the
// pool serialises these in, every caller after the first hits a key that is
// already claimed. Removing the recovery turns those into 500s on every run.
//
// All callers must be handed the SAME id — a caller that got a 2xx pointing at
// nothing, or at a different row, has been told a comforting lie.
func TestCreateTransaction_ConcurrentSameKeyYieldsOneRow(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	const rounds = 4
	const workers = 8

	for round := 0; round < rounds; round++ {
		body := lunchBody(catID)
		body["description"] = fmt.Sprintf("Lunch %d", round)
		// Padded to clear MinClientKeyLength — a real client mints a UUID, and
		// the shape guard rejects anything shorter than 16 characters.
		body = withKey(body, fmt.Sprintf("shared-key-round-%08d", round))

		recs := make([]*httptest.ResponseRecorder, workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				recs[i] = postCreate(t, h, user, body)
			}(i)
		}
		close(start)
		wg.Wait()

		var wantID int64
		for i, rec := range recs {
			if rec.Code < 200 || rec.Code > 299 {
				t.Fatalf("round %d worker %d: status = %d, want 2xx; body = %s",
					round, i, rec.Code, rec.Body.String())
			}
			id := idOf(t, decodeObject(t, rec))
			if i == 0 {
				wantID = id
				continue
			}
			if id != wantID {
				t.Errorf("round %d worker %d: id = %d, want %d — concurrent "+
					"submissions of one key resolved to different rows", round, i, id, wantID)
			}
		}
	}

	if n := countTransactionRows(t, h); n != rounds {
		t.Fatalf("transactions rows = %d, want %d — a concurrent replay was stored",
			n, rounds)
	}
}

// TestCreateTransaction_ReplayAfterSoftDeleteDoesNotResurrect covers the case
// the unique index is deliberately shaped for. The user saves a transaction,
// deletes it, and only then does a queued retry of the original submission
// drain. The create did complete; the delete was a separate and LATER intent,
// so the delete stands and the retry creates nothing.
//
// This is why migration 017's index omits the `deleted_at IS NULL` predicate
// that migration 008's carries. With that predicate the tombstoned row would
// drop out of the index, the INSERT would succeed, and deleted content would
// reappear under a new id with no user action.
func TestCreateTransaction_ReplayAfterSoftDeleteDoesNotResurrect(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	body := withKey(lunchBody(catID), "cccccccc-0000-0000-0000-000000000003")

	first := postCreate(t, h, user, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", first.Code, first.Body.String())
	}
	id := idOf(t, decodeObject(t, first))

	delRec := httptest.NewRecorder()
	h.handleDeleteTransaction(delRec, withUserAndURLParam(
		httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(id), nil),
		user, "id", fmt.Sprint(id)))
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body = %s", delRec.Code, delRec.Body.String())
	}

	replay := postCreate(t, h, user, body)
	if replay.Code < 200 || replay.Code > 299 {
		t.Fatalf("replay after delete: status = %d, want 2xx; body = %s",
			replay.Code, replay.Body.String())
	}
	replayObj := decodeObject(t, replay)
	if got := idOf(t, replayObj); got != id {
		t.Errorf("replay returned id %d, want the original (tombstoned) row %d", got, id)
	}
	assertDollarsNotCents(t, replayObj, 12.50)

	if n := countTransactionRows(t, h); n != 1 {
		t.Fatalf("transactions rows = %d, want 1 — the retry inserted a new row "+
			"after the original was trashed", n)
	}

	var deletedAt sql.NullString
	if err := h.db.QueryRow(`SELECT deleted_at FROM transactions WHERE id = ?`, id).Scan(&deletedAt); err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	if !deletedAt.Valid {
		t.Error("the replay cleared the tombstone — a retry must not undo a delete " +
			"the user made afterwards")
	}
}

// TestCreateTransaction_ReplayDoesNotRenotifyHousehold covers the post-commit
// hooks, which the audit-row count cannot see. A replay writes nothing, so the
// hooks that announce a new transaction — the activity push in particular —
// must not run either. If the replay branch were ever moved below them, the
// audit trail would still read correctly (one insert) while the other household
// member's phone buzzed a second time for a transaction that was added once.
//
// That failure is invisible from the database and is exactly the annoyance the
// key was added to remove, so it gets its own test rather than riding on the
// audit assertion in TestCreateTransaction_ReplayedKeyCreatesNothing.
func TestCreateTransaction_ReplayDoesNotRenotifyHousehold(t *testing.T) {
	q, db := setupTestDB(t)
	sender := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = sender
	user := seedTestUser(t, q, "alice", RoleMember)
	other := seedTestUser(t, q, "bob", RoleMember)
	cat := seedExpenseCat(t, q, "Groceries")
	seedPushSub(t, q, other.ID, "https://push.example/replay-other")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	body := withKey(lunchBody(cat.ID), "dddddddd-0000-0000-0000-000000000004")

	if rec := postCreate(t, h, user, body); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if n := sender.count(); n != 1 {
		t.Fatalf("pushes after the original create = %d, want 1", n)
	}

	if rec := postCreate(t, h, user, body); rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("replay: status = %d, want 2xx; body = %s", rec.Code, rec.Body.String())
	}
	if n := sender.count(); n != 1 {
		t.Errorf("pushes after the replay = %d, want 1 — the household was told "+
			"twice about one transaction", n)
	}
}

// TestCreateTransaction_ClientKeyLengthBoundaries walks both ends of the
// accepted range one character at a time. Testing the exact boundaries is what
// makes the bounds real: a test that only sends a wildly overlong key passes
// just as happily against an off-by-one, and both bounds are now load-bearing —
// the ceiling protects an indexed column, the floor keeps degenerate constants
// from claiming a namespace.
func TestCreateTransaction_ClientKeyLengthBoundaries(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Food")

	rejected := []struct {
		name string
		n    int
	}{
		{"one below the minimum", MinClientKeyLength - 1},
		{"one above the maximum", MaxClientKeyLength + 1},
	}
	for _, tc := range rejected {
		rec := postCreate(t, h, user, withKey(lunchBody(catID), strings.Repeat("k", tc.n)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s (%d chars): status = %d, want 400; body = %s",
				tc.name, tc.n, rec.Code, rec.Body.String())
		}
	}
	if n := countTransactionRows(t, h); n != 0 {
		t.Fatalf("transactions rows = %d, want 0 — a rejected request wrote a row", n)
	}

	accepted := []struct {
		name string
		n    int
	}{
		{"exactly the minimum", MinClientKeyLength},
		{"exactly the maximum", MaxClientKeyLength},
	}
	for i, tc := range accepted {
		key := strings.Repeat("abcdefghij", 10)[:tc.n]
		body := lunchBody(catID)
		// Vary the content so these two creates cannot collide on content_hash;
		// this test is about the key bounds, not the hash recovery.
		body["description"] = fmt.Sprintf("Lunch %d", i)
		rec := postCreate(t, h, user, withKey(body, key))
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s (%d chars) was rejected: status = %d, body = %s",
				tc.name, tc.n, rec.Code, rec.Body.String())
		}
		if k := storedKey(t, h, idOf(t, decodeObject(t, rec))); !k.Valid || k.String != key {
			t.Errorf("%s: stored key = %#v, want %q", tc.name, k, key)
		}
	}
}
