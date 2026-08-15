package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// B6j: the transactions list carries the creator's DISPLAY NAME so a member can
// tell their own rows from the rest of the household's. The list is
// deliberately household-wide and GET /users is admin-only, so before this a
// member found out a row was somebody else's only when Save returned 403.
//
// The column choice is load-bearing and every test below seeds users whose
// display_name DIFFERS from their username, so a mutant that selects
// u.username instead of u.display_name goes red rather than passing on values
// that happen to coincide. The wire key is `created_by`, not `username`,
// because the value is a display name and `username` means the login
// identifier everywhere else on the wire.
//
// Every assertion below decodes into []map[string]any rather than a typed
// struct. A typed decode zero-fills a missing or renamed key and reports
// success — which is exactly how the migration-010 money regression reached
// production past a green test.

// listTransactionsRaw performs GET /transactions as the given user and returns
// the raw rows and the reported total.
func listTransactionsRaw(t *testing.T, h *Handler, user database.User) ([]map[string]any, int) {
	t.Helper()
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/transactions", nil), user)
	rec := httptest.NewRecorder()
	h.handleListTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list transactions: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode list body: %v", err)
	}
	return body.Transactions, body.Total
}

// createdByOf pulls the attribution field off a raw row, failing loudly when
// the key is absent — the failure mode a typed decode would hide.
func createdByOf(t *testing.T, row map[string]any) string {
	t.Helper()
	raw, present := row["created_by"]
	if !present {
		t.Fatalf("row is missing the created_by key entirely: %#v", row)
	}
	name, ok := raw.(string)
	if !ok {
		t.Fatalf("created_by must be a string, got %T (%#v)", raw, raw)
	}
	return name
}

// seedNamedUser creates a user whose display_name deliberately differs from
// their username. seedTestUser sets both to the same string, which would make
// the display_name-vs-username column choice untestable.
func seedNamedUser(t *testing.T, q *database.Queries, username, displayName, role string) database.User {
	t.Helper()
	hash, err := auth.HashPassword("testpassword")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := q.CreateUser(context.Background(), database.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		DisplayName:  displayName,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	if user.DisplayName == user.Username {
		t.Fatalf("fixture must differ: display_name %q == username %q", user.DisplayName, user.Username)
	}
	return user
}

func rowByID(t *testing.T, rows []map[string]any, id int64) map[string]any {
	t.Helper()
	for _, row := range rows {
		if n, ok := row["id"].(float64); ok && int64(n) == id {
			return row
		}
	}
	t.Fatalf("row %d is not in the list response: %#v", id, rows)
	return nil
}

// TestHandleListTransactions_CarriesCreatorDisplayName is the core contract:
// two different creators, two different names, visible to a MEMBER who owns
// only one of the rows. Nothing about ownership changes — this is display only.
//
// Both users carry a display_name that differs from their username, so the
// assertions below fail if the query ever selects the wrong column.
func TestHandleListTransactions_CarriesCreatorDisplayName(t *testing.T) {
	h := setupHandler(t)
	owner := seedNamedUser(t, h.queries, "elienop", "Elie", RoleAdmin)
	member := seedNamedUser(t, h.queries, "partner", "Partner Name", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	ownerRow := seedTestTransaction(t, h.queries, owner.ID, catID, "2026-03-01", 40, "Supermarket")
	memberRow := seedTestTransaction(t, h.queries, member.ID, catID, "2026-03-02", 12, "Bakery")

	// Read as the MEMBER: they cannot call GET /users, so the list response is
	// the only place this name can come from.
	rows, total := listTransactionsRaw(t, h, member)
	if total != 2 {
		t.Fatalf("expected 2 rows, got %d", total)
	}

	if got := createdByOf(t, rowByID(t, rows, ownerRow.ID)); got != "Elie" {
		t.Errorf("row created by the admin: created_by = %q, want the display name %q (username is %q)",
			got, "Elie", "elienop")
	}
	if got := createdByOf(t, rowByID(t, rows, memberRow.ID)); got != "Partner Name" {
		t.Errorf("row created by the member: created_by = %q, want the display name %q (username is %q)",
			got, "Partner Name", "partner")
	}

	// The two rows must not report the same name — a mutant that hard-coded
	// the requesting user's own name would otherwise pass the pair above.
	if createdByOf(t, rowByID(t, rows, ownerRow.ID)) == createdByOf(t, rowByID(t, rows, memberRow.ID)) {
		t.Error("both rows report the same creator; attribution is not per-row")
	}
}

// TestHandleListTransactions_DeletedCreatorRendersEmptyName covers the
// orphan case. transactions.user_id is NOT NULL REFERENCES users(id) ON DELETE
// CASCADE, so this cannot arise while foreign keys are enforced — but a
// restored backup or a connection that lost _foreign_keys=on can produce it.
// The row must still be listed, with an empty creator and no error.
func TestHandleListTransactions_DeletedCreatorRendersEmptyName(t *testing.T) {
	h := setupHandler(t)
	reader := seedNamedUser(t, h.queries, "reader", "Reader Name", RoleMember)
	ghost := seedNamedUser(t, h.queries, "ghost", "Ghost Name", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	orphan := seedTestTransaction(t, h.queries, ghost.ID, catID, "2026-03-01", 40, "Legacy row")
	live := seedTestTransaction(t, h.queries, reader.ID, catID, "2026-03-02", 12, "Current row")

	// Drop the creator WITHOUT the cascade, reproducing a legacy/restored DB.
	deleteUserWithoutCascade(t, h, ghost.ID)

	rows, total := listTransactionsRaw(t, h, reader)
	if total != 2 {
		t.Fatalf("the orphaned row must still be listed: total = %d, want 2", total)
	}
	if len(rows) != 2 {
		t.Fatalf("the orphaned row must still be returned: got %d rows, want 2", len(rows))
	}
	if got := createdByOf(t, rowByID(t, rows, orphan.ID)); got != "" {
		t.Errorf("orphaned row: created_by = %q, want the empty string", got)
	}
	if got := createdByOf(t, rowByID(t, rows, live.ID)); got != "Reader Name" {
		t.Errorf("live row: created_by = %q, want %q", got, "Reader Name")
	}
}

// TestHandleListTransactions_TotalMatchesReturnedRows guards the split between
// the count query (no users join) and the data query (LEFT JOIN users). Turning
// that LEFT JOIN into an inner one drops the orphaned row from the data while
// the count still includes it — money would go missing from the ledger while
// the header kept claiming it was there.
func TestHandleListTransactions_TotalMatchesReturnedRows(t *testing.T) {
	h := setupHandler(t)
	reader := seedNamedUser(t, h.queries, "reader", "Reader Name", RoleMember)
	ghost := seedNamedUser(t, h.queries, "ghost", "Ghost Name", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	seedTestTransaction(t, h.queries, ghost.ID, catID, "2026-03-01", 40, "Legacy row")
	seedTestTransaction(t, h.queries, reader.ID, catID, "2026-03-02", 12, "Current row")
	deleteUserWithoutCascade(t, h, ghost.ID)

	rows, total := listTransactionsRaw(t, h, reader)
	if total != len(rows) {
		t.Errorf("total = %d but %d rows returned — the count and data queries have different scopes",
			total, len(rows))
	}
}

// TestHandleListTransactions_AttributionHidesTombstoned is the CLAUDE.md
// soft-delete invariant on the new join: adding users to the FROM clause must
// not resurrect a tombstoned row. The sentinel amount makes a leak visible as
// money, not just as a row count.
func TestHandleListTransactions_AttributionHidesTombstoned(t *testing.T) {
	h := setupHandler(t)
	owner := seedNamedUser(t, h.queries, "elienop", "Elie", RoleAdmin)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	live := seedTestTransaction(t, h.queries, owner.ID, catID, "2026-03-01", 40, "Supermarket")
	seedTombstonedTestTransaction(t, h.queries, owner.ID, catID, "2026-03-02", 999, "Deleted row")

	rows, total := listTransactionsRaw(t, h, owner)
	if total != 1 || len(rows) != 1 {
		t.Fatalf("tombstoned row leaked: total = %d, rows = %d", total, len(rows))
	}
	if got := createdByOf(t, rowByID(t, rows, live.ID)); got != "Elie" {
		t.Errorf("live row: created_by = %q, want %q", got, "Elie")
	}
	for _, row := range rows {
		if amt, ok := row["amount"].(float64); ok && amt == 999 {
			t.Error("the 999 sentinel amount reached the response — a tombstoned row was joined in")
		}
	}
}

// TestHandleCreateTransaction_ReturnsCreatorDisplayName pins the other emit
// site. The create response has no join to draw on, so the attribution comes
// from the authenticated user's STORED DisplayName — who owns the row by
// construction. Sourcing it from the session user rather than the request body
// is what keeps a client from claiming someone else's name.
func TestHandleCreateTransaction_ReturnsCreatorDisplayName(t *testing.T) {
	h := setupHandler(t)
	user := seedNamedUser(t, h.queries, "elienop", "Elie", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	body := strings.NewReader(`{
		"date": "2026-03-01",
		"amount": 40.00,
		"description": "Supermarket",
		"category_id": ` + itoa(catID) + `
	}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", body), user)
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if got := createdByOf(t, resp); got != "Elie" {
		t.Errorf("create response: created_by = %q, want the display name %q (username is %q)",
			got, "Elie", "elienop")
	}
}

// deleteUserWithoutCascade removes a users row while leaving that user's
// transactions behind, which is only possible with foreign keys disabled. The
// pool is capped at one connection (setupTestDB), so the PRAGMA applies to the
// same connection the DELETE runs on.
func deleteUserWithoutCascade(t *testing.T, h *Handler, userID int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.db.ExecContext(ctx, "PRAGMA foreign_keys=off"); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := h.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", userID); err != nil {
		t.Fatalf("delete user %d: %v", userID, err)
	}
	if _, err := h.db.ExecContext(ctx, "PRAGMA foreign_keys=on"); err != nil {
		t.Fatalf("re-enable foreign keys: %v", err)
	}

	// The whole point of the fixture is an orphaned transaction. If the
	// cascade fired anyway the test below would pass for the wrong reason.
	var orphans int
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions t LEFT JOIN users u ON t.user_id = u.id WHERE u.id IS NULL`,
	).Scan(&orphans); err != nil {
		t.Fatalf("count orphaned transactions: %v", err)
	}
	if orphans == 0 {
		t.Fatal("fixture did not produce an orphaned transaction — the cascade fired despite PRAGMA foreign_keys=off")
	}
}

// TestHandleBatchCreateTransactions_ReturnsCreatorDisplayName pins the third
// emit site. The deep review's surviving mutant proved why an assertion is
// needed and a signature is not: toTransactionResponse's explicit createdBy
// parameter makes every call site pass SOMETHING, but only a test checks WHAT.
// Batch-create shipped "" under mutation with the whole suite green until this
// test existed. No web/src code calls this endpoint today; the API-token
// surface can, and TransactionRow renders "" as "Unknown".
func TestHandleBatchCreateTransactions_ReturnsCreatorDisplayName(t *testing.T) {
	h := setupHandler(t)
	user := seedNamedUser(t, h.queries, "elienop", "Elie", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	body := strings.NewReader(`[
		{"date": "2026-03-01", "amount": 12.00, "description": "Batch row one", "category_id": ` + itoa(catID) + `},
		{"date": "2026-03-02", "amount": 7.50, "description": "Batch row two", "category_id": ` + itoa(catID) + `}
	]`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body), user)
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	if len(resp) != 2 {
		t.Fatalf("expected 2 created rows in the response, got %d", len(resp))
	}
	for i, row := range resp {
		got, ok := row["created_by"].(string)
		if !ok {
			t.Errorf("batch row %d is missing the created_by key entirely: %v", i, row)
			continue
		}
		if got != "Elie" {
			t.Errorf("batch row %d: created_by = %q, want the display name %q (username is %q)",
				i, got, "Elie", "elienop")
		}
	}
}

// B36: the display name alone cannot identify a person. Self-service rename
// lets a member PATCH their display_name to the exact string another member
// uses, and because created_by comes from a live JOIN the relabel applies
// retroactively to every row they ever entered. The fix is not server-side
// display-name uniqueness — that error leaks the SET of existing names to any
// member who probes for it — but rendering @username beside the name, which
// needs the username ON THE WIRE: GET /api/users is admin-only, so a member
// cannot resolve user_id client-side.
//
// Every test below seeds users whose display_name AND username differ, from
// each other and between users, so a query that selected the same column twice
// or swapped the two goes red instead of passing on coinciding values.

// createdByUsernameOf pulls the username attribution off a raw row, failing
// loudly when the key is absent — the failure a typed decode would zero-fill
// into a passing test.
func createdByUsernameOf(t *testing.T, row map[string]any) string {
	t.Helper()
	raw, present := row["created_by_username"]
	if !present {
		t.Fatalf("row is missing the created_by_username key entirely: %#v", row)
	}
	name, ok := raw.(string)
	if !ok {
		t.Fatalf("created_by_username must be a string, got %T (%#v)", raw, raw)
	}
	return name
}

// assertAttribution checks both attribution fields on one row at once. Doing
// them as a pair is the point: the two values must never be sourced from the
// same column, and the mismatch check below is what catches a query that
// selected u.display_name twice.
func assertAttribution(t *testing.T, where string, row map[string]any, wantName, wantUsername string) {
	t.Helper()
	if got := createdByOf(t, row); got != wantName {
		t.Errorf("%s: created_by = %q, want the display name %q", where, got, wantName)
	}
	if got := createdByUsernameOf(t, row); got != wantUsername {
		t.Errorf("%s: created_by_username = %q, want the username %q", where, got, wantUsername)
	}
	if wantName != wantUsername && createdByOf(t, row) == createdByUsernameOf(t, row) {
		t.Errorf("%s: both fields report %q — one column is feeding both",
			where, createdByOf(t, row))
	}
}

// TestHandleListTransactions_CarriesCreatorUsername is the core contract for
// the list: two creators, four distinct strings, read by a MEMBER who owns one
// of the two rows. The member is the whole reason the field exists — an admin
// could already resolve every username through GET /api/users.
func TestHandleListTransactions_CarriesCreatorUsername(t *testing.T) {
	h := setupHandler(t)
	owner := seedNamedUser(t, h.queries, "elienop", "Elie", RoleAdmin)
	member := seedNamedUser(t, h.queries, "partner", "Partner Name", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	ownerRow := seedTestTransaction(t, h.queries, owner.ID, catID, "2026-03-01", 40, "Supermarket")
	memberRow := seedTestTransaction(t, h.queries, member.ID, catID, "2026-03-02", 12, "Bakery")

	rows, total := listTransactionsRaw(t, h, member)
	if total != 2 {
		t.Fatalf("expected 2 rows, got %d", total)
	}

	assertAttribution(t, "admin's row", rowByID(t, rows, ownerRow.ID), "Elie", "elienop")
	assertAttribution(t, "member's row", rowByID(t, rows, memberRow.ID), "Partner Name", "partner")

	// Per-row, not per-request: a mutant that stamped the READER's own username
	// on everything passes both assertions above only if the two rows agree.
	if createdByUsernameOf(t, rowByID(t, rows, ownerRow.ID)) ==
		createdByUsernameOf(t, rowByID(t, rows, memberRow.ID)) {
		t.Error("both rows report the same creator username; attribution is not per-row")
	}
}

// TestHandleListTransactions_DeletedCreatorRendersEmptyUsername is the presence
// half of the contract: the key is ALWAYS emitted, and an absent creator makes
// it the empty string rather than making it disappear. transactions.user_id is
// NOT NULL REFERENCES users(id) ON DELETE CASCADE, so an orphan needs a
// restored backup or a connection that lost _foreign_keys=on — under an INNER
// join the row would vanish from the ledger entirely.
func TestHandleListTransactions_DeletedCreatorRendersEmptyUsername(t *testing.T) {
	h := setupHandler(t)
	reader := seedNamedUser(t, h.queries, "reader", "Reader Name", RoleMember)
	ghost := seedNamedUser(t, h.queries, "ghost", "Ghost Name", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	orphan := seedTestTransaction(t, h.queries, ghost.ID, catID, "2026-03-01", 40, "Legacy row")
	live := seedTestTransaction(t, h.queries, reader.ID, catID, "2026-03-02", 12, "Current row")

	deleteUserWithoutCascade(t, h, ghost.ID)

	rows, total := listTransactionsRaw(t, h, reader)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("the orphaned row must still be listed: total = %d, rows = %d, want 2 and 2",
			total, len(rows))
	}

	// Both fields empty TOGETHER — they come off one join, so a row can never
	// report a name without an identifier or the reverse.
	orphanRow := rowByID(t, rows, orphan.ID)
	if got := createdByUsernameOf(t, orphanRow); got != "" {
		t.Errorf("orphaned row: created_by_username = %q, want the empty string", got)
	}
	if got := createdByOf(t, orphanRow); got != "" {
		t.Errorf("orphaned row: created_by = %q, want the empty string", got)
	}
	assertAttribution(t, "live row", rowByID(t, rows, live.ID), "Reader Name", "reader")
}

// TestHandleCreateTransaction_ReturnsCreatorUsername pins the create emit site.
// There is no join here: the value comes from the authenticated user's STORED
// Username, who owns the row by construction. Sourcing it from the session
// rather than the request body is what stops a client claiming someone else's
// identifier.
func TestHandleCreateTransaction_ReturnsCreatorUsername(t *testing.T) {
	h := setupHandler(t)
	user := seedNamedUser(t, h.queries, "elienop", "Elie", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	body := strings.NewReader(`{
		"date": "2026-03-01",
		"amount": 40.00,
		"description": "Supermarket",
		"category_id": ` + itoa(catID) + `
	}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", body), user)
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	assertAttribution(t, "create response", resp, "Elie", "elienop")
}

// TestHandleCreateTransaction_IdempotentReplayCarriesCreatorUsername covers the
// FOURTH emit site, which is easy to miss: an idempotent replay returns early
// from its own writeJSON call, several hundred lines before the one the test
// above exercises. The replay must be byte-identical to the original response —
// a client retried precisely because it does not know whether the first attempt
// landed — so a field added to one branch and not the other is a real defect,
// not a cosmetic one.
func TestHandleCreateTransaction_IdempotentReplayCarriesCreatorUsername(t *testing.T) {
	h := setupHandler(t)
	user := seedNamedUser(t, h.queries, "elienop", "Elie", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	body := withKey(lunchBody(catID), "36000000-0000-0000-0000-000000000036")

	first := postCreate(t, h, user, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, body = %s", first.Code, first.Body.String())
	}
	firstObj := decodeObject(t, first)
	assertAttribution(t, "first create", firstObj, "Elie", "elienop")

	replay := postCreate(t, h, user, body)
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay: status = %d, body = %s", replay.Code, replay.Body.String())
	}
	replayObj := decodeObject(t, replay)
	assertAttribution(t, "replay", replayObj, "Elie", "elienop")

	if n := countTransactionRows(t, h); n != 1 {
		t.Fatalf("transactions rows = %d, want 1 — the replay created a second row", n)
	}
	if !reflect.DeepEqual(firstObj, replayObj) {
		t.Errorf("replay body differs from the original.\n first: %#v\nreplay: %#v", firstObj, replayObj)
	}
}

// TestHandleBatchCreateTransactions_ReturnsCreatorUsername pins the batch emit
// site. Nothing under web/src posts to this endpoint today; the API-token
// surface can, and the deep review's surviving mutant on the display-name
// version proved the point — toTransactionResponse's explicit parameters make
// every call site pass SOMETHING, but only a test checks WHAT.
func TestHandleBatchCreateTransactions_ReturnsCreatorUsername(t *testing.T) {
	h := setupHandler(t)
	user := seedNamedUser(t, h.queries, "elienop", "Elie", RoleMember)
	catID := seedExpenseCategory(t, h.queries, "Groceries-"+t.Name())

	body := strings.NewReader(`[
		{"date": "2026-03-01", "amount": 12.00, "description": "Batch row one", "category_id": ` + itoa(catID) + `},
		{"date": "2026-03-02", "amount": 7.50, "description": "Batch row two", "category_id": ` + itoa(catID) + `}
	]`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body), user)
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	if len(resp) != 2 {
		t.Fatalf("expected 2 created rows in the response, got %d", len(resp))
	}
	for i, row := range resp {
		assertAttribution(t, fmt.Sprintf("batch row %d", i), row, "Elie", "elienop")
	}
}
