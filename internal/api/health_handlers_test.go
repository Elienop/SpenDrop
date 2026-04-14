package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// The tests in this file cover the Phase 2.2 public data-aware health
// endpoint: handleHealthzData and its two private helpers
// (latestSchemaVersion, latestTransactionWriteAt).
//
// Invariants worth defending:
//
//  1. The endpoint is intentionally UNAUTHENTICATED — monitoring
//     scrapers on the Docker network must reach it without a session.
//     There is no auth.GetUser gate inside the handler, and the router
//     registers the route OUTSIDE the `/api` auth-protected block.
//
//  2. Status flips to "degraded" + HTTP 503 when any sub-check fails.
//     The current sub-checks that can flip it are:
//       - quick_check != "ok" (or its underlying query errored)
//       - cached full integrity result is non-empty AND not "ok"
//       - count/timestamp queries errored (rarer, but exercised via the
//         helper-function unit tests)
//     A NEVER-CHECKED cached result (zero time, empty string) must NOT
//     flip the status — that is the legitimate pre-startup-check state.
//
//  3. Nullable timestamps (LastWriteAt, LastIntegrityCheckAt) are
//     emitted via `omitempty` and must be absent from the JSON when
//     the underlying value is zero. Dashboards key off field presence.

// --- handleHealthzData: happy path ---

func TestHandleHealthzData_HappyPath_Returns200WithOk(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	// Seed the cached integrity result as if the startup check had
	// just completed successfully. Without this call the "never ran"
	// zero state is legitimate but makes assertions on the
	// last_integrity_check_at field ambiguous.
	now := time.Now().UTC()
	h.SetIntegrityResult(now, database.IntegrityOK)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)

	if resp.Status != "ok" {
		t.Errorf("Status=%q, want %q", resp.Status, "ok")
	}
	if resp.QuickCheck != database.IntegrityOK {
		t.Errorf("QuickCheck=%q, want %q", resp.QuickCheck, database.IntegrityOK)
	}
	if resp.LastIntegrityCheckResult != database.IntegrityOK {
		t.Errorf("LastIntegrityCheckResult=%q, want %q", resp.LastIntegrityCheckResult, database.IntegrityOK)
	}
	// SchemaVersion is populated only if migrations recorded a version.
	// RunMigrations inside setupTestDB always applies at least the
	// initial migration, so this must be non-empty.
	if resp.SchemaVersion == "" {
		t.Error("SchemaVersion is empty; want most-recent migration filename")
	}
	if resp.LastIntegrityCheckAt == nil {
		t.Error("LastIntegrityCheckAt is nil; want the seeded timestamp")
	} else if !resp.LastIntegrityCheckAt.Equal(now) {
		t.Errorf("LastIntegrityCheckAt=%v, want %v", resp.LastIntegrityCheckAt, now)
	}
}

// --- status flip: cached integrity result is non-ok ---

func TestHandleHealthzData_DegradedFromCachedIntegrity_Returns503(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	// Simulate a corrupt database as seen by the daily ticker: the
	// pragma returned a multi-line error string. The handler must
	// surface this verbatim AND flip the HTTP status to 503.
	corrupt := "*** in database main ***\nPage 12: btreeInitPage() returns error code 11"
	h.SetIntegrityResult(time.Now().UTC(), corrupt)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503; body=%s", rec.Code, rec.Body.String())
	}

	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)
	if resp.Status != "degraded" {
		t.Errorf("Status=%q, want %q", resp.Status, "degraded")
	}
	if resp.LastIntegrityCheckResult != corrupt {
		t.Errorf("LastIntegrityCheckResult=%q, want verbatim corrupt string", resp.LastIntegrityCheckResult)
	}
	// quick_check is independent and should still be "ok" — in-memory
	// fresh DB cannot be corrupted by merely stamping the cache.
	if resp.QuickCheck != database.IntegrityOK {
		t.Errorf("QuickCheck=%q, want %q (the in-memory DB is healthy)", resp.QuickCheck, database.IntegrityOK)
	}
}

// --- status flip: zero-state integrity must NOT degrade ---

func TestHandleHealthzData_NeverCheckedIntegrity_StaysOk(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	// Deliberately do NOT call SetIntegrityResult. The zero value
	// ("", time.Time{}) represents "main.go has not yet pushed a
	// result", which is common in tests and during the narrow window
	// between DB open and the startup check returning. The handler
	// must treat the empty string as neutral — a non-"ok" string is
	// what flips degraded, and "" is not non-"ok" in that sense.

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Capture the raw body once — decoding into the typed struct below
	// would consume rec.Body and leave the subsequent Unmarshal with
	// an empty buffer.
	bodyBytes := rec.Body.Bytes()

	var resp healthzDataResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		t.Fatalf("typed decode: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("Status=%q, want %q", resp.Status, "ok")
	}
	// QuickCheck runs synchronously on every request — pin it to
	// IntegrityOK independently from Status so a future regression
	// that lets quick_check return a non-ok string while Status
	// somehow stays "ok" cannot slip through this test. The two
	// axes flip the response together today but the test should not
	// rely on that coupling.
	if resp.QuickCheck != database.IntegrityOK {
		t.Errorf("QuickCheck=%q, want %q", resp.QuickCheck, database.IntegrityOK)
	}
	if resp.LastIntegrityCheckResult != "" {
		t.Errorf("LastIntegrityCheckResult=%q, want empty", resp.LastIntegrityCheckResult)
	}
	// LastIntegrityCheckAt must be absent from the JSON — the pointer
	// is nil and the field carries `omitempty`. Re-decode the raw
	// body into a generic map so we can assert the key is missing
	// rather than just "zero".
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("raw decode: %v", err)
	}
	if _, present := raw["last_integrity_check_at"]; present {
		t.Error("last_integrity_check_at present in JSON; want omitted when zero")
	}
}

// --- transaction counts: split live vs deleted ---

func TestHandleHealthzData_TransactionCountsSplit(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	// Two live, one tombstoned. Uses distinctive amounts so a
	// regression that accidentally swaps live/deleted shows up in
	// counts rather than being masked by equal-size buckets.
	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "a")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "b")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-03", 999.0, "gone")

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)
	if resp.TransactionsLive != 2 {
		t.Errorf("TransactionsLive=%d, want 2", resp.TransactionsLive)
	}
	if resp.TransactionsDeleted != 1 {
		t.Errorf("TransactionsDeleted=%d, want 1", resp.TransactionsDeleted)
	}
}

// --- last_write_at: populated after any write ---

func TestHandleHealthzData_LastWriteAt_PopulatedAfterWrite(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	before := time.Now().UTC().Add(-2 * time.Second)
	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 42.0, "anchor")
	after := time.Now().UTC().Add(2 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)
	if resp.LastWriteAt == nil {
		t.Fatal("LastWriteAt is nil; want a populated timestamp after seedTestTransaction")
	}
	// The seeded row's updated_at should fall inside the bracketed
	// window around the call. We compare via time.Time.Before/After so
	// the test is robust to the DB's clock granularity (SQLite uses
	// second precision for CURRENT_TIMESTAMP).
	got := *resp.LastWriteAt
	if got.Before(before) || got.After(after) {
		t.Errorf("LastWriteAt=%v outside window [%v, %v]", got, before, after)
	}
}

// --- last_write_at: empty DB omits the field ---

func TestHandleHealthzData_LastWriteAt_EmptyDBOmitsField(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Capture body bytes once: decoding consumes the buffer, so a
	// second Unmarshal off rec.Body.Bytes() would see an empty slice.
	bodyBytes := rec.Body.Bytes()

	// Re-decode into a generic map so we can assert the key is
	// missing, not just "zero-valued".
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("raw decode: %v", err)
	}
	if _, present := raw["last_write_at"]; present {
		t.Error("last_write_at present in JSON; want omitted for empty DB")
	}

	// Sanity check: the strongly-typed response also sees a nil
	// pointer, so the omitempty tag is doing its job.
	var resp healthzDataResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		t.Fatalf("typed decode: %v", err)
	}
	if resp.LastWriteAt != nil {
		t.Errorf("LastWriteAt=%v, want nil for empty DB", resp.LastWriteAt)
	}
}

// --- schema_version: reflects most-recent migration ---

func TestHandleHealthzData_SchemaVersion_MatchesAppliedMigration(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)

	// Cross-check the reported version against a direct query on
	// schema_migrations. The test doesn't hard-code the exact
	// filename — new migrations are added by later PRs — but it
	// asserts the reported value equals whatever the DB considers
	// the latest.
	var want string
	if err := db.QueryRow(
		`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`,
	).Scan(&want); err != nil {
		t.Fatalf("query latest schema_migrations: %v", err)
	}
	if resp.SchemaVersion != want {
		t.Errorf("SchemaVersion=%q, want %q", resp.SchemaVersion, want)
	}
	// Belt and braces: a reported empty string would mean the
	// helper silently absorbed an ErrNoRows-style fallback on a DB
	// that clearly has migrations applied.
	if resp.SchemaVersion == "" {
		t.Error("SchemaVersion is empty; want non-empty after RunMigrations")
	}
}

// --- helper: latestSchemaVersion directly ---

func TestLatestSchemaVersion_PopulatedDB_ReturnsMostRecent(t *testing.T) {
	_, db := setupTestDB(t)

	got, err := latestSchemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("latestSchemaVersion: %v", err)
	}
	if got == "" {
		t.Error("latestSchemaVersion returned empty; want non-empty after migrations")
	}
}

// --- helper: latestTransactionWriteAt ---

func TestLatestTransactionWriteAt_EmptyTable_ReturnsZeroTime(t *testing.T) {
	_, db := setupTestDB(t)

	got, err := latestTransactionWriteAt(context.Background(), db)
	if err != nil {
		t.Fatalf("latestTransactionWriteAt: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("latestTransactionWriteAt=%v, want zero time on empty table", got)
	}
}

func TestLatestTransactionWriteAt_PopulatedTable_ReturnsMax(t *testing.T) {
	q, db := setupTestDB(t)
	user := seedTestUser(t, q, "alice", "admin")

	before := time.Now().UTC().Add(-2 * time.Second)
	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "a")
	after := time.Now().UTC().Add(2 * time.Second)

	got, err := latestTransactionWriteAt(context.Background(), db)
	if err != nil {
		t.Fatalf("latestTransactionWriteAt: %v", err)
	}
	if got.IsZero() {
		t.Fatal("latestTransactionWriteAt returned zero after seeded write")
	}
	// SQLite's CURRENT_TIMESTAMP is second-precision; compare via
	// the same bracketed window pattern used in the handler test.
	if got.Before(before) || got.After(after) {
		t.Errorf("latestTransactionWriteAt=%v outside window [%v, %v]", got, before, after)
	}
}

func TestLatestTransactionWriteAt_IncludesTombstonedRows(t *testing.T) {
	q, db := setupTestDB(t)
	user := seedTestUser(t, q, "alice", "admin")

	// The helper is deliberately insensitive to deleted_at — it is a
	// "did the application write anything recently?" signal, not a
	// "live row" signal. A DB whose only remaining rows are in the
	// trash still has a measurable last-write time.
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-01", 999.0, "gone")

	got, err := latestTransactionWriteAt(context.Background(), db)
	if err != nil {
		t.Fatalf("latestTransactionWriteAt: %v", err)
	}
	if got.IsZero() {
		t.Error("latestTransactionWriteAt=zero; want populated timestamp from tombstoned row")
	}
}

// --- content-type is application/json ---

func TestHandleHealthzData_ContentTypeIsJSON(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type=%q, want application/json", ct)
	}
}

// --- router-level: public, no auth required ---

func TestNewRouter_HealthzData_Public_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	// No session cookie, no auth middleware in the way. The endpoint
	// must route all the way to the handler and respond with a
	// populated body.
	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp healthzDataResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("Status=%q, want %q", resp.Status, "ok")
	}
	// A fresh router-wired DB has no cached integrity result because
	// NewRouter does not call SetIntegrityResult — only production
	// main.go does. That zero state must not degrade the endpoint.
	if resp.LastIntegrityCheckResult != "" {
		t.Errorf("LastIntegrityCheckResult=%q, want empty in router-only wiring", resp.LastIntegrityCheckResult)
	}
}

// --- router-level: NewRouterWithHandler returns a Handler that the
//                   caller can seed via SetIntegrityResult, which
//                   subsequent /healthz/data requests must observe.

func TestNewRouterWithHandler_SeededIntegrityResult_IsReflectedInHealthz(t *testing.T) {
	q, db := setupTestDB(t)
	router, h := NewRouterWithHandler(q, db, nil)

	now := time.Now().UTC().Truncate(time.Second)
	h.SetIntegrityResult(now, database.IntegrityOK)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp healthzDataResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.LastIntegrityCheckResult != database.IntegrityOK {
		t.Errorf("LastIntegrityCheckResult=%q, want %q", resp.LastIntegrityCheckResult, database.IntegrityOK)
	}
	if resp.LastIntegrityCheckAt == nil || !resp.LastIntegrityCheckAt.Equal(now) {
		t.Errorf("LastIntegrityCheckAt=%v, want %v", resp.LastIntegrityCheckAt, now)
	}
}

// --- router-level: the endpoint is registered as GET only ---
//
// Not strictly a contract of the handler itself, but documenting that
// /healthz/data is a read-only endpoint. A future PR that accidentally
// registers a POST binding at the same path would surface as a
// behaviour change here. Chi's default response to an unregistered
// method on a registered path is 405; we assert that shape rather
// than the exact status code so chi version bumps cannot regress the
// test for no good reason.

func TestNewRouter_HealthzData_POST_DoesNotInvokeHandler(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	req := httptest.NewRequest(http.MethodPost, "/healthz/data", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Anything in the 2xx range means POST was accepted — that is
	// the regression we are guarding against. 404 or 405 are both
	// acceptable "this isn't a POST endpoint" responses.
	if rec.Code >= 200 && rec.Code < 300 {
		t.Errorf("status=%d, want non-2xx (POST must not be accepted); body=%s", rec.Code, rec.Body.String())
	}
}
