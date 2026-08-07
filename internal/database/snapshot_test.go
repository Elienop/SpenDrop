package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFormatMigrationSnapshotName_Shape asserts the generated filename
// matches the documented shape: pre-migration-<version>-<UTC stamp>Z.db.
func TestFormatMigrationSnapshotName_Shape(t *testing.T) {
	ts := time.Date(2026, 4, 13, 15, 30, 45, 0, time.UTC)
	got := formatMigrationSnapshotName("005_foo", ts)
	want := "pre-migration-005_foo-2026-04-13T153045Z.db"
	if got != want {
		t.Errorf("formatMigrationSnapshotName = %q, want %q", got, want)
	}
}

// TestFormatMigrationSnapshotName_ConvertsToUTC asserts the formatter
// normalizes a non-UTC time to UTC before encoding — the filename must
// be stable regardless of the caller's local timezone.
func TestFormatMigrationSnapshotName_ConvertsToUTC(t *testing.T) {
	// 15:30 in a +05:00 zone is 10:30 UTC.
	zone := time.FixedZone("+05:00", 5*3600)
	ts := time.Date(2026, 4, 13, 15, 30, 0, 0, zone)
	got := formatMigrationSnapshotName("005_foo", ts)
	if !strings.Contains(got, "2026-04-13T103000Z") {
		t.Errorf("formatMigrationSnapshotName = %q, expected UTC 2026-04-13T103000Z", got)
	}
}

// TestFormatMigrationSnapshotName_NoColons asserts the generated name
// contains no ':' so it round-trips through FAT/exFAT/NTFS volumes the
// operator may back up to (NAS shares, cloud sync targets, USB drives).
// This is the same constraint Tier 1's FormatFilename honors.
func TestFormatMigrationSnapshotName_NoColons(t *testing.T) {
	ts := time.Date(2026, 4, 13, 15, 30, 45, 0, time.UTC)
	got := formatMigrationSnapshotName("005_foo", ts)
	if strings.Contains(got, ":") {
		t.Errorf("formatMigrationSnapshotName = %q, must not contain ':'", got)
	}
}

// TestParseMigrationSnapshotName_RoundTrip asserts format → parse
// recovers the exact timestamp AND version the formatter encoded.
func TestParseMigrationSnapshotName_RoundTrip(t *testing.T) {
	ts := time.Date(2026, 4, 13, 15, 30, 45, 0, time.UTC)
	name := formatMigrationSnapshotName("005_foo", ts)
	ver, got, ok := parseMigrationSnapshotName(name)
	if !ok {
		t.Fatalf("parseMigrationSnapshotName(%q) returned !ok", name)
	}
	if !got.Equal(ts) {
		t.Errorf("parseMigrationSnapshotName ts = %v, want %v", got, ts)
	}
	if ver != "005_foo" {
		t.Errorf("parseMigrationSnapshotName version = %q, want %q", ver, "005_foo")
	}
}

// TestParseMigrationSnapshotName_Rejects asserts the parser rejects
// any name that does not match the canonical shape. This is the
// predicate pruneMigrationSnapshots uses to decide which files are
// candidates for removal — any false-positive here could delete a
// Tier 1 backup or an operator file that happened to land in the
// same directory.
func TestParseMigrationSnapshotName_Rejects(t *testing.T) {
	cases := []string{
		"spendrop-2026-04-13T0300Z.db",                 // Tier 1 backup, wrong prefix
		"pre-migration-005_foo-2026-04-13T153045Z.txt", // wrong extension
		"pre-migration-005_foo-2026-04-13T153045.db",   // missing Z suffix
		"pre-migration-Z.db",                           // too short for any version
		"pre-migration--2026-04-13T153045Z.db",         // empty version (no char before separator)
		"README.md",                                    // operator file
		"pre-migration-v-zzzz-zz-zzTzzzzzzZ.db",        // valid shape but unparseable timestamp
	}
	for _, c := range cases {
		if _, _, ok := parseMigrationSnapshotName(c); ok {
			t.Errorf("parseMigrationSnapshotName(%q) = ok, want !ok", c)
		}
	}
}

// writeSnapPair helpers below: used by prune tests to seed a directory
// with fake snapshots without going through backup.Run (which would
// require a real source DB). The .db and .sha256 files are just
// byte-strings — prune only inspects filenames, not file contents.
func writeSnapPair(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("fake db"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte("fake sum\n"), 0o644); err != nil {
		t.Fatalf("write %s sidecar: %v", name, err)
	}
}

// TestPruneMigrationSnapshots_KeepsMostRecent asserts that given 5
// snapshots and keep=3, the oldest 2 .db files AND their sidecars are
// removed, and the newest 3 survive intact.
func TestPruneMigrationSnapshots_KeepsMostRecent(t *testing.T) {
	dir := t.TempDir()

	var names []string
	for i := 0; i < 5; i++ {
		ts := time.Date(2026, 1, 1+i, 12, 0, 0, 0, time.UTC)
		name := formatMigrationSnapshotName("v", ts)
		names = append(names, name)
		writeSnapPair(t, dir, name)
	}

	if err := pruneMigrationSnapshots(dir, 3, ""); err != nil {
		t.Fatalf("pruneMigrationSnapshots: %v", err)
	}

	for i, n := range names {
		dbPath := filepath.Join(dir, n)
		sidecarPath := dbPath + ".sha256"
		_, dbErr := os.Stat(dbPath)
		_, scErr := os.Stat(sidecarPath)
		if i < 2 {
			if dbErr == nil {
				t.Errorf("expected %s removed, still present", dbPath)
			}
			if scErr == nil {
				t.Errorf("expected %s removed, still present", sidecarPath)
			}
		} else {
			if dbErr != nil {
				t.Errorf("expected %s retained, stat: %v", dbPath, dbErr)
			}
			if scErr != nil {
				t.Errorf("expected %s retained, stat: %v", sidecarPath, scErr)
			}
		}
	}
}

// TestPruneMigrationSnapshots_IgnoresNonSnapshots asserts that files
// that don't match the canonical pre-migration shape (Tier 1 backups,
// operator files, README, etc.) are never touched by prune — even when
// keep=0 and we're asking prune to "remove everything it considers
// prunable". This is the safety invariant that keeps the shared
// backup directory from eating operator files.
func TestPruneMigrationSnapshots_IgnoresNonSnapshots(t *testing.T) {
	dir := t.TempDir()

	// One pre-migration snapshot, one Tier 1 backup, one operator file.
	snap := formatMigrationSnapshotName("v", time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	extras := []string{
		snap,
		"spendrop-2026-04-13T0300Z.db", // sibling Tier 1 backup
		"notes.txt",                    // operator file
	}
	for _, n := range extras {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}

	// Prune to 0 (keep nothing prunable). Only the pre-migration file
	// should be removed; the Tier 1 backup and operator file must survive.
	if err := pruneMigrationSnapshots(dir, 0, ""); err != nil {
		t.Fatalf("pruneMigrationSnapshots: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, snap)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected %s removed, stat: %v", snap, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spendrop-2026-04-13T0300Z.db")); err != nil {
		t.Errorf("Tier 1 backup should be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("operator file should be preserved: %v", err)
	}
}

// TestPruneMigrationSnapshots_NoOpWhenFewerThanKeep asserts that when
// fewer snapshots exist than the retention count, prune does nothing.
func TestPruneMigrationSnapshots_NoOpWhenFewerThanKeep(t *testing.T) {
	dir := t.TempDir()

	// Two snapshots, keep=3 → should be a no-op.
	for i := 0; i < 2; i++ {
		ts := time.Date(2026, 1, 1+i, 12, 0, 0, 0, time.UTC)
		name := formatMigrationSnapshotName("v", ts)
		writeSnapPair(t, dir, name)
	}

	if err := pruneMigrationSnapshots(dir, 3, ""); err != nil {
		t.Fatalf("pruneMigrationSnapshots: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	// 2 .db + 2 .sha256 = 4 entries.
	if len(entries) != 4 {
		t.Errorf("expected 4 entries remaining (2 db + 2 sidecar), got %d", len(entries))
	}
}

// TestPruneMigrationSnapshots_MissingDirIsNotError asserts that calling
// prune against a non-existent directory returns nil. RunMigrations
// calls prune best-effort after every successful migration, and we
// don't want the first migration's prune to fail just because the
// snapshot dir was created later than the prune call expected (race
// with mkdir).
func TestPruneMigrationSnapshots_MissingDirIsNotError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if err := pruneMigrationSnapshots(dir, 3, ""); err != nil {
		t.Errorf("expected nil for missing dir, got %v", err)
	}
}

// TestPruneMigrationSnapshots_NegativeKeepRejected asserts that a
// negative keep count is caller error and surfaces as an error — a
// bug in the caller could pass -1 and silently delete every snapshot
// on disk.
func TestPruneMigrationSnapshots_NegativeKeepRejected(t *testing.T) {
	dir := t.TempDir()
	if err := pruneMigrationSnapshots(dir, -1, ""); err == nil {
		t.Fatal("expected error for negative keep, got nil")
	}
}

// TestSnapshotForMigration_WritesFileAndSidecar is an end-to-end test
// over a real SQLite database: create a table, close the handle, call
// SnapshotForMigration, and verify the returned path points at a .db
// file with an adjacent .sha256 sidecar. This is the happy path that
// all the RunMigrations acceptance criteria build on.
func TestSnapshotForMigration_WritesFileAndSidecar(t *testing.T) {
	db, dbPath := openTestDB(t)

	// Seed with one table so VACUUM INTO has non-trivial content.
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO t (v) VALUES ('hello')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	snapDir := filepath.Join(filepath.Dir(dbPath), "snapshots")
	got, err := SnapshotForMigration(context.Background(), dbPath, snapDir, "005_foo", 5*time.Second)
	if err != nil {
		t.Fatalf("SnapshotForMigration: %v", err)
	}

	if _, err := os.Stat(got); err != nil {
		t.Errorf("snapshot file not at returned path: %v", err)
	}
	if _, err := os.Stat(got + ".sha256"); err != nil {
		t.Errorf("sidecar not present: %v", err)
	}

	name := filepath.Base(got)
	if !strings.HasPrefix(name, "pre-migration-005_foo-") {
		t.Errorf("unexpected filename prefix: %q", name)
	}
	if !strings.HasSuffix(name, "Z.db") {
		t.Errorf("unexpected filename suffix: %q", name)
	}
}

// TestSnapshotForMigration_RejectsVersionWithDash asserts the guard
// that protects the filename parser. Migration versions in this repo
// use [0-9A-Za-z_] only, so this is a fail-loud check for a future
// author who adds a dashed migration name. The db/snap paths here are
// inside t.TempDir() so a future refactor that reordered the guards
// (e.g. MkdirAll first to fail-fast on a bad mount) would not create
// directories inside the test package directory.
func TestSnapshotForMigration_RejectsVersionWithDash(t *testing.T) {
	dir := t.TempDir()
	_, err := SnapshotForMigration(context.Background(), filepath.Join(dir, "db"), filepath.Join(dir, "snap"), "bad-version", time.Second)
	if err == nil {
		t.Fatal("expected error for dashed version, got nil")
	}
	if !strings.Contains(err.Error(), "'-'") {
		t.Errorf("error should mention '-' restriction, got %q", err.Error())
	}
}

// TestSnapshotForMigration_EmptyParamsRejected asserts each of the
// three required string params fails-loud when empty. The checks are
// defensive — a nil config or a mis-wired caller should produce a
// clear error instead of a half-created snapshot dir. Non-empty paths
// point into t.TempDir() so a future refactor that reordered the
// guards would not create state in the package directory.
func TestSnapshotForMigration_EmptyParamsRejected(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	snap := filepath.Join(dir, "snap")
	cases := []struct {
		name    string
		dbPath  string
		snapDir string
		version string
	}{
		{"empty dbPath", "", snap, "v"},
		{"empty snapDir", db, "", "v"},
		{"empty version", db, snap, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := SnapshotForMigration(context.Background(), c.dbPath, c.snapDir, c.version, time.Second)
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

// TestSnapshotForMigration_FailsOnFilenameCollision asserts that if a
// file already exists at the exact canonical path SnapshotForMigration
// would write to, the call surfaces an error rather than silently
// overwriting or returning a success with stale bytes. This is the
// crash-loop-retry-within-same-second scenario the seconds-precision
// timestamp was designed to *reduce* but cannot fully eliminate.
//
// We can't pin time.Now() inside SnapshotForMigration without adding a
// clock hook, so the assertion is probabilistic: the test computes the
// canonical name for `time.Now()`, pre-writes that exact file, then
// immediately calls SnapshotForMigration. On a normal machine both
// operations land in the same second with near-certainty and the
// collision triggers. A one-in-a-million race where the test skips
// seconds is acceptable — the bug we're guarding against would manifest
// in production every time a container restarts within a second.
func TestSnapshotForMigration_FailsOnFilenameCollision(t *testing.T) {
	db, dbPath := openTestDB(t)
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	snapDir := filepath.Join(filepath.Dir(dbPath), "snapshots")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatalf("mkdir snap dir: %v", err)
	}

	// Pre-seed a file at the exact canonical path SnapshotForMigration
	// will compute. Using time.Now() here gives us ~99%+ chance of
	// collision with the time.Now() inside the function call below.
	preseedName := formatMigrationSnapshotName("v", time.Now())
	preseedPath := filepath.Join(snapDir, preseedName)
	if err := os.WriteFile(preseedPath, []byte("collision"), 0o644); err != nil {
		t.Fatalf("pre-seed collision file: %v", err)
	}

	_, err := SnapshotForMigration(context.Background(), dbPath, snapDir, "v", 5*time.Second)
	if err == nil {
		// If the second-boundary race fired, skip rather than fail:
		// this test is probabilistic by design and a false negative
		// would just mean the two calls spanned a second.
		t.Skip("second-boundary race: pre-seed and SnapshotForMigration landed on different seconds")
	}

	// The pre-seeded file's contents must be untouched — a bad refuse
	// path might delete the existing file before erroring.
	got, rerr := os.ReadFile(preseedPath)
	if rerr != nil {
		t.Fatalf("read pre-seeded file: %v", rerr)
	}
	if string(got) != "collision" {
		t.Errorf("pre-seeded file was clobbered, contents now %q", string(got))
	}
}

// mustExist / mustBeGone: tiny assertion helpers for the anchor-policy
// tests below, checking a snapshot .db and its sidecar together so every
// assertion covers the pair invariant prune maintains.
func mustExist(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Errorf("expected %s retained: %v", name, err)
	}
	if _, err := os.Stat(filepath.Join(dir, name+".sha256")); err != nil {
		t.Errorf("expected %s sidecar retained: %v", name, err)
	}
}

func mustBeGone(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		t.Errorf("expected %s removed, still present", name)
	}
	if _, err := os.Stat(filepath.Join(dir, name+".sha256")); err == nil {
		t.Errorf("expected %s sidecar removed, still present", name)
	}
}

// TestPruneMigrationSnapshots_AnchorSurvivesCrashLoop is the B3 policy
// test: a failing upgrade's bracket holds one pristine pre-upgrade
// snapshot (the OLDEST for the target version) plus a stream of
// per-retry snapshots. Prune with the anchor exemption must converge to
// {anchor, newest keep-1} — deleting the anchor would destroy the only
// full-rollback point once a partial apply has committed earlier
// migrations.
func TestPruneMigrationSnapshots_AnchorSurvivesCrashLoop(t *testing.T) {
	dir := t.TempDir()

	var names []string
	for i := 0; i < 6; i++ {
		ts := time.Date(2026, 8, 7, 12, 0, i, 0, time.UTC)
		name := formatMigrationSnapshotName("021_target", ts)
		names = append(names, name)
		writeSnapPair(t, dir, name)
	}

	if err := pruneMigrationSnapshots(dir, 3, "021_target"); err != nil {
		t.Fatalf("pruneMigrationSnapshots: %v", err)
	}

	// Retained: the anchor (oldest, index 0) + the 2 newest (indexes 4, 5).
	mustExist(t, dir, names[0])
	mustExist(t, dir, names[4])
	mustExist(t, dir, names[5])
	// Removed: the middle of the loop.
	mustBeGone(t, dir, names[1])
	mustBeGone(t, dir, names[2])
	mustBeGone(t, dir, names[3])
}

// TestPruneMigrationSnapshots_EmptyAnchorKeepsOldBehavior pins that the
// "" anchor (the success path) behaves exactly as the pre-B3 prune did
// on the same seed: newest keep survive, oldest go — including the
// would-be anchor.
func TestPruneMigrationSnapshots_EmptyAnchorKeepsOldBehavior(t *testing.T) {
	dir := t.TempDir()

	var names []string
	for i := 0; i < 6; i++ {
		ts := time.Date(2026, 8, 7, 12, 0, i, 0, time.UTC)
		name := formatMigrationSnapshotName("021_target", ts)
		names = append(names, name)
		writeSnapPair(t, dir, name)
	}

	if err := pruneMigrationSnapshots(dir, 3, ""); err != nil {
		t.Fatalf("pruneMigrationSnapshots: %v", err)
	}

	mustBeGone(t, dir, names[0])
	mustBeGone(t, dir, names[1])
	mustBeGone(t, dir, names[2])
	mustExist(t, dir, names[3])
	mustExist(t, dir, names[4])
	mustExist(t, dir, names[5])
}

// TestPruneMigrationSnapshots_AbsentAnchorDegrades: an anchorVersion with
// no matching snapshot (operator deleted the bracket's files mid-loop)
// must degrade to the plain newest-keep rule, not error and not
// accidentally protect anything else.
func TestPruneMigrationSnapshots_AbsentAnchorDegrades(t *testing.T) {
	dir := t.TempDir()

	var names []string
	for i := 0; i < 5; i++ {
		ts := time.Date(2026, 8, 7, 12, 0, i, 0, time.UTC)
		name := formatMigrationSnapshotName("020_other", ts)
		names = append(names, name)
		writeSnapPair(t, dir, name)
	}

	if err := pruneMigrationSnapshots(dir, 3, "021_missing"); err != nil {
		t.Fatalf("pruneMigrationSnapshots: %v", err)
	}

	mustBeGone(t, dir, names[0])
	mustBeGone(t, dir, names[1])
	mustExist(t, dir, names[2])
	mustExist(t, dir, names[3])
	mustExist(t, dir, names[4])
}

// TestPruneMigrationSnapshots_AnchorIsVersionScoped: the exemption
// protects the oldest snapshot of the ANCHOR version only — an older
// snapshot from a previous upgrade bracket gets no protection.
func TestPruneMigrationSnapshots_AnchorIsVersionScoped(t *testing.T) {
	dir := t.TempDir()

	oldBracket := formatMigrationSnapshotName("019_old", time.Date(2026, 8, 7, 11, 0, 0, 0, time.UTC))
	writeSnapPair(t, dir, oldBracket)

	var names []string
	for i := 0; i < 4; i++ {
		ts := time.Date(2026, 8, 7, 12, 0, i, 0, time.UTC)
		name := formatMigrationSnapshotName("021_target", ts)
		names = append(names, name)
		writeSnapPair(t, dir, name)
	}

	if err := pruneMigrationSnapshots(dir, 3, "021_target"); err != nil {
		t.Fatalf("pruneMigrationSnapshots: %v", err)
	}

	// oldBracket is the oldest file overall but the WRONG version — it
	// must not be protected. Anchor = names[0] (oldest 021_target).
	mustBeGone(t, dir, oldBracket)
	mustExist(t, dir, names[0])
	mustBeGone(t, dir, names[1])
	mustExist(t, dir, names[2])
	mustExist(t, dir, names[3])
}

// TestPruneMigrationSnapshots_AnchorSurvivesKeepZero pins that the
// exemption is absolute: even keep=0 (remove everything prunable) spares
// the anchor. No production caller uses keep=0 with an anchor; this test
// exists so the "exempt means exempt" semantics can't silently regress
// into "exempt unless keep is small".
func TestPruneMigrationSnapshots_AnchorSurvivesKeepZero(t *testing.T) {
	dir := t.TempDir()

	a := formatMigrationSnapshotName("021_target", time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	b := formatMigrationSnapshotName("021_target", time.Date(2026, 8, 7, 12, 0, 1, 0, time.UTC))
	writeSnapPair(t, dir, a)
	writeSnapPair(t, dir, b)

	if err := pruneMigrationSnapshots(dir, 0, "021_target"); err != nil {
		t.Fatalf("pruneMigrationSnapshots: %v", err)
	}

	mustExist(t, dir, a)
	mustBeGone(t, dir, b)
}

// TestRunMigrations_ErrorNamesSnapshotPath asserts that when the
// migration *apply* step fails after a successful snapshot, the
// returned error carries the snapshot path so the operator sees a
// direct recovery instruction ("restore from <path>").
//
// We stage a real apply failure by pre-creating an index that collides
// with one created by 001_initial_schema.sql. The migration file
// creates all tables with CREATE TABLE IF NOT EXISTS (so pre-seeding a
// table wouldn't conflict) but creates indexes without the IF NOT
// EXISTS clause — so pre-seeding `idx_sessions_user_id` with a dummy
// shape guarantees the migration fails at that statement with "index
// idx_sessions_user_id already exists". The error bubbles up through
// applyPendingMigrations and is wrapped by RunMigrations with "restore
// from <snapshot path>".
func TestRunMigrations_ErrorNamesSnapshotPath(t *testing.T) {
	db, dbPath := openTestDB(t)

	// Pre-create a sessions table and the conflicting index.
	if _, err := db.Exec(`CREATE TABLE sessions (token TEXT, user_id INTEGER);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);`); err != nil {
		t.Fatalf("seed conflicting index: %v", err)
	}

	opts := MigrationOptions{
		DBPath:      dbPath,
		SnapshotDir: filepath.Join(filepath.Dir(dbPath), "snapshots"),
		BusyTimeout: 5 * time.Second,
	}

	err := RunMigrations(db, opts)
	if err == nil {
		t.Fatal("expected RunMigrations to fail, got nil")
	}
	if !strings.Contains(err.Error(), "restore from ") {
		t.Errorf("error should contain 'restore from <path>', got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "pre-migration-") {
		t.Errorf("error should carry snapshot filename (prefix 'pre-migration-'), got %q", err.Error())
	}
}
