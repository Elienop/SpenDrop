package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/elienop/spendrop/internal/config"
)

// seedEmptyDB creates a small SQLite database at path with NO "transactions"
// table, using the same DSN style as the server so the source file is
// representative (WAL mode, busy_timeout set).
//
// Since backup.Run verifies (B8), this fixture is no longer merely "some
// bytes to copy": it is the CLI's first-boot case. Verify's row-count check
// hard-codes the "transactions" table, which migration 001 creates, so a
// source without it exercises the absent-table arm. Deliberately kept in
// that shape rather than upgraded — a `spendrop backup` run against a
// pre-migration database has to work, and nothing else covers it from the
// CLI boundary.
func seedEmptyDB(path string) error {
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE marker (id INTEGER PRIMARY KEY)`)
	return err
}

// seedTransactionsDB creates a source database shaped like a migrated
// SpenDrop install: a "transactions" table with rows in it. This is what
// drives Verify's row-count parity arm, the check the CLI's mainline
// happy-path test needs to exercise.
func seedTransactionsDB(path string) error {
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE transactions (id INTEGER PRIMARY KEY, amount_cents INTEGER NOT NULL)`); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO transactions (amount_cents) VALUES (100), (250), (999)`)
	return err
}

// seedUnverifiableDB creates a source whose snapshot cannot pass
// verification: a 512-byte page size makes VACUUM INTO emit roughly 1 KB,
// below Verify's one-SQLite-page floor. The snapshot itself succeeds, so
// any failure is attributable to verification and nothing else. Same
// technique as internal/backup's tinyPageSizeSourceDB.
func seedUnverifiableDB(path string) error {
	db, err := sql.Open("sqlite3", "file:"+path+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA page_size=512`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE transactions (id INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO transactions (id) VALUES (1)`)
	return err
}

// withArgs temporarily swaps os.Args for the duration of a test.
func withArgs(t *testing.T, args []string) {
	t.Helper()
	orig := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = orig })
}

// captureStderr swaps os.Stderr for a pipe and returns a function that
// restores it and yields everything written. Same swap-and-restore idiom as
// withArgs, with two additions the pipe requires: a goroutine drains it
// continuously (a writer blocks once the pipe buffer fills, which would
// deadlock the test rather than fail it), and the restore is idempotent so
// the deferred call and the explicit one cannot double-close.
//
// This exists because the exit code alone cannot tell the CLI's two failure
// branches apart — both return 1. The stderr line is the only observable
// difference, so it is the only thing that can pin the errors.Is branch.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	os.Stderr = w

	drained := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		drained <- buf.String()
	}()

	var once sync.Once
	var out string
	restore := func() string {
		once.Do(func() {
			os.Stderr = orig
			_ = w.Close()
			out = <-drained
			_ = r.Close()
		})
		return out
	}
	t.Cleanup(func() { restore() })
	return restore
}

func dispatchCfg(t *testing.T, dbPath string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.DBPath = dbPath
	return &cfg
}

// TestDispatchSubcommand_NoArgs asserts the dispatcher returns unhandled when
// no subcommand is present, letting main fall through to the HTTP-server path.
func TestDispatchSubcommand_NoArgs(t *testing.T) {
	withArgs(t, []string{"spendrop"})
	handled, code := dispatchSubcommand(context.Background(), dispatchCfg(t, "unused.db"))
	if handled {
		t.Errorf("handled = true, want false")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
}

// TestDispatchSubcommand_UnknownSubcommand asserts unknown subcommands fall
// through to the server path (handled=false) rather than erroring out. This
// keeps the door open for future os.Args usage outside this file.
func TestDispatchSubcommand_UnknownSubcommand(t *testing.T) {
	withArgs(t, []string{"spendrop", "frobnicate"})
	handled, _ := dispatchSubcommand(context.Background(), dispatchCfg(t, "unused.db"))
	if handled {
		t.Errorf("handled = true, want false")
	}
}

// TestDispatchSubcommand_BackupMissingArg asserts the dispatcher rejects a
// bare `spendrop backup` (no destination) with exit code 2 — the conventional
// "usage" exit.
func TestDispatchSubcommand_BackupMissingArg(t *testing.T) {
	withArgs(t, []string{"spendrop", "backup"})
	handled, code := dispatchSubcommand(context.Background(), dispatchCfg(t, "unused.db"))
	if !handled {
		t.Errorf("handled = false, want true")
	}
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

// TestDispatchSubcommand_BackupTooManyArgs asserts extra positional args are
// also rejected with exit 2.
func TestDispatchSubcommand_BackupTooManyArgs(t *testing.T) {
	withArgs(t, []string{"spendrop", "backup", "a", "b"})
	handled, code := dispatchSubcommand(context.Background(), dispatchCfg(t, "unused.db"))
	if !handled {
		t.Errorf("handled = false, want true")
	}
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

// TestDispatchSubcommand_BackupRejectsReservedPrefix asserts the dispatcher
// refuses destinations whose basename starts with "spendrop-". That prefix
// is the scheduler's namespace — a manual CLI backup landing on the same
// basename pattern as a scheduled one would be parsed as an auto-backup by
// Prune and deleted on the next tick, silently destroying the operator's
// work. The guard belongs in the CLI layer so the error lands on the
// operator's terminal before any database work happens.
func TestDispatchSubcommand_BackupRejectsReservedPrefix(t *testing.T) {
	tmp := t.TempDir()
	dst := filepath.Join(tmp, "spendrop-2026-04-13T1200Z.db")
	withArgs(t, []string{"spendrop", "backup", dst})
	cfg := dispatchCfg(t, filepath.Join(tmp, "src.db"))
	cfg.SQLite.BusyTimeout = 5 * time.Second

	handled, code := dispatchSubcommand(context.Background(), cfg)
	if !handled {
		t.Errorf("handled = false, want true")
	}
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	// The guard fires before backup.Run is called, so no file should have
	// been created — proving the rejection is not a post-hoc unlink.
	if _, err := os.Stat(dst); err == nil {
		t.Errorf("backup file was created despite prefix rejection")
	}
	if _, err := os.Stat(dst + ".sha256"); err == nil {
		t.Errorf("sidecar was created despite prefix rejection")
	}
}

// TestDispatchSubcommand_BackupAcceptsNonReservedPrefix proves the guard
// only fires on the exact reserved prefix and does not accidentally reject
// plausible operator names like "manual-..." or "pre-migration-...".
func TestDispatchSubcommand_BackupAcceptsNonReservedPrefix(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "manual-2026-04-13.db")

	if err := seedEmptyDB(src); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	withArgs(t, []string{"spendrop", "backup", dst})
	cfg := dispatchCfg(t, src)
	cfg.SQLite.BusyTimeout = 5 * time.Second

	handled, code := dispatchSubcommand(context.Background(), cfg)
	if !handled {
		t.Fatalf("handled = false")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("backup file not created: %v", err)
	}
}

// TestDispatchSubcommand_BackupHappyPath is a minimal smoke test that the
// wiring between main's CLI dispatch and internal/backup.Run is intact. The
// exhaustive coverage of Run lives in internal/backup/backup_test.go; here
// we only need to prove the dispatcher passes its arguments through.
//
// The source carries a populated "transactions" table on purpose: with Run
// verifying (B8), that is what puts the mainline CLI path through Verify's
// row-count parity arm. Its sibling
// TestDispatchSubcommand_BackupAcceptsNonReservedPrefix keeps the
// table-less fixture and covers the absent-table arm, so the two together
// exercise both shapes across the CLI boundary.
func TestDispatchSubcommand_BackupHappyPath(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	if err := seedTransactionsDB(src); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	withArgs(t, []string{"spendrop", "backup", dst})
	cfg := dispatchCfg(t, src)
	cfg.SQLite.BusyTimeout = 5 * time.Second

	handled, code := dispatchSubcommand(context.Background(), cfg)
	if !handled {
		t.Fatalf("handled = false")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("backup file not created: %v", err)
	}
	if _, err := os.Stat(dst + ".sha256"); err != nil {
		t.Errorf("sidecar not created: %v", err)
	}
}

// TestDispatchSubcommand_BackupMissingSourceDatabase covers the CLI's
// generic (non-verification) failure branch with the case that actually
// bites an operator: DB_PATH points somewhere wrong, or the data volume is
// not mounted where the container expects it.
//
// This used to exit ZERO. go-sqlite3 created the missing file on open, and
// the one-page table-less database it created passed every check, so
// `spendrop backup` printed "backup ok" and left a sidecar'd file behind —
// a backup of nothing, carrying the marker the restore drill trusts. Worth
// a case at this boundary rather than only at internal/backup: the exit
// code and the absence of a reassuring success line are what an operator
// (or a cron wrapper) actually observes.
func TestDispatchSubcommand_BackupMissingSourceDatabase(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "not-mounted", "spendrop.db")
	dst := filepath.Join(tmp, "dst.db")

	withArgs(t, []string{"spendrop", "backup", dst})
	cfg := dispatchCfg(t, src)
	cfg.SQLite.BusyTimeout = 5 * time.Second

	stderr := captureStderr(t)
	handled, code := dispatchSubcommand(context.Background(), cfg)
	msg := stderr()

	if !handled {
		t.Fatalf("handled = false")
	}
	if code != 1 {
		t.Errorf("code = %d, want 1 (a missing source database is a failed run)", code)
	}
	// Positive marker FIRST. The absence assertion below is worthless on
	// its own — an empty capture would satisfy it — so prove the stream was
	// really captured before concluding anything from what is missing.
	if !strings.Contains(msg, "backup failed") {
		t.Errorf("stderr = %q, want the generic failure line", msg)
	}
	// This is the generic branch, so it must NOT claim verification failed.
	// Deleting the errors.Is branch in the dispatcher routes the OTHER test
	// through this same line; this assertion is what stops the two from
	// being interchangeable.
	if strings.Contains(msg, "failed verification") {
		t.Errorf("stderr = %q, a missing source is not a verification failure", msg)
	}
	if _, err := os.Stat(src); err == nil {
		t.Error("the missing source database was created as a side effect")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("backup written despite the source not existing")
	}
	if _, err := os.Stat(dst + ".sha256"); err == nil {
		t.Error("sidecar written despite the source not existing")
	}
}

// TestDispatchSubcommand_BackupVerifyFailure is the CLI half of B8. Before
// it, `docker exec spendrop ./spendrop backup ...` wrote a ".sha256" sidecar
// unconditionally — an operator taking a manual backup ahead of something
// risky got the trust marker whether or not the copy was sound, and the
// restore drill reads that marker as proof.
//
// A verify failure must exit non-zero and leave the directory exactly as it
// was: no .db to mistake for a backup, and above all no sidecar.
func TestDispatchSubcommand_BackupVerifyFailure(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	if err := seedUnverifiableDB(src); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	withArgs(t, []string{"spendrop", "backup", dst})
	cfg := dispatchCfg(t, src)
	cfg.SQLite.BusyTimeout = 5 * time.Second

	stderr := captureStderr(t)
	handled, code := dispatchSubcommand(context.Background(), cfg)
	msg := stderr()

	if !handled {
		t.Fatalf("handled = false")
	}
	if code != 1 {
		t.Errorf("code = %d, want 1 (verification failure is a failed run)", code)
	}
	// The exit code cannot distinguish the two failure branches — both are
	// 1 — so this line is the only thing pinning the errors.Is dispatch.
	if !strings.Contains(msg, "failed verification") {
		t.Errorf("stderr = %q, want the verification-specific line", msg)
	}
	// The claim the wording is allowed to make unconditionally. The .db's
	// fate is deliberately NOT asserted in the message: Run reports a failed
	// cleanup inside the error text instead of the CLI promising a clean
	// filesystem it did not confirm.
	if !strings.Contains(msg, "no sidecar was written") {
		t.Errorf("stderr = %q, want the no-sidecar guarantee stated", msg)
	}
	// The specific failed check has to survive into the operator's terminal
	// — a summarised "verification failed" would strand them without the
	// one fact that tells them where to look next.
	if !strings.Contains(msg, "size too small") {
		t.Errorf("stderr = %q, want the failed check named", msg)
	}
	if _, err := os.Stat(dst + ".sha256"); err == nil {
		t.Error("sidecar written despite verification failure")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("backup .db left behind despite verification failure")
	}
}
