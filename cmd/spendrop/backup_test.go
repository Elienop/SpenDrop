package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/elienop/spendrop/internal/config"
)

// backupTestCfg builds a minimal *config.Config that points runBackup at the
// given source DB path. Only the fields runBackup actually reads are populated.
func backupTestCfg(t *testing.T, dbPath string) *config.Config {
	t.Helper()
	return &config.Config{
		DBPath: dbPath,
		SQLite: config.SQLiteConfig{
			BusyTimeout: 5 * time.Second,
			JournalMode: "WAL",
			ForeignKeys: true,
		},
	}
}

// populateSourceDB creates a tiny test schema at path with three rows of
// known data. Returns the open *sql.DB so the caller can keep using it for
// concurrent-write tests; otherwise the caller should close it.
func populateSourceDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := "file:" + path + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE things (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value REAL NOT NULL
	)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO things (name, value) VALUES
		('alpha', 1.5),
		('beta',  2.5),
		('gamma', 3.5)`); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	return db
}

func TestRunBackup_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	cfg := backupTestCfg(t, src)
	if err := runBackup(context.Background(), cfg, dst); err != nil {
		t.Fatalf("runBackup: %v", err)
	}

	// Backup file exists and is non-empty.
	dstInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if dstInfo.Size() < 4096 {
		t.Errorf("backup smaller than one SQLite page: %d bytes", dstInfo.Size())
	}

	// Sidecar exists and matches the actual file hash.
	sidecarBytes, err := os.ReadFile(dst + ".sha256")
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	expectedHash, err := sha256File(dst)
	if err != nil {
		t.Fatalf("hash backup: %v", err)
	}
	wantSidecar := fmt.Sprintf("%s  %s\n", expectedHash, "dst.db")
	if string(sidecarBytes) != wantSidecar {
		t.Errorf("sidecar = %q, want %q", string(sidecarBytes), wantSidecar)
	}

	// Backup passes integrity check.
	backupDB, err := sql.Open("sqlite3", dst)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backupDB.Close()

	var status string
	if err := backupDB.QueryRow("PRAGMA integrity_check").Scan(&status); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if status != "ok" {
		t.Errorf("integrity_check returned %q, want ok", status)
	}

	// Row count parity with source.
	var srcCount, dstCount int
	if err := srcDB.QueryRow("SELECT COUNT(*) FROM things").Scan(&srcCount); err != nil {
		t.Fatalf("count src: %v", err)
	}
	if err := backupDB.QueryRow("SELECT COUNT(*) FROM things").Scan(&dstCount); err != nil {
		t.Fatalf("count dst: %v", err)
	}
	if srcCount != dstCount {
		t.Errorf("row count: src=%d dst=%d", srcCount, dstCount)
	}
	if srcCount != 3 {
		t.Errorf("seed inserted %d rows, expected 3", srcCount)
	}
}

func TestRunBackup_NoCompanionFiles(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	cfg := backupTestCfg(t, src)
	if err := runBackup(context.Background(), cfg, dst); err != nil {
		t.Fatalf("runBackup: %v", err)
	}

	for _, ext := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(dst + ext); err == nil {
			t.Errorf("unexpected companion file: %s%s", dst, ext)
		}
	}
}

func TestRunBackup_RefusesOverwrite(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	cfg := backupTestCfg(t, src)

	// First call succeeds.
	if err := runBackup(context.Background(), cfg, dst); err != nil {
		t.Fatalf("first runBackup: %v", err)
	}

	// Snapshot the current dst contents and mtime, plus the sidecar bytes.
	before, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read first backup: %v", err)
	}
	beforeInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat first backup: %v", err)
	}
	sidecarBefore, err := os.ReadFile(dst + ".sha256")
	if err != nil {
		t.Fatalf("read first sidecar: %v", err)
	}

	// Second call must fail.
	err = runBackup(context.Background(), cfg, dst)
	if err == nil {
		t.Fatal("expected second runBackup to fail")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("error = %v, want 'refusing to overwrite'", err)
	}

	// File must be untouched (same bytes, same mtime).
	after, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read second backup: %v", err)
	}
	if string(before) != string(after) {
		t.Error("dst contents changed despite overwrite refusal")
	}
	afterInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat second backup: %v", err)
	}
	if !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Errorf("dst mtime changed: %v vs %v", beforeInfo.ModTime(), afterInfo.ModTime())
	}

	// Sidecar must also be untouched — nothing about a refusal should
	// change the filesystem state of the prior good backup.
	sidecarAfter, err := os.ReadFile(dst + ".sha256")
	if err != nil {
		t.Fatalf("read second sidecar: %v", err)
	}
	if string(sidecarBefore) != string(sidecarAfter) {
		t.Errorf("sidecar contents changed despite overwrite refusal")
	}
}

func TestRunBackup_RefusesOrphanSidecar(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	// Pre-create an orphan sidecar without the companion DB file. This is the
	// state a previously-failed backup run would leave behind, and runBackup
	// must surface it instead of silently overwriting.
	orphanBytes := []byte("stale\n")
	sidecarPath := dst + ".sha256"
	if err := os.WriteFile(sidecarPath, orphanBytes, 0o644); err != nil {
		t.Fatalf("write orphan sidecar: %v", err)
	}

	cfg := backupTestCfg(t, src)
	err := runBackup(context.Background(), cfg, dst)
	if err == nil {
		t.Fatal("expected runBackup to fail on orphan sidecar")
	}
	if !strings.Contains(err.Error(), "sidecar") {
		t.Errorf("error = %v, want it to mention the sidecar", err)
	}

	// dst itself must still not exist.
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("backup file was created despite orphan-sidecar refusal")
	}

	// The orphan sidecar must be byte-for-byte unchanged: the guard exists
	// precisely so the operator can see and investigate the stale file,
	// not so we can quietly rewrite it.
	afterOrphan, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read orphan sidecar after refusal: %v", err)
	}
	if string(afterOrphan) != string(orphanBytes) {
		t.Errorf("orphan sidecar was modified: got %q, want %q", string(afterOrphan), string(orphanBytes))
	}
}

func TestRunBackup_EmptyDestRejected(t *testing.T) {
	cfg := backupTestCfg(t, filepath.Join(t.TempDir(), "src.db"))
	if err := runBackup(context.Background(), cfg, ""); err == nil {
		t.Fatal("expected empty destination to be rejected")
	}
}

func TestRunBackup_CreatesParentDirectory(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	// Destination is two levels deep in non-existent dirs.
	dst := filepath.Join(tmp, "nested", "deeper", "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	cfg := backupTestCfg(t, src)
	if err := runBackup(context.Background(), cfg, dst); err != nil {
		t.Fatalf("runBackup: %v", err)
	}

	if _, err := os.Stat(dst); err != nil {
		t.Errorf("backup not created in nested dir: %v", err)
	}
}

func TestRunBackup_ConcurrentWriteSnapshotIsConsistent(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer srcDB.Close()

	if _, err := srcDB.Exec(`CREATE TABLE rows (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Seed 100 rows so the file has meaningful content for the snapshot.
	for i := 0; i < 100; i++ {
		if _, err := srcDB.Exec(`INSERT INTO rows (id) VALUES (?)`, i); err != nil {
			t.Fatalf("seed insert %d: %v", i, err)
		}
	}

	cfg := backupTestCfg(t, src)

	// Start a writer goroutine that inserts new rows continuously.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		next := 100
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := srcDB.Exec(`INSERT INTO rows (id) VALUES (?)`, next); err != nil {
				// Briefly back off on a busy lock; expected under contention.
				time.Sleep(time.Millisecond)
				continue
			}
			next++
		}
	}()

	// Run the backup while the writer is hammering. VACUUM INTO is allowed
	// to take a snapshot at any point in the writer's timeline; the only
	// requirement is that the snapshot be internally consistent.
	if err := runBackup(context.Background(), cfg, dst); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("runBackup under concurrent writes: %v", err)
	}

	close(stop)
	wg.Wait()

	// Backup must pass integrity check.
	backupDB, err := sql.Open("sqlite3", dst)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backupDB.Close()

	var status string
	if err := backupDB.QueryRow("PRAGMA integrity_check").Scan(&status); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if status != "ok" {
		t.Errorf("integrity_check returned %q, want ok", status)
	}

	// Backup must contain a contiguous prefix of IDs starting at 0. A gap
	// in the middle would indicate an inconsistent snapshot.
	rows, err := backupDB.Query("SELECT id FROM rows ORDER BY id")
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rows.Close()

	want := 0
	for rows.Next() {
		var got int
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if got != want {
			t.Errorf("row gap at index %d: got id=%d", want, got)
			break
		}
		want++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	// Require STRICTLY more than the 100 seeded rows so the test proves
	// the goroutine actually landed at least one insert during runBackup's
	// window. A snapshot containing exactly 100 rows would still be valid
	// but would mean the race never fired — meaning the test tells us
	// nothing about consistency under contention.
	if want <= 100 {
		t.Errorf("concurrent writer never landed an insert during backup window: got %d rows, want > 100", want)
	}
}

func TestQuoteSQLiteString(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "''"},
		{"foo", "'foo'"},
		{"foo's bar", "'foo''s bar'"},
		{"can't won't", "'can''t won''t'"},
		{`\path\to\file`, `'\path\to\file'`}, // backslashes are literal in SQLite
		{"'", "''''"},
		{"''", "''''''"},
		{"/app/data/backups/snap.db", "'/app/data/backups/snap.db'"},
	}
	for _, tc := range cases {
		if got := quoteSQLiteString(tc.in); got != tc.want {
			t.Errorf("quoteSQLiteString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSha256File(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "data.bin")
	payload := []byte("the quick brown fox jumps over the lazy dog")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := sha256File(path)
	if err != nil {
		t.Fatalf("sha256File: %v", err)
	}

	h := sha256.Sum256(payload)
	want := hex.EncodeToString(h[:])
	if got != want {
		t.Errorf("sha256File = %s, want %s", got, want)
	}
}

func TestSha256File_MissingFile(t *testing.T) {
	if _, err := sha256File(filepath.Join(t.TempDir(), "missing.bin")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
