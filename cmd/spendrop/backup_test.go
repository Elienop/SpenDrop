package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/elienop/spendrop/internal/config"
)

// seedEmptyDB creates a small empty SQLite database at path using the same
// DSN style as the server so the source file is representative (WAL mode,
// busy_timeout set). Used only by the dispatch happy-path smoke test.
func seedEmptyDB(path string) error {
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE marker (id INTEGER PRIMARY KEY)`)
	return err
}

// withArgs temporarily swaps os.Args for the duration of a test.
func withArgs(t *testing.T, args []string) {
	t.Helper()
	orig := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = orig })
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
func TestDispatchSubcommand_BackupHappyPath(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	// Create a minimal SQLite file so Run has something to snapshot. We
	// use the same DSN style as the server (WAL + busy_timeout) so the
	// source is representative.
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
	if _, err := os.Stat(dst + ".sha256"); err != nil {
		t.Errorf("sidecar not created: %v", err)
	}
}
