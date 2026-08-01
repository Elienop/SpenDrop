package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// Tests for the invariant that an import never files a transaction under a
// category the user did not choose for it. Exactly three outcomes are
// legitimate for a row's category: the sheet's name matches one the household
// already has, the user mapped that name explicitly, or the cell was empty and
// the user chose a default. Anything else stops the import and says what is
// unresolved.
//
// Two shapes used to pass silently and each has its own test below:
//
//	1. a default was chosen and a name was left unmapped — those rows landed
//	   in the default category, counted as "imported", with nothing anywhere
//	   recording that they had been re-homed;
//	2. no default was chosen and a name was unmapped — those rows were
//	   dropped, folded into a bare `skipped` count the user could not explain.

// uploadForCategoryGate uploads a sheet and returns (importID, previewBody).
func uploadForCategoryGate(t *testing.T, h *Handler, user database.User, rows [][]string) (string, map[string]any) {
	t.Helper()
	xlsx := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, rows)
	req := postMultipartFile(t, "/api/import/upload", xlsx)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rec, &resp)
	id, _ := resp["import_id"].(string)
	if id == "" {
		t.Fatalf("no import_id in preview; body=%s", rec.Body.String())
	}
	return id, resp
}

// confirmWithChoices posts a confirm carrying the given category decisions.
func confirmWithChoices(t *testing.T, h *Handler, user database.User, importID string, defaultCategoryID int64, categoryMap map[string]int64) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"import_id": importID}
	if defaultCategoryID != 0 {
		payload["default_category_id"] = defaultCategoryID
	}
	if categoryMap != nil {
		payload["category_map"] = categoryMap
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal confirm body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportConfirm(rec, req)
	return rec
}

// ledgerRows maps every live transaction's description to its category NAME.
// The name, not the id: "did this row land under the category the user chose"
// is the invariant under test, and an id assertion reads as a number nobody
// can check against the sheet.
func ledgerRows(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`
		SELECT t.description, c.name
		FROM transactions t
		JOIN categories c ON c.id = t.category_id
		WHERE t.deleted_at IS NULL`)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var desc, cat string
		if err := rows.Scan(&desc, &cat); err != nil {
			t.Fatalf("scan ledger row: %v", err)
		}
		out[desc] = cat
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ledger: %v", err)
	}
	return out
}

// snapshotBody copies a recorder's body before anything reads it.
// decodeResponse consumes rec.Body, so a test that asserts on two different
// keys has to take the bytes once up front — otherwise the second read sees
// an empty buffer and fails for a reason that has nothing to do with the
// handler.
func snapshotBody(rec *httptest.ResponseRecorder) []byte {
	return append([]byte(nil), rec.Body.Bytes()...)
}

// decodeUnresolved pulls the unresolved_categories array off a body and
// returns it keyed by category name. Decoding into []map[string]any rather
// than a typed struct on purpose: a typed decode of a renamed or missing
// field just zero-fills and the assertion passes against nothing.
func decodeUnresolved(t *testing.T, body []byte, key string) map[string]map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, body)
	}
	raw, ok := parsed[key]
	if !ok {
		t.Fatalf("%q missing from body; raw=%s", key, body)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("%q = %T, want an array; raw=%s", key, raw, body)
	}
	out := make(map[string]map[string]any, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%q entry = %T, want an object", key, item)
		}
		name, _ := entry["name"].(string)
		out[name] = entry
	}
	return out
}

// --- Silent failure 1: a default absorbs a name the user never mapped ---

func TestHandleImportConfirm_UnmappedCategory_DoesNotFallBackToDefault(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "unmapped-default", "admin")

	// "Grocries" is a typo — it matches no seeded category. "Food" does.
	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Grocries"},
		{"2026-01-16", "Dinner out", "31.00", "Food"},
	})

	foodID := categoryIDByName(t, h, "Food")

	// A default IS chosen — which is exactly the shape that used to pass.
	// The typo'd rows landed in Food and were counted as imported.
	rec := confirmWithChoices(t, h, user, id, foodID, nil)
	raw := snapshotBody(rec)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, raw)
	}
	if code, _ := body["code"].(string); code != "UNRESOLVED_CATEGORIES" {
		t.Errorf("code=%v, want UNRESOLVED_CATEGORIES; body=%s", body["code"], raw)
	}

	entries := decodeUnresolved(t, raw, "unresolved_categories")
	if len(entries) != 1 {
		t.Fatalf("unresolved_categories has %d entries, want exactly 1 (only the typo); got %v", len(entries), entries)
	}
	entry, ok := entries["Grocries"]
	if !ok {
		t.Fatalf("no entry for %q; got %v", "Grocries", entries)
	}
	if reason, _ := entry["reason"].(string); reason != "unmapped" {
		t.Errorf("reason=%v, want unmapped", entry["reason"])
	}
	rowIDs, _ := entry["row_ids"].([]any)
	if len(rowIDs) != 1 || int(rowIDs[0].(float64)) != 0 {
		t.Errorf("row_ids=%v, want [0]", entry["row_ids"])
	}

	// The whole batch is refused, so the row that WOULD have resolved is
	// not in the ledger either — nothing is half-imported behind the user's
	// back.
	if got := ledgerRows(t, db); len(got) != 0 {
		t.Errorf("ledger=%v, want empty after a rejected confirm", got)
	}
}

func TestHandleImportConfirm_MappedCategory_LandsInTheMappedCategory(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "mapped-lands", "admin")

	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Grocries"},
	})

	foodID := categoryIDByName(t, h, "Food")
	shoppingID := categoryIDByName(t, h, "Shopping")

	// The user maps the typo to Shopping while the DEFAULT is Food. If the
	// map were ignored in favour of the default, the row would still import
	// — under the wrong category, silently. Asserting the resulting category
	// is what makes this test about the invariant rather than the status.
	rec := confirmWithChoices(t, h, user, id, foodID, map[string]int64{"Grocries": shoppingID})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got := ledgerRows(t, db)
	if got["Weekly shop"] != "Shopping" {
		t.Errorf("Weekly shop filed under %q, want Shopping; ledger=%v", got["Weekly shop"], got)
	}
}

// --- Silent failure 2: no default, so unresolvable rows were dropped ---

func TestHandleImportConfirm_MissingCategoryNoDefault_IsRefusedNotDropped(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "missing-nodefault", "admin")

	// Two rows with an EMPTY category cell and one that resolves by name.
	// With no default these two used to vanish into the skipped count.
	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", ""},
		{"2026-01-16", "Pharmacy", "18.00", ""},
		{"2026-01-17", "Dinner out", "31.00", "Food"},
	})

	rec := confirmWithChoices(t, h, user, id, 0, nil)
	raw := snapshotBody(rec)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, raw)
	}
	if code, _ := body["code"].(string); code != "UNRESOLVED_CATEGORIES" {
		t.Errorf("code=%v, want UNRESOLVED_CATEGORIES", body["code"])
	}

	entries := decodeUnresolved(t, raw, "unresolved_categories")
	entry, ok := entries[""]
	if !ok {
		t.Fatalf("no entry for the empty-cell case; got %v", entries)
	}
	if reason, _ := entry["reason"].(string); reason != "missing" {
		t.Errorf("reason=%v, want missing", entry["reason"])
	}
	rowIDs, _ := entry["row_ids"].([]any)
	if len(rowIDs) != 2 {
		t.Errorf("row_ids=%v, want the two empty-category rows", entry["row_ids"])
	}

	if got := ledgerRows(t, db); len(got) != 0 {
		t.Errorf("ledger=%v, want empty after a rejected confirm", got)
	}
}

func TestHandleImportConfirm_MissingCategoryWithDefault_Imports(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "missing-withdefault", "admin")

	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", ""},
	})

	foodID := categoryIDByName(t, h, "Food")
	rec := confirmWithChoices(t, h, user, id, foodID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// An empty cell has no name to decide about, so choosing a default IS
	// the decision for it — that path must stay open or every sheet without
	// a Category column becomes unimportable.
	if got := ledgerRows(t, db); got["Weekly shop"] != "Food" {
		t.Errorf("Weekly shop filed under %q, want Food; ledger=%v", got["Weekly shop"], got)
	}
}

// --- The common case must stay one click ---

func TestHandleImportConfirm_AllCategoriesMatch_NeedsNoDecision(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "all-match", "admin")

	id, preview := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Food"},
		{"2026-01-16", "Electric bill", "120.00", "Utilities"},
		// Case differs from the seeded "Transportation" — a case-insensitive
		// name match is a match, not a decision.
		{"2026-01-17", "Bus pass", "25.00", "transportation"},
	})

	// Nothing to decide, so the preview must not ask.
	if list, ok := preview["unresolved_categories"].([]any); !ok || len(list) != 0 {
		t.Errorf("preview unresolved_categories=%v, want empty for a file whose categories all match", preview["unresolved_categories"])
	}

	// No default, no map: the confirm a user gets by clicking Import
	// straight off a clean file.
	rec := confirmWithChoices(t, h, user, id, 0, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := ledgerRows(t, db)
	if len(got) != 3 {
		t.Fatalf("ledger has %d rows, want 3; got %v", len(got), got)
	}
	if got["Bus pass"] != "Transportation" {
		t.Errorf("Bus pass filed under %q, want Transportation", got["Bus pass"])
	}
}

// --- Scope: rows that need no decision must not demand one ---

func TestHandleImportConfirm_SkippedRowsDoNotBlockOnCategory(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "skipped-nogate", "admin")

	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Grocries"},
		{"2026-01-16", "Dinner out", "31.00", "Food"},
	})

	// Skipping the typo'd row is a legitimate resolution — it is not going
	// to be inserted, so it needs no category. Mirrors the collision gate,
	// where a group whose members are all skipped stops blocking.
	if rec := patchImportRow(t, h, user, id, 0, "skip", true); rec.Code != http.StatusOK {
		t.Fatalf("patch skip: status=%d; body=%s", rec.Code, rec.Body.String())
	} else {
		entries := decodeUnresolved(t, rec.Body.Bytes(), "unresolved_categories")
		if len(entries) != 0 {
			t.Errorf("after skipping the only undecided row, preview still lists %v", entries)
		}
	}

	rec := confirmWithChoices(t, h, user, id, 0, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := ledgerRows(t, db)
	if len(got) != 1 || got["Dinner out"] != "Food" {
		t.Errorf("ledger=%v, want only the Food row", got)
	}
}

func TestHandleImportConfirm_RowRejectedBeforeCategory_DoesNotBlock(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "junk-footer", "admin")

	// The trailing-total row every real spreadsheet has: no usable date and
	// no category. It is going to be dropped as unparseable whatever the
	// user decides, so demanding a category decision for it would block a
	// file that has nothing wrong with it.
	id, preview := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Food"},
		{"TOTAL", "Sum", "42.50", ""},
	})

	if list, ok := preview["unresolved_categories"].([]any); !ok || len(list) != 0 {
		t.Errorf("preview unresolved_categories=%v, want empty — the footer row is dropped for its date", preview["unresolved_categories"])
	}

	rec := confirmWithChoices(t, h, user, id, 0, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	decodeResponse(t, rec, &body)
	if n, _ := body["imported"].(float64); int(n) != 1 {
		t.Errorf("imported=%v, want 1", body["imported"])
	}
	reasons, _ := body["skipped_reasons"].(map[string]any)
	if n, _ := reasons["unparseable_date"].(float64); int(n) != 1 {
		t.Errorf("skipped_reasons=%v, want unparseable_date:1", body["skipped_reasons"])
	}
}

// --- Gate ordering ---

func TestHandleImportConfirm_GateOrder_LengthBeforeCategory(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "order-length", "admin")

	// Length is reported first: it is the only blocker whose remedy is an
	// edit to row content, and being told to shorten a description only
	// after editing it for something else is the round trip the ordering
	// avoids.
	//
	// The two blockers have to sit on DIFFERENT rows for the order to be
	// observable at all. An over-long row is excluded from the category
	// list — preCategorySkipReason drops it before its category is
	// consulted — so a single row carrying both only ever reports as too
	// long, whichever gate runs first. Row 0 is too long with a category
	// that resolves; row 1 is fine except for a category nothing matches.
	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", strings.Repeat("d", MaxDescriptionLength+1), "42.50", "Food"},
		{"2026-01-16", "Weekly shop", "31.00", "Grocries"},
	})

	rec := confirmWithChoices(t, h, user, id, 0, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	decodeResponse(t, rec, &body)
	if code, _ := body["code"].(string); code != "FIELD_TOO_LONG" {
		t.Errorf("code=%v, want FIELD_TOO_LONG to win over UNRESOLVED_CATEGORIES", body["code"])
	}
}

func TestHandleImportConfirm_GateOrder_CategoryBeforeCollisions(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "order-collide", "admin")

	// The collision view is a FUNCTION of the resolved category — the
	// content hash is computed from the canonical category name — so
	// reporting collisions computed against categories the user has not
	// chosen would name rows that stop colliding once the mapping lands,
	// and stay silent about rows that start.
	//
	// Again the two blockers must sit on different rows to be
	// distinguishable: a row whose category does not resolve is dropped
	// from collision grouping entirely, so it can never be both at once.
	// Seed a live row first, then upload one row that duplicates it (a
	// db_match collision, category resolves) alongside one row whose
	// category matches nothing.
	seedID, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Food"},
	})
	if rec := confirmWithChoices(t, h, user, seedID, 0, nil); rec.Code != http.StatusOK {
		t.Fatalf("seed confirm: status=%d; body=%s", rec.Code, rec.Body.String())
	}

	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Food"}, // collides with the seeded row
		{"2026-01-16", "Bakery", "8.00", "Grocries"},   // category matches nothing
	})

	foodID := categoryIDByName(t, h, "Food")
	rec := confirmWithChoices(t, h, user, id, foodID, nil)
	raw := snapshotBody(rec)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, raw)
	}
	if code, _ := body["code"].(string); code != "UNRESOLVED_CATEGORIES" {
		t.Errorf("code=%v, want UNRESOLVED_CATEGORIES to be reported before UNRESOLVED_COLLISIONS; body=%s", body["code"], raw)
	}
	// Both gates really are armed — otherwise the assertion above would
	// hold for the trivial reason that only one of them could fire.
	if _, present := body["collision_groups"]; present {
		t.Errorf("body carries collision_groups, so the collision gate ran: %s", raw)
	}
}

// --- A category id that names nothing ---

func TestHandleImportConfirm_UnknownDefaultCategoryID_Returns400(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "bogus-default", "admin")

	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", ""},
	})

	// Left unchecked this reaches processImportRows, fails the catIDToName
	// lookup, and reports "0 imported, 1 skipped" with nothing naming the id.
	rec := confirmWithChoices(t, h, user, id, 999999, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	decodeResponse(t, rec, &body)
	if code, _ := body["code"].(string); code != "UNKNOWN_CATEGORY" {
		t.Errorf("code=%v, want UNKNOWN_CATEGORY", body["code"])
	}
	ids, _ := body["unknown_category_ids"].([]any)
	if len(ids) != 1 || int(ids[0].(float64)) != 999999 {
		t.Errorf("unknown_category_ids=%v, want [999999]", body["unknown_category_ids"])
	}
}

func TestHandleImportConfirm_UnknownMappedCategoryID_Returns400(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "bogus-map", "admin")

	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Grocries"},
	})

	rec := confirmWithChoices(t, h, user, id, 0, map[string]int64{"Grocries": 888888})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	decodeResponse(t, rec, &body)
	if code, _ := body["code"].(string); code != "UNKNOWN_CATEGORY" {
		t.Errorf("code=%v, want UNKNOWN_CATEGORY", body["code"])
	}
}

// --- The preview surfaces, and the resume path ---

func TestHandleImportUpload_EmitsUnresolvedCategories(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "preview-unresolved", "admin")

	_, preview := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Grocries"},
		{"2026-01-16", "Bakery", "8.00", "Grocries"},
		{"2026-01-17", "Rent", "900.00", ""},
		{"2026-01-18", "Dinner out", "31.00", "Food"},
	})

	raw, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("re-marshal preview: %v", err)
	}
	entries := decodeUnresolved(t, raw, "unresolved_categories")
	if len(entries) != 2 {
		t.Fatalf("unresolved_categories has %d entries, want 2 (the typo and the empty cell); got %v", len(entries), entries)
	}
	typo, ok := entries["Grocries"]
	if !ok {
		t.Fatalf("no Grocries entry; got %v", entries)
	}
	if reason, _ := typo["reason"].(string); reason != "unmapped" {
		t.Errorf("Grocries reason=%v, want unmapped", typo["reason"])
	}
	// Both rows carrying the name are attributed to it — that count is the
	// difference between "one typo" and "half my ledger".
	if rowIDs, _ := typo["row_ids"].([]any); len(rowIDs) != 2 {
		t.Errorf("Grocries row_ids=%v, want both rows", typo["row_ids"])
	}
	if empty, ok := entries[""]; !ok {
		t.Errorf("no empty-cell entry; got %v", entries)
	} else if reason, _ := empty["reason"].(string); reason != "missing" {
		t.Errorf("empty-cell reason=%v, want missing", empty["reason"])
	}
}

func TestHandleImportGetSession_ResumeKeepsUnresolvedCategories(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "resume-unresolved", "admin")

	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Grocries"},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/import/"+id, nil)
	req = withUserAndURLParams(req, user, map[string]string{"importID": id})
	rec := httptest.NewRecorder()
	h.handleImportGetSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Without this the flags the preview was asking the user to act on
	// vanish on a page reload, and the gate silently stops blocking.
	entries := decodeUnresolved(t, rec.Body.Bytes(), "unresolved_categories")
	if _, ok := entries["Grocries"]; !ok {
		t.Errorf("resume dropped the unresolved category; got %v", entries)
	}
}

// --- The confirm response has to explain itself ---

func TestHandleImportConfirm_SkippedReasons_ExplainTheCount(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "skip-breakdown", "admin")

	// Seed a row so the second sheet row lands as a content-hash duplicate.
	firstID, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Food"},
	})
	if rec := confirmWithChoices(t, h, user, firstID, 0, nil); rec.Code != http.StatusOK {
		t.Fatalf("seed confirm: status=%d; body=%s", rec.Code, rec.Body.String())
	}

	id, _ := uploadForCategoryGate(t, h, user, [][]string{
		{"2026-01-15", "Weekly shop", "42.50", "Food"}, // duplicate of the seeded row
		{"2026-01-16", "Dinner out", "31.00", "Food"},  // will be user-skipped
		{"TOTAL", "Sum", "73.50", "Food"},              // unparseable date
		{"2026-01-17", "Pharmacy", "18.00", "Food"},    // the only one that lands
	})
	if rec := patchImportRow(t, h, user, id, 1, "skip", true); rec.Code != http.StatusOK {
		t.Fatalf("patch skip: status=%d; body=%s", rec.Code, rec.Body.String())
	}

	// The duplicate is a collision against a live row, so resolve it the way
	// a user would: skip it.
	if rec := patchImportRow(t, h, user, id, 0, "skip", true); rec.Code != http.StatusOK {
		t.Fatalf("patch skip dup: status=%d; body=%s", rec.Code, rec.Body.String())
	}

	rec := confirmWithChoices(t, h, user, id, 0, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	decodeResponse(t, rec, &body)
	if n, _ := body["imported"].(float64); int(n) != 1 {
		t.Errorf("imported=%v, want 1", body["imported"])
	}
	if n, _ := body["skipped"].(float64); int(n) != 3 {
		t.Errorf("skipped=%v, want 3", body["skipped"])
	}
	reasons, ok := body["skipped_reasons"].(map[string]any)
	if !ok {
		t.Fatalf("skipped_reasons=%T, want an object; body=%s", body["skipped_reasons"], rec.Body.String())
	}
	if n, _ := reasons["user_skipped"].(float64); int(n) != 2 {
		t.Errorf("skipped_reasons[user_skipped]=%v, want 2; got %v", reasons["user_skipped"], reasons)
	}
	if n, _ := reasons["unparseable_date"].(float64); int(n) != 1 {
		t.Errorf("skipped_reasons[unparseable_date]=%v, want 1; got %v", reasons["unparseable_date"], reasons)
	}
	// A reason nothing hit must not appear at all — a wall of zeroes
	// describes nothing, and the UI renders whatever it is handed.
	if _, present := reasons["duplicate"]; present {
		t.Errorf("skipped_reasons carries a zero-count reason: %v", reasons)
	}

	// The counts have to add up to the number the user is shown, or the
	// breakdown is decoration rather than an explanation.
	sum := 0
	for _, v := range reasons {
		n, _ := v.(float64)
		sum += int(n)
	}
	if skipped, _ := body["skipped"].(float64); sum != int(skipped) {
		t.Errorf("skipped_reasons sums to %d, want %v", sum, body["skipped"])
	}
}

// --- The resolver itself ---

func TestResolveCategoryID_UnmatchedNameNeverBecomesTheDefault(t *testing.T) {
	catNameToID := map[string]int64{"food": 7}

	tests := []struct {
		name        string
		cell        string
		categoryMap map[string]int64
		defaultID   int64
		want        int64
	}{
		{"empty cell takes the default", "", nil, 42, 42},
		{"empty cell with no default is undecided", "", nil, 0, 0},
		{"name matching a category resolves", "Food", nil, 42, 7},
		{"name match is case-insensitive", "  FOOD ", nil, 42, 7},
		{"explicit map wins over the default", "Grocries", map[string]int64{"Grocries": 9}, 42, 9},
		{"explicit map wins over a name match", "Food", map[string]int64{"Food": 9}, 42, 9},
		// The whole point: a name nothing matches stays undecided even with
		// a default sitting right there.
		{"unmatched name does NOT take the default", "Grocries", nil, 42, 0},
		{"unmatched name with an unrelated map entry stays undecided", "Grocries", map[string]int64{"Other": 9}, 42, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveCategoryID(tc.cell, tc.categoryMap, catNameToID, tc.defaultID)
			if got != tc.want {
				t.Errorf("resolveCategoryID(%q, %v, default=%d) = %d, want %d",
					tc.cell, tc.categoryMap, tc.defaultID, got, tc.want)
			}
		})
	}
}

func TestUnresolvedImportCategories_GroupsByExactNameInFirstSeenOrder(t *testing.T) {
	rows := []importRow{
		{RowID: 0, Date: "2026-01-15", Description: "a", Amount: 1, Category: "Zed"},
		{RowID: 1, Date: "2026-01-15", Description: "b", Amount: 1, Category: "Alpha"},
		{RowID: 2, Date: "2026-01-15", Description: "c", Amount: 1, Category: "Zed"},
		{RowID: 3, Date: "2026-01-15", Description: "d", Amount: 1, Category: "Food"},
	}
	got := unresolvedImportCategories(rows, nil, map[string]int64{"food": 7}, 0)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2; %+v", len(got), got)
	}
	// First-appearance order, not alphabetical — the sheet's own order is
	// what the user is looking at.
	if got[0].Name != "Zed" || got[1].Name != "Alpha" {
		t.Errorf("order = %q, %q; want Zed then Alpha", got[0].Name, got[1].Name)
	}
	if len(got[0].RowIDs) != 2 || got[0].RowIDs[0] != 0 || got[0].RowIDs[1] != 2 {
		t.Errorf("Zed row_ids=%v, want [0 2]", got[0].RowIDs)
	}
}
