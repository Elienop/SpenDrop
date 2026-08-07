package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// Tests for the /healthz/data write probe (backlog B7 piece 2, migration 018).
//
// The invariant the whole feature exists for: a database that has gone
// READ-ONLY must flip this endpoint to 503 on the very next scrape. Every
// other sub-check is a read and passes cleanly on such a database — that is
// precisely why the gap existed — so TestHandleHealthzData_ReadOnlyDatabase_*
// below asserts not only the 503 but that quick_check is still "ok" beside
// it. Without that second assertion the test would also pass if the read-only
// fixture had merely broken the reads, which would prove nothing about the
// probe.
//
// The happy-path test asserts the STORED MARKER MOVES rather than asserting
// the status field says "ok". A status-field assertion survives the write
// being dropped entirely; a marker assertion does not.

// steppingClock returns a new instant on every call, advancing by step. Unlike
// fixedClock it can distinguish "the handler wrote twice" from "the handler
// wrote once and the second call was a no-op", because the two writes carry
// different timestamps. Pointer receiver: Now() mutates.
type steppingClock struct {
	t    time.Time
	step time.Duration
}

func (c *steppingClock) Now() time.Time {
	now := c.t
	c.t = c.t.Add(c.step)
	return now
}

// storedProbeAt reads the single health_write_probe row. found is false when
// the table is empty, which is the legitimate never-probed state of a freshly
// migrated database.
func storedProbeAt(t *testing.T, db *sql.DB) (at time.Time, found bool) {
	t.Helper()
	err := db.QueryRow(`SELECT probed_at FROM health_write_probe WHERE id = 1`).Scan(&at)
	if err == sql.ErrNoRows {
		return time.Time{}, false
	}
	if err != nil {
		t.Fatalf("read health_write_probe: %v", err)
	}
	return at, true
}

// probeRowCount is the storage-footprint assertion: the table must hold at
// most one row no matter how many scrapes have run.
func probeRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM health_write_probe`).Scan(&n); err != nil {
		t.Fatalf("count health_write_probe: %v", err)
	}
	return n
}

// mainDBPath recovers the file backing an open handle, so a test can open a
// SECOND connection to the same database with different flags. Cheaper and
// less fragile than duplicating setupTestDB just to return its path.
func mainDBPath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var path string
	if err := db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatalf("resolve main database path: %v", err)
	}
	if path == "" {
		t.Fatal("main database path is empty; expected a file-backed test DB")
	}
	return path
}

// --- happy path: the probe actually writes, and keeps writing ---

func TestHandleHealthzData_WriteProbe_AdvancesStoredMarker(t *testing.T) {
	q, db := setupTestDB(t)
	first := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	step := 30 * time.Second
	h := NewHandlerWithClock(q, db, &steppingClock{t: first, step: step})

	scrape := func() healthzDataResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
		rec := httptest.NewRecorder()
		h.handleHealthzData(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var resp healthzDataResponse
		decodeResponse(t, rec, &resp)
		if resp.WriteProbe != writeProbeOK {
			t.Fatalf("WriteProbe=%q, want %q", resp.WriteProbe, writeProbeOK)
		}
		return resp
	}

	scrape()
	// The write probe is the FIRST h.clock.Now() call in each request —
	// later sub-checks (the checkpoint stale threshold, and the backup
	// block when wired) read the clock after it, and how many of them do
	// varies with the constructor. So at1 must equal the clock's first
	// tick, and the second scrape's marker is asserted RELATIVE to the
	// first rather than as a hard-coded multiple of step. Any new
	// sub-check that reads the clock ahead of the probe breaks the
	// at1==first assertion by design.
	at1, found := storedProbeAt(t, db)
	if !found {
		t.Fatal("health_write_probe has no row after a scrape; the probe never wrote")
	}
	if !at1.Equal(first) {
		t.Errorf("probed_at=%v after first scrape, want the clock's first instant %v", at1.UTC(), first)
	}

	scrape()
	at2, found := storedProbeAt(t, db)
	if !found {
		t.Fatal("health_write_probe row vanished after the second scrape")
	}
	if !at2.After(at1) {
		t.Errorf("probed_at did not advance across two scrapes: first=%v second=%v — the second write was skipped or silently failed", at1.UTC(), at2.UTC())
	}

	// Storage footprint: two scrapes, still exactly one row. An
	// append-only probe would read 2 here and grow without bound in
	// production.
	if n := probeRowCount(t, db); n != 1 {
		t.Errorf("health_write_probe row count=%d after two scrapes, want 1", n)
	}
}

// --- fresh database: the first scrape ever must seed and report ok ---

func TestHandleHealthzData_WriteProbe_FreshDatabase_SeedsAndReportsOk(t *testing.T) {
	q, db := setupTestDB(t)

	// Migration 018 creates the table empty and seeds nothing, so this is
	// the insert arm of the upsert being exercised, not the update arm.
	if n := probeRowCount(t, db); n != 0 {
		t.Fatalf("freshly migrated DB has %d health_write_probe rows, want 0 (the migration must not seed)", n)
	}

	h := NewHandler(q, db)
	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 on a fresh database; body=%s", rec.Code, rec.Body.String())
	}
	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)
	if resp.WriteProbe != writeProbeOK {
		t.Errorf("WriteProbe=%q, want %q", resp.WriteProbe, writeProbeOK)
	}
	if resp.Status != "ok" {
		t.Errorf("Status=%q, want %q", resp.Status, "ok")
	}
	if _, found := storedProbeAt(t, db); !found {
		t.Error("health_write_probe is still empty after the first scrape; the insert arm did not run")
	}
}

// --- the whole point: a read-only database must go 503 ---

func TestHandleHealthzData_ReadOnlyDatabase_Returns503WithWriteError(t *testing.T) {
	q, db := setupTestDB(t)
	path := mainDBPath(t, db)

	// A second handle to the SAME file with mode=ro. Everything else in the
	// DSN mirrors production (setupTestDB explains why that matters), and
	// the pool is capped at one connection for the same reason.
	//
	// mode=ro reproduces the failure precisely: SQLite answers a write with
	// "attempt to write a readonly database" — the same sentence the B2
	// restore bug put in the container log when the file ended up owned by
	// root — while leaving every read working. A chmod-based fixture would
	// be weaker: it also blocks creating the -wal/-shm files, so the reads
	// would fail too and the resulting 503 would not be attributable to the
	// write probe.
	roDB, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open read-only handle: %v", err)
	}
	roDB.SetMaxOpenConns(1)
	t.Cleanup(func() { roDB.Close() })

	// Sanity-check the fixture itself before trusting the assertions below:
	// if this write ever started succeeding, the test would silently stop
	// testing anything.
	if _, err := roDB.Exec(`UPDATE health_write_probe SET probed_at = probed_at`); err == nil {
		t.Fatal("fixture is not read-only: a write through the mode=ro handle succeeded")
	}

	h := NewHandler(database.New(roDB), roDB)
	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503 on a read-only database; body=%s", rec.Code, rec.Body.String())
	}
	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)

	if resp.Status != "degraded" {
		t.Errorf("Status=%q, want %q", resp.Status, "degraded")
	}
	if !strings.Contains(resp.WriteProbe, "attempt to write a readonly database") {
		t.Errorf("WriteProbe=%q, want it to carry the driver's failure string verbatim", resp.WriteProbe)
	}
	if !strings.HasPrefix(resp.WriteProbe, "error: ") {
		t.Errorf("WriteProbe=%q, want the %q prefix quick_check uses for the same shape", resp.WriteProbe, "error: ")
	}

	// The load-bearing half of this test. Every OTHER sub-check reads, and
	// reads work fine on a read-only database — so these must all still be
	// healthy. If they are not, the fixture broke reads and the 503 above
	// proves nothing about the write probe.
	if resp.QuickCheck != database.IntegrityOK {
		t.Errorf("QuickCheck=%q, want %q — the fixture must break WRITES only, or the 503 is not attributable to the write probe", resp.QuickCheck, database.IntegrityOK)
	}
	if resp.SchemaVersion == "" {
		t.Error("SchemaVersion is empty; the read-only fixture broke the schema_version read, so the 503 is not attributable to the write probe")
	}
	_ = q // the read-write Queries handle is unused; the handler under test runs on roDB
}

// --- the storage bound is enforced by the schema, not by the caller ---

func TestHealthWriteProbeTable_CannotGrowBeyondOneRow(t *testing.T) {
	_, db := setupTestDB(t)

	if _, err := db.Exec(`INSERT INTO health_write_probe (id, probed_at) VALUES (2, '2026-08-08 10:00:00')`); err == nil {
		t.Fatal("inserted a second health_write_probe row; CHECK (id = 1) is missing, so the probe table can grow without bound")
	} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Errorf("second-row insert failed with %v, want a CHECK constraint violation", err)
	}
}

// --- the response must never be served from a cache ---

// A cached 200 defeats every sub-check at once: an intermediary answering
// for the origin means no quick_check ran and no probe wrote, so the
// read-only detection this endpoint exists for silently reports a state
// nobody measured. The header makes the control self-defending; this pin
// makes removing the header loud.
func TestHandleHealthzData_CacheControlNoStore(t *testing.T) {
	q, db := setupTestDB(t)

	h := NewHandler(q, db)
	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control=%q, want %q — without it a caching intermediary can serve a stale 200 and the write probe never runs", got, "no-store")
	}
}
