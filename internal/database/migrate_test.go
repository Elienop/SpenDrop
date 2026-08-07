package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// openTestDB creates a file-backed SQLite database inside t.TempDir() and
// returns both the handle and the path. Phase 4.1 forced the shift from
// `:memory:` to on-disk: SnapshotForMigration opens its own read-only
// connection via a dbPath parameter, and for `:memory:` each connection
// sees an independent (empty) database, which makes the snapshot a
// meaningless no-op. File-backed temp DBs give every connection the same
// view. The t.TempDir() cleanup tears the file down at the end of the
// test — no manual rm needed.
func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dbPath
}

// defaultMigrationOptions returns a MigrationOptions pointing at a
// snapshot directory *adjacent* to the test DB inside t.TempDir(). The
// directory does not exist on entry — RunMigrations is responsible for
// creating it (via SnapshotForMigration → os.MkdirAll), and we let that
// code path run in tests so we exercise the create-on-first-use behavior
// that production relies on.
func defaultMigrationOptions(t *testing.T, dbPath string) MigrationOptions {
	t.Helper()
	return MigrationOptions{
		DBPath:      dbPath,
		SnapshotDir: filepath.Join(filepath.Dir(dbPath), "snapshots"),
		BusyTimeout: 5 * time.Second,
	}
}

// embeddedMigrationNames reads migrationsFS and returns every *.sql
// migration filename in sort order. Tests use this to derive their
// expected migration count and highest-version label from the same
// source of truth RunMigrations loads from, so adding a new migration
// file never silently breaks the assertions here. Panics on error
// because a corrupted embed.FS at test time indicates a build-system
// bug that should fail the whole suite loudly.
func embeddedMigrationNames(t *testing.T) []string {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read embedded migrations dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// countSnapshotFiles returns the number of pre-migration snapshot .db
// files present in dir. It only counts the .db files, not the sidecars,
// so tests can read the "how many snapshots exist" signal unambiguously.
// Non-matching files are ignored so a sibling Tier 1 backup wouldn't
// confuse the count.
func countSnapshotFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read snapshot dir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "pre-migration-") && strings.HasSuffix(name, "Z.db") {
			n++
		}
	}
	return n
}

func TestRunMigrations_CreatesSchemaTable(t *testing.T) {
	db, dbPath := openTestDB(t)

	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// schema_migrations table must exist
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one migration to be recorded")
	}
}

func TestRunMigrations_AppliesInitialSchema(t *testing.T) {
	db, dbPath := openTestDB(t)

	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Verify tables from 001_initial_schema.sql exist
	tables := []string{"users", "sessions", "categories", "currencies", "transactions", "budgets", "savings_goals", "saved_filters", "app_settings"}
	for _, table := range tables {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Errorf("table %s should exist: %v", table, err)
		}
	}
}

func TestRunMigrations_RecordsMigrationVersion(t *testing.T) {
	db, dbPath := openTestDB(t)

	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var version string
	err := db.QueryRow("SELECT version FROM schema_migrations WHERE version = '001_initial_schema.sql'").Scan(&version)
	if err != nil {
		t.Fatalf("expected 001_initial_schema.sql in schema_migrations: %v", err)
	}
	if version != "001_initial_schema.sql" {
		t.Fatalf("expected version '001_initial_schema.sql', got %q", version)
	}
}

func TestRunMigrations_IsIdempotent(t *testing.T) {
	db, dbPath := openTestDB(t)
	opts := defaultMigrationOptions(t, dbPath)

	// Run twice
	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	// Idempotency invariant: after N invocations the table still holds
	// exactly one row per embedded migration. We derive `want` from
	// migrationsFS rather than hardcoding it so adding a future
	// migration file never silently breaks this assertion.
	want := len(embeddedMigrationNames(t))
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != want {
		t.Fatalf("expected %d migration records, got %d", want, count)
	}
}

func TestRunMigrations_SeedsDefaultData(t *testing.T) {
	db, dbPath := openTestDB(t)

	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Check seeded categories
	var catCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM categories").Scan(&catCount); err != nil {
		t.Fatalf("query categories: %v", err)
	}
	if catCount != 19 {
		t.Fatalf("expected 19 seeded categories, got %d", catCount)
	}

	// Check seeded currencies
	var curCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM currencies").Scan(&curCount); err != nil {
		t.Fatalf("query currencies: %v", err)
	}
	if curCount != 3 {
		t.Fatalf("expected 3 seeded currencies, got %d", curCount)
	}
}

// TestRunMigrations_WritesPreMigrationSnapshot asserts that on a fresh
// database, RunMigrations produces a pre-migration-*.db file and a
// matching .sha256 sidecar inside opts.SnapshotDir, and that the filename
// carries the target migration version (the highest-sorted pending
// migration). This is the load-bearing acceptance criterion for Phase
// 4.1: "snapshot file appears BEFORE any migration runs."
func TestRunMigrations_WritesPreMigrationSnapshot(t *testing.T) {
	db, dbPath := openTestDB(t)
	opts := defaultMigrationOptions(t, dbPath)

	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	entries, err := os.ReadDir(opts.SnapshotDir)
	if err != nil {
		t.Fatalf("read snapshot dir: %v", err)
	}

	var dbCount, sidecarCount int
	var snapName string
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".db.sha256"):
			sidecarCount++
		case strings.HasSuffix(name, ".db"):
			dbCount++
			snapName = name
		}
	}

	if dbCount != 1 {
		t.Fatalf("expected 1 snapshot .db file, got %d", dbCount)
	}
	if sidecarCount != 1 {
		t.Fatalf("expected 1 sidecar .sha256 file, got %d", sidecarCount)
	}
	// The highest pending migration on a fresh DB is the last name in
	// the embedded, sort-ordered migration set. Carrying that target
	// version in the filename lets an operator instantly identify which
	// upgrade the snapshot captures state before. Deriving `wantVersion`
	// from migrationsFS rather than hardcoding it means adding a new
	// migration file automatically shifts the expectation — the
	// assertion stays correct without a companion test edit.
	names := embeddedMigrationNames(t)
	if len(names) == 0 {
		t.Fatalf("embedded migrations set is empty")
	}
	wantVersion := strings.TrimSuffix(names[len(names)-1], ".sql")
	if !strings.Contains(snapName, wantVersion) {
		t.Errorf("snapshot name %q should carry target version %q", snapName, wantVersion)
	}
}

// TestRunMigrations_NoSnapshotWhenFullyMigrated asserts that a second
// RunMigrations call against an already-up-to-date database is a no-op:
// no new snapshot is written. This is the hot path for every normal
// server restart — if it created a snapshot on each boot, the directory
// would grow without bound (well, bounded by migrationSnapshotKeep, but
// every restart would still churn the filesystem for no reason).
func TestRunMigrations_NoSnapshotWhenFullyMigrated(t *testing.T) {
	db, dbPath := openTestDB(t)
	opts := defaultMigrationOptions(t, dbPath)

	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}

	first := countSnapshotFiles(t, opts.SnapshotDir)
	if first != 1 {
		t.Fatalf("expected 1 snapshot after first run, got %d", first)
	}

	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	second := countSnapshotFiles(t, opts.SnapshotDir)
	if second != first {
		t.Errorf("expected snapshot count unchanged across idempotent runs, got first=%d second=%d", first, second)
	}
}

// TestRunMigrations_RefusesWhenSnapshotFails asserts that if the
// pre-migration snapshot cannot be written, RunMigrations returns an
// error tagged "refusing to migrate" and leaves schema_migrations in
// its pre-migration state. main.go's log.Fatalf provides the "process
// exits non-zero" half of the acceptance criterion; this test covers
// the "no schema_migrations row is inserted" half.
func TestRunMigrations_RefusesWhenSnapshotFails(t *testing.T) {
	db, dbPath := openTestDB(t)
	opts := defaultMigrationOptions(t, dbPath)

	// Block the snapshot dir by putting a regular file at the exact
	// path where the directory should go. os.MkdirAll refuses to
	// clobber files with a directory, so SnapshotForMigration fails at
	// its very first step and never reaches backup.Run. This exercises
	// the exact error path an operator would hit if /app/data/migration-snapshots
	// was misconfigured as a file mount.
	if err := os.WriteFile(opts.SnapshotDir, []byte("block"), 0o644); err != nil {
		t.Fatalf("write block file: %v", err)
	}

	err := RunMigrations(db, opts)
	if err == nil {
		t.Fatal("expected RunMigrations to fail, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to migrate") {
		t.Errorf("error should contain 'refusing to migrate', got %q", err.Error())
	}

	// schema_migrations should exist (ensureMigrationsTable ran before
	// the snapshot attempt) but be empty — no migration should have
	// been applied before the snapshot failure short-circuited the run.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 applied migrations after snapshot failure, got %d", count)
	}
}

// failMigrations seeds the conflicting index that makes the initial
// schema migration fail deterministically (same idiom as
// TestRunMigrations_ErrorNamesSnapshotPath), so tests can drive
// RunMigrations' failure path on demand.
func failMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE sessions (token TEXT, user_id INTEGER);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);`); err != nil {
		t.Fatalf("seed conflicting index: %v", err)
	}
}

// TestRunMigrations_FailurePathPrunesSnapshots is the B3 regression: a
// failing migration under a restart loop used to add one full DB copy
// per attempt with nothing ever pruning. Seed the snapshot dir as if
// several failed attempts already happened, fail one more, and assert
// the directory is bounded AND the bracket anchor (the oldest snapshot
// for the pending target version) survived — the assertion that kills a
// naive keep-newest prune.
func TestRunMigrations_FailurePathPrunesSnapshots(t *testing.T) {
	db, dbPath := openTestDB(t)
	failMigrations(t, db)
	opts := defaultMigrationOptions(t, dbPath)

	// The bracket's target version is the highest pending migration —
	// derived from the same embed FS RunMigrations reads.
	names := embeddedMigrationNames(t)
	target := strings.TrimSuffix(names[len(names)-1], ".sql")

	// Seed 5 prior "attempts" for this bracket, oldest first, timed
	// RELATIVE to now (minutes in the past) so the real snapshot
	// RunMigrations takes is always strictly newest — hardcoded dates
	// made this test time-of-day dependent.
	if err := os.MkdirAll(opts.SnapshotDir, 0o755); err != nil {
		t.Fatalf("mkdir snapshot dir: %v", err)
	}
	var seeded []string
	for i := 0; i < 5; i++ {
		ts := time.Now().UTC().Add(-time.Duration(10-i) * time.Minute)
		name := formatMigrationSnapshotName(target, ts)
		seeded = append(seeded, name)
		writeSnapPair(t, opts.SnapshotDir, name)
	}
	anchor := seeded[0]

	err := RunMigrations(db, opts)
	if err == nil {
		t.Fatal("expected RunMigrations to fail, got nil")
	}
	if !strings.Contains(err.Error(), "restore from ") {
		t.Errorf("failure-path prune must not change the error: got %q", err.Error())
	}

	// Bounded: at most migrationSnapshotKeep snapshots remain (5 seeded
	// + 1 real minus pruning).
	if n := countSnapshotFiles(t, opts.SnapshotDir); n > migrationSnapshotKeep {
		t.Errorf("snapshot dir holds %d snapshots after failure, want <= %d", n, migrationSnapshotKeep)
	}
	// Anchored: the oldest bracket snapshot survived the prune.
	if _, statErr := os.Stat(filepath.Join(opts.SnapshotDir, anchor)); statErr != nil {
		t.Errorf("bracket anchor %s was pruned on the failure path: %v", anchor, statErr)
	}
}

// TestRunMigrations_RepeatedFailuresStayBounded drives the failure path
// twice end-to-end (two real snapshots ~1.1s apart so their
// seconds-precision names differ) and asserts the directory converges
// instead of growing — the crash-loop shape itself.
func TestRunMigrations_RepeatedFailuresStayBounded(t *testing.T) {
	db, dbPath := openTestDB(t)
	failMigrations(t, db)
	opts := defaultMigrationOptions(t, dbPath)

	if err := RunMigrations(db, opts); err == nil {
		t.Fatal("attempt 1: expected failure, got nil")
	}
	firstCount := countSnapshotFiles(t, opts.SnapshotDir)
	if firstCount != 1 {
		t.Fatalf("after attempt 1: %d snapshots, want 1", firstCount)
	}

	time.Sleep(1100 * time.Millisecond) // distinct seconds-precision filename

	if err := RunMigrations(db, opts); err == nil {
		t.Fatal("attempt 2: expected failure, got nil")
	}
	if n := countSnapshotFiles(t, opts.SnapshotDir); n > migrationSnapshotKeep {
		t.Errorf("after attempt 2: %d snapshots, want <= %d", n, migrationSnapshotKeep)
	}
}
