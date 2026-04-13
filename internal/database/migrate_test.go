package database

import (
	"database/sql"
	"os"
	"path/filepath"
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

	// Should still have exactly 4 migration records (one per migration file)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 4 {
		t.Fatalf("expected 4 migration records, got %d", count)
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
	// The highest pending migration on a fresh DB is 004_drop_categories_color.sql.
	// Carrying the target version in the filename lets an operator instantly
	// identify which upgrade the snapshot captures state before.
	if !strings.Contains(snapName, "004_drop_categories_color") {
		t.Errorf("snapshot name %q should carry target version '004_drop_categories_color'", snapName)
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
