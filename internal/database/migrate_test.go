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

// snapshotNames returns the names of every pre-migration snapshot .db
// file in dir, sorted. Sidecars are excluded so callers read the "which
// snapshots exist" signal unambiguously, and non-matching files (a
// sibling Tier 1 backup, an operator file) are ignored. Because the
// version label is constant within one upgrade bracket and the timestamp
// is fixed-width, sorted order is chronological order for a bracket's
// snapshots.
func snapshotNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read snapshot dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "pre-migration-") && strings.HasSuffix(name, "Z.db") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// countSnapshotFiles returns the number of pre-migration snapshot .db
// files present in dir.
func countSnapshotFiles(t *testing.T, dir string) int {
	t.Helper()
	return len(snapshotNames(t, dir))
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

// TestRunMigrations_RefusesWhenSnapshotFailsVerification is the sibling of
// TestRunMigrations_RefusesWhenSnapshotFails for the OTHER way a
// pre-migration anchor can be untrustworthy: the file was written fine, but
// it does not match the live database.
//
// Before B8 this state was invisible — the snapshot path never verified, so
// an unsound copy got its ".sha256" sidecar, RunMigrations logged it as the
// file to "restore from", and the migration proceeded on the strength of an
// anchor nobody had checked. Fail closed instead: a rollback point we
// cannot trust is worth no more than one we could not write.
func TestRunMigrations_RefusesWhenSnapshotFailsVerification(t *testing.T) {
	db, dbPath := openUnverifiableTestDB(t)
	opts := defaultMigrationOptions(t, dbPath)

	err := RunMigrations(db, opts)
	if err == nil {
		t.Fatal("expected RunMigrations to refuse an unverifiable snapshot, got nil")
	}
	// Assert the phrase the DEDICATED arm produces, not merely the word
	// "verification". Deleting the errors.Is(backup.ErrVerifyFailed)
	// branch sends this through the fallback arm, whose %w chain still
	// contains "backup verification failed" from the sentinel's own text
	// — so a bare Contains(…, "verification") passes with the branch
	// GONE and proves nothing. Only the dedicated arm emits
	// "failed verification (refusing to migrate)"; the fallback emits
	// "failed (refusing to migrate)". This one substring covers both the
	// refusal and the wording split.
	if !strings.Contains(err.Error(), "failed verification (refusing to migrate)") {
		t.Errorf("error should come from the dedicated verification arm, got %q", err.Error())
	}

	// No migration applied. schema_migrations exists because
	// ensureMigrationsTable runs before the snapshot attempt, but it must
	// be empty — that is the half of "refuses to start" this test owns.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 applied migrations after a failed verify, got %d", count)
	}

	// And no anchor left on disk to mislead a later restore.
	if n := countSnapshotFiles(t, opts.SnapshotDir); n != 0 {
		t.Errorf("expected no snapshot files after a failed verify, got %d", n)
	}
}

// TestRunMigrations_FirstBootSnapshotsBeforeTransactionsExists pins the arm
// that makes verifying the pre-migration snapshot survivable at all.
//
// backup.Verify's row-count check hard-codes the "transactions" table, and
// migration 001 is what creates it — but the pre-migration snapshot is
// taken BEFORE any migration applies. On a brand-new install the source
// therefore has no such table, and a verify that treated that as failure
// would refuse to migrate on every first boot: a crash loop the user cannot
// escape, on a container that has never worked once.
//
// The pre-condition assertion is the point. Without it this reads as an
// ordinary happy-path migration test, and a future fixture that happened to
// create "transactions" up front would silently stop covering first boot.
func TestRunMigrations_FirstBootSnapshotsBeforeTransactionsExists(t *testing.T) {
	db, dbPath := openTestDB(t)
	opts := defaultMigrationOptions(t, dbPath)

	var before int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'transactions'`,
	).Scan(&before); err != nil {
		t.Fatalf("probe source: %v", err)
	}
	if before != 0 {
		t.Fatalf("fixture already has a transactions table — this is no longer the first-boot case")
	}

	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("RunMigrations on a first boot: %v", err)
	}

	// The snapshot was taken AND trusted: a sidecar is the marker every
	// later phase reads, so its absence would mean first boot silently
	// produces an untrusted anchor.
	names := snapshotNames(t, opts.SnapshotDir)
	if len(names) != 1 {
		t.Fatalf("expected exactly 1 pre-migration snapshot, got %d: %v", len(names), names)
	}
	if _, err := os.Stat(filepath.Join(opts.SnapshotDir, names[0]+".sha256")); err != nil {
		t.Errorf("first-boot snapshot has no sidecar: %v", err)
	}

	// And the migration actually ran, so the arm is not being reached by
	// short-circuiting the whole thing.
	var after int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'transactions'`,
	).Scan(&after); err != nil {
		t.Fatalf("probe after migration: %v", err)
	}
	if after != 1 {
		t.Errorf("expected migrations to create the transactions table, found %d", after)
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

// TestFailurePruneExemptions_FieldsComeFromOppositeEnds pins WHICH end of
// the pending list feeds WHICH exemption field. Both fields are version
// labels, so a derivation that swapped them would compile, prune without
// error, and only show up as a lost pristine snapshot mid-incident. The
// expected literals name their fields so a swap cannot satisfy them.
func TestFailurePruneExemptions_FieldsComeFromOppositeEnds(t *testing.T) {
	cases := []struct {
		name    string
		pending []string
		want    pruneExemptions
	}{
		{
			// Anchor = LAST (highest) pending = the bracket's target
			// version; Floor = FIRST (lowest) pending = the stuck
			// migration, which is what survives a target rotation.
			name:    "multi-migration bracket",
			pending: []string{"020_a.sql", "021_b.sql", "022_c.sql"},
			want:    pruneExemptions{Anchor: "022_c", Floor: "020_a"},
		},
		{
			name:    "single pending migration collapses both onto it",
			pending: []string{"020_a.sql"},
			want:    pruneExemptions{Anchor: "020_a", Floor: "020_a"},
		},
		{
			// Unreachable in production (RunMigrations returns early on
			// an empty pending list) but the function stays total: the
			// zero value means "no exemptions", never a panic.
			name:    "empty list yields no exemptions",
			pending: nil,
			want:    pruneExemptions{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := failurePruneExemptions(c.pending); got != c.want {
				t.Errorf("failurePruneExemptions(%v) = %+v, want %+v", c.pending, got, c.want)
			}
		})
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
	// Anchored: the oldest bracket snapshot survived the prune, sidecar
	// included — prune removes a .db and its .sha256 as a pair, so the
	// retention assertion checks the pair too.
	mustExist(t, opts.SnapshotDir, anchor)
}

// TestRunMigrations_RepeatedFailuresStayBounded pins the crash-loop
// STEADY STATE: a directory already carrying the debris of earlier
// failed attempts must converge to exactly migrationSnapshotKeep files
// on the next failure, and the pristine pre-upgrade copy must be one of
// them.
//
// It seeds the priors rather than driving several real attempts because
// the assertion that matters is only reachable once more than `keep`
// files exist — the earlier version of this test drove two real attempts
// and asserted `n > migrationSnapshotKeep`, which two attempts can never
// produce, so it passed with the entire failure-path prune deleted.
// Seeding four priors puts five files on disk before the prune and makes
// `== migrationSnapshotKeep` a claim only a working prune can satisfy.
func TestRunMigrations_RepeatedFailuresStayBounded(t *testing.T) {
	db, dbPath := openTestDB(t)
	failMigrations(t, db)
	opts := defaultMigrationOptions(t, dbPath)

	// Priors carry the bracket's target version — the highest pending
	// migration, read from the same embed FS RunMigrations uses.
	names := embeddedMigrationNames(t)
	target := strings.TrimSuffix(names[len(names)-1], ".sql")

	if err := os.MkdirAll(opts.SnapshotDir, 0o755); err != nil {
		t.Fatalf("mkdir snapshot dir: %v", err)
	}
	// Timed RELATIVE to now (minutes in the past, oldest first) so the
	// real snapshot this run takes is always strictly the newest.
	var seeded []string
	for i := 0; i < 4; i++ {
		ts := time.Now().UTC().Add(-time.Duration(10-i) * time.Minute)
		name := formatMigrationSnapshotName(target, ts)
		seeded = append(seeded, name)
		writeSnapPair(t, opts.SnapshotDir, name)
	}

	if err := RunMigrations(db, opts); err == nil {
		t.Fatal("expected RunMigrations to fail, got nil")
	}

	// 4 seeded + 1 just written = 5 before the prune; one exemption
	// (floor and anchor resolve to the same oldest file) leaves keep-1
	// competitive slots, so the directory lands on exactly keep.
	if n := countSnapshotFiles(t, opts.SnapshotDir); n != migrationSnapshotKeep {
		t.Errorf("snapshot dir holds %d snapshots after a failed attempt, want exactly %d", n, migrationSnapshotKeep)
	}
	mustExist(t, opts.SnapshotDir, seeded[0])
}

// TestRunMigrations_PartialApplyBracketKeepsPristineAnchor drives a REAL
// partial apply — the shape the exemption exists for — instead of the
// nothing-ever-commits shape the other failure tests produce.
//
// Pre-creating `transactions_new` is what splits the bracket. It is the
// staging table 002_cascade_deletes.sql creates in its first statement
// (no IF NOT EXISTS), and 001_initial_schema.sql never mentions that
// name: 001 applies and COMMITS, 002 aborts immediately at "table
// transactions_new already exists". From that point the pre-upgrade
// snapshot is the only artifact that can roll the database back past
// 001. The conflicting-index idiom failMigrations uses cannot serve
// here — it fails the FIRST migration, so nothing is ever committed and
// there is no partial apply. (010 also uses the transactions_new
// staging name, but 002's abort means it is never reached in this test.)
//
// The loop runs migrationSnapshotKeep+1 attempts because that is the
// first count at which the prune must delete something; with fewer files
// on disk the retention assertions would hold even with the exemption
// removed. Each attempt waits past a second boundary so its
// seconds-precision snapshot filename is distinct.
//
// True targetVersion rotation cannot be driven from here — the migration
// set is an embed.FS fixed at build time — which is why
// TestPruneMigrationSnapshots_FloorSurvivesVersionRotation pins the
// rotation case at the policy level instead.
func TestRunMigrations_PartialApplyBracketKeepsPristineAnchor(t *testing.T) {
	db, dbPath := openTestDB(t)
	opts := defaultMigrationOptions(t, dbPath)

	if _, err := db.Exec(`CREATE TABLE transactions_new (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("seed conflicting staging table: %v", err)
	}

	err := RunMigrations(db, opts)
	if err == nil {
		t.Fatal("attempt 1: expected RunMigrations to fail, got nil")
	}
	if !strings.Contains(err.Error(), "restore from ") {
		t.Errorf("attempt 1 error should name the snapshot, got %q", err.Error())
	}

	// The partial apply itself: everything before the conflict is
	// committed, the conflicting migration is not.
	assertMigrationApplied(t, db, "001_initial_schema.sql", true)
	assertMigrationApplied(t, db, "002_cascade_deletes.sql", false)

	after1 := snapshotNames(t, opts.SnapshotDir)
	if len(after1) != 1 {
		t.Fatalf("after attempt 1: %d snapshots, want 1", len(after1))
	}
	pristine := after1[0]

	var secondAttempt string
	for attempt := 2; attempt <= migrationSnapshotKeep+1; attempt++ {
		time.Sleep(1100 * time.Millisecond) // distinct seconds-precision filename

		if err := RunMigrations(db, opts); err == nil {
			t.Fatalf("attempt %d: expected failure, got nil", attempt)
		}
		if attempt == 2 {
			names := snapshotNames(t, opts.SnapshotDir)
			if len(names) != 2 {
				t.Fatalf("after attempt 2: %d snapshots, want 2", len(names))
			}
			secondAttempt = names[1]
		}
	}

	if n := countSnapshotFiles(t, opts.SnapshotDir); n != migrationSnapshotKeep {
		t.Errorf("snapshot dir holds %d snapshots after %d failed attempts, want exactly %d",
			n, migrationSnapshotKeep+1, migrationSnapshotKeep)
	}
	// The pristine copy outlives the loop; the second attempt's snapshot
	// (the oldest non-exempt file) is what the prune consumed instead.
	mustExist(t, opts.SnapshotDir, pristine)
	mustBeGone(t, opts.SnapshotDir, secondAttempt)
}

// TestRunMigrations_SuccessPathTrimsToNewestKeep pins the "self-limiting
// pin" claim in RunMigrations' doc comment at the CALL SITE: once a
// migration finally succeeds, the exemptions are released and the
// directory falls straight back to the plain newest-keep rule.
//
// The seeded files carry the bracket's real target version, i.e. they are
// exactly the debris a crash loop leaves behind, and seeded[0] is the file
// the FAILURE path would pin (both the floor and the anchor rule resolve
// to it). Passing exemptions here — the whole mutation this test exists to
// kill — would spare seeded[0] and consume a slot that seeded[2] should
// have taken. Note the file COUNT still lands on migrationSnapshotKeep
// either way, so the count assertion alone proves nothing; the
// mustBeGone(seeded[0]) / mustExist(seeded[2]) pair is what fails.
func TestRunMigrations_SuccessPathTrimsToNewestKeep(t *testing.T) {
	db, dbPath := openTestDB(t)
	opts := defaultMigrationOptions(t, dbPath)

	names := embeddedMigrationNames(t)
	target := strings.TrimSuffix(names[len(names)-1], ".sql")

	if err := os.MkdirAll(opts.SnapshotDir, 0o755); err != nil {
		t.Fatalf("mkdir snapshot dir: %v", err)
	}
	// Timed RELATIVE to now (oldest first) so the real snapshot this run
	// takes is always strictly the newest — a hardcoded date would make
	// the ordering time-of-day dependent.
	var seeded []string
	for i := 0; i < 4; i++ {
		ts := time.Now().UTC().Add(-time.Duration(10-i) * time.Minute)
		name := formatMigrationSnapshotName(target, ts)
		seeded = append(seeded, name)
		writeSnapPair(t, opts.SnapshotDir, name)
	}

	// All migrations apply cleanly on a fresh DB, so this takes a real
	// snapshot too: 5 files exist when the success-path prune runs.
	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	if n := countSnapshotFiles(t, opts.SnapshotDir); n != migrationSnapshotKeep {
		t.Errorf("snapshot dir holds %d snapshots after a successful migration, want exactly %d", n, migrationSnapshotKeep)
	}
	mustBeGone(t, opts.SnapshotDir, seeded[0])
	mustBeGone(t, opts.SnapshotDir, seeded[1])
	mustExist(t, opts.SnapshotDir, seeded[2])
	mustExist(t, opts.SnapshotDir, seeded[3])
}

// assertMigrationApplied checks whether schema_migrations carries a row
// for version, so partial-apply tests can state both halves of the split
// (committed / not committed) directly.
func assertMigrationApplied(t *testing.T, db *sql.DB, version string, want bool) {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&n); err != nil {
		t.Fatalf("query schema_migrations for %s: %v", version, err)
	}
	if got := n > 0; got != want {
		t.Errorf("migration %s applied = %v, want %v", version, got, want)
	}
}
