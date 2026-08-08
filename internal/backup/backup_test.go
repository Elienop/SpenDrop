package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

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
	// Use the table name "transactions" rather than a generic test name
	// so scheduler tests (same package) can exercise the Verify path
	// without a second helper — Verify counts rows in "transactions"
	// specifically, and a parallel schema here keeps the fixture DB
	// representative of production.
	if _, err := db.Exec(`CREATE TABLE transactions (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value REAL NOT NULL
	)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO transactions (name, value) VALUES
		('alpha', 1.5),
		('beta',  2.5),
		('gamma', 3.5)`); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	return db
}

const testBusyTimeout = 5 * time.Second

func TestRun_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	if err := Run(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Run: %v", err)
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
	expectedHash, err := Sha256File(dst)
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
	if err := srcDB.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&srcCount); err != nil {
		t.Fatalf("count src: %v", err)
	}
	if err := backupDB.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&dstCount); err != nil {
		t.Fatalf("count dst: %v", err)
	}
	if srcCount != dstCount {
		t.Errorf("row count: src=%d dst=%d", srcCount, dstCount)
	}
	if srcCount != 3 {
		t.Errorf("seed inserted %d rows, expected 3", srcCount)
	}
}

func TestRun_NoCompanionFiles(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	if err := Run(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, ext := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(dst + ext); err == nil {
			t.Errorf("unexpected companion file: %s%s", dst, ext)
		}
	}
}

func TestRun_RefusesOverwrite(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	// First call succeeds.
	if err := Run(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("first Run: %v", err)
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
	err = Run(context.Background(), src, testBusyTimeout, dst)
	if err == nil {
		t.Fatal("expected second Run to fail")
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

func TestRun_RefusesOrphanSidecar(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	// Pre-create an orphan sidecar without the companion DB file. This is the
	// state a previously-failed backup run would leave behind, and Run must
	// surface it instead of silently overwriting.
	orphanBytes := []byte("stale\n")
	sidecarPath := dst + ".sha256"
	if err := os.WriteFile(sidecarPath, orphanBytes, 0o644); err != nil {
		t.Fatalf("write orphan sidecar: %v", err)
	}

	err := Run(context.Background(), src, testBusyTimeout, dst)
	if err == nil {
		t.Fatal("expected Run to fail on orphan sidecar")
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

// TestRun_VerifyFailureLeavesNothingBehind is the load-bearing test for B8:
// before it, Run was Snapshot + WriteSidecar with no verification, so the
// CLI subcommand and every pre-migration snapshot stamped the ".sha256"
// trust marker onto a file nobody had checked.
//
// tinyPageSizeSourceDB is the package's deterministic verify failure: a
// 512-byte page size makes VACUUM INTO emit ~1 KB, below Verify's one-page
// floor. The snapshot itself succeeds, so a failure here can only come from
// the verification step — which is exactly the splice under test.
//
// The three assertions are inseparable: an error alone would be satisfied
// by a Run that failed for any reason, and a missing sidecar alone would be
// satisfied by a Run that never got as far as writing one.
func TestRun_VerifyFailureLeavesNothingBehind(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	tinyPageSizeSourceDB(t, src)

	err := Run(context.Background(), src, testBusyTimeout, dst)
	if err == nil {
		t.Fatal("expected Run to fail verification, got nil")
	}
	// Names the failed check, and proves the failure came from Verify
	// rather than from VACUUM INTO — Snapshot has no size opinion at all.
	if !strings.Contains(err.Error(), "size too small") {
		t.Errorf("error = %v, want the failed check named ('size too small')", err)
	}
	// The sentinel is what lets the pre-migration path and the CLI word a
	// verification failure differently from a write failure.
	if !errors.Is(err, ErrVerifyFailed) {
		t.Errorf("error = %v, want it to wrap ErrVerifyFailed", err)
	}

	// No sidecar: an unverified file must never carry the trust marker.
	if _, statErr := os.Stat(dst + ".sha256"); statErr == nil {
		t.Error("sidecar written for a backup that failed verification")
	}
	// No .db either: Run's no-half-state contract. The scheduler keeps the
	// file (renamed .corrupt) because it is a loop that would otherwise
	// overwrite its own evidence; Run's callers are one-shot and want the
	// filesystem to look untouched after a failed command.
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("failed backup left a .db behind")
	}
}

// TestRun_SourceWithoutTransactionsTable covers the first-boot arm end to
// end: migration 001 is what creates "transactions", and the pre-migration
// snapshot runs before any migration applies, so on a brand-new install the
// source legitimately has no such table.
//
// Verify's row-count check hard-codes that table name, so making Run verify
// would have turned every first boot into "refusing to migrate" — the exact
// B3-class crash loop this repo has shipped once already. The backup here
// must be produced AND trusted.
func TestRun_SourceWithoutTransactionsTable(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer srcDB.Close()
	// The on-disk shape of a first boot: ensureMigrationsTable has run,
	// nothing else has.
	if _, err := srcDB.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at DATETIME)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	if err := Run(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Run on a pre-migration source: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("backup not created: %v", err)
	}
	if _, err := os.Stat(dst + ".sha256"); err != nil {
		t.Errorf("sidecar not created: %v", err)
	}
}

// TestRun_MissingSourceIsRefusedAndNeverCreated closes the phantom-backup
// hole: go-sqlite3 CREATES a missing database file on open, and the file it
// creates is exactly one SQLite page with no tables in it — which clears
// Verify's size floor (the test is <, not <=) and clears the absent-table
// arm. A misconfigured DBPath therefore used to produce a verified,
// sidecar'd backup of a database that never existed, and the restore drill
// reads that sidecar as proof the file is trustworthy.
//
// The side-effect assertion is the load-bearing one. An error alone would
// be satisfied by a guard placed anywhere in Run, including after the
// source has already been opened and conjured into existence — which would
// leave a stray empty database at the operator's typo'd path for the NEXT
// run to back up successfully.
func TestRun_MissingSourceIsRefusedAndNeverCreated(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "typo.db")
	dst := filepath.Join(tmp, "dst.db")

	err := Run(context.Background(), src, testBusyTimeout, dst)
	if err == nil {
		t.Fatal("expected Run to refuse a nonexistent source, got nil")
	}
	// Naming the path is the whole diagnostic: the operator has to be able
	// to see WHICH path was wrong.
	if !strings.Contains(err.Error(), src) {
		t.Errorf("error = %v, want it to name the missing source %s", err, src)
	}

	if _, statErr := os.Stat(src); statErr == nil {
		t.Error("Run created the missing source database — the guard must run before anything opens it")
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("backup written for a source that does not exist")
	}
	if _, statErr := os.Stat(dst + ".sha256"); statErr == nil {
		t.Error("sidecar written for a source that does not exist")
	}
}

// TestRun_ZeroByteSourceIsRefused closes the last way to mint a fully
// trusted backup of nothing.
//
// os.Stat proves a path EXISTS, not that it holds a database. SQLite treats
// a 0-byte file as a valid empty database, so a DB_PATH aimed at a touch'd
// file — a fresh bind mount, a volume that never got populated — passed the
// existence guard, found no "transactions" table so rode the absence arm,
// and produced a 4096-byte copy that cleared the size floor exactly (the
// test is <, not <=). Measured end to end: every check passed and the file
// earned a sidecar. The scheduler already fails closed on this input, so
// Run was the only remaining leak.
//
// The assertion names the SPECIFIC message rather than settling for a
// non-nil error: any later check failing would also produce an error, and
// a bare err != nil would pass while the guard did nothing.
func TestRun_ZeroByteSourceIsRefused(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "empty.db")
	dst := filepath.Join(tmp, "dst.db")

	if err := os.WriteFile(src, nil, 0o644); err != nil {
		t.Fatalf("create 0-byte source: %v", err)
	}

	err := Run(context.Background(), src, testBusyTimeout, dst)
	if err == nil {
		t.Fatal("expected Run to refuse a 0-byte source, got nil")
	}
	if !strings.Contains(err.Error(), "is empty (0 bytes)") {
		t.Errorf("error = %v, want the emptiness guard to be what fired", err)
	}
	if !strings.Contains(err.Error(), src) {
		t.Errorf("error = %v, want it to name the source %s", err, src)
	}

	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("backup written for a 0-byte source")
	}
	if _, statErr := os.Stat(dst + ".sha256"); statErr == nil {
		t.Error("sidecar written for a 0-byte source — a trusted backup of no data")
	}
}

// TestRun_TableLessButRealSourceIsAccepted is the guard's counterweight.
// The emptiness check must not catch a legitimate first boot: at the moment
// the pre-migration snapshot runs, the database is real but holds only
// schema_migrations. Measured at 12 KB, comfortably non-zero — but that is
// exactly the kind of "obviously fine" case a size guard breaks, and
// breaking it would refuse to migrate on every new install.
func TestRun_TableLessButRealSourceIsAccepted(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "firstboot.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer srcDB.Close()
	if _, err := srcDB.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	if err := Run(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Run on a real but table-less first-boot source: %v", err)
	}
	if _, err := os.Stat(dst + ".sha256"); err != nil {
		t.Errorf("first-boot backup did not earn a sidecar: %v", err)
	}
}

func TestRun_EmptyDestRejected(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	// The source must be REAL. This test used to pass a nonexistent path,
	// which was harmless until Run gained its source-existence guard — from
	// then on the guard, not the empty-destination check, would have been
	// what produced the error, and the test would have kept passing with
	// the destination check deleted.
	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	err := Run(context.Background(), src, testBusyTimeout, "")
	if err == nil {
		t.Fatal("expected empty destination to be rejected")
	}
	if !strings.Contains(err.Error(), "destination") {
		t.Errorf("error = %v, want the destination named as the problem", err)
	}
}

func TestRun_CreatesParentDirectory(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	// Destination is two levels deep in non-existent dirs.
	dst := filepath.Join(tmp, "nested", "deeper", "dst.db")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	if err := Run(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(dst); err != nil {
		t.Errorf("backup not created in nested dir: %v", err)
	}
}

// TestRun_ConcurrentWriteSnapshotIsConsistent proves VACUUM INTO produces an
// internally consistent snapshot while a writer hammers the source.
//
// Its table is `rows`, NOT `transactions` — so since B8 this test travels
// Verify's absent-table arm, where the row count is never consulted and an
// unbounded concurrent writer therefore cannot perturb the result. That is
// deliberate and is what keeps the writer loop below flake-free. The
// count-parity arm needs its own fixture; see
// TestRun_CountParityUnderAnOpenWriteTransaction.
func TestRun_ConcurrentWriteSnapshotIsConsistent(t *testing.T) {
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

	// Start a writer goroutine that inserts new rows continuously. `landed`
	// counts successful inserts so the main goroutine can wait for real
	// contention to exist before calling Run — see the poll loop below for
	// why this matters.
	stop := make(chan struct{})
	var landed atomic.Int64
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
			landed.Add(1)
		}
	}()

	// Pre-gate: wait until the writer has actually landed a few inserts
	// before calling Run. Without this gate, a slow-to-schedule goroutine
	// could mean Run completes before the writer runs at all — the backup
	// would still be valid, but the post-check below (`want > 100`) would
	// fire spuriously, failing CI on a race in the *test*, not in the
	// code under test. 3 is an arbitrary small number large enough to
	// prove the writer loop is actually making progress; 5 seconds is a
	// generous safety budget for a CPU-starved CI runner.
	deadline := time.Now().Add(5 * time.Second)
	for landed.Load() < 3 {
		if time.Now().After(deadline) {
			close(stop)
			wg.Wait()
			t.Fatalf("concurrent writer never landed 3 rows within 5s (landed=%d)", landed.Load())
		}
		time.Sleep(time.Millisecond)
	}

	// Run the backup while the writer is hammering. VACUUM INTO is allowed
	// to take a snapshot at any point in the writer's timeline; the only
	// requirement is that the snapshot be internally consistent.
	if err := Run(context.Background(), src, testBusyTimeout, dst); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("Run under concurrent writes: %v", err)
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
	// the goroutine actually landed at least one insert during Run's
	// window. A snapshot containing exactly 100 rows would still be valid
	// but would mean the race never fired — meaning the test tells us
	// nothing about consistency under contention.
	if want <= 100 {
		t.Errorf("concurrent writer never landed an insert during backup window: got %d rows, want > 100", want)
	}
}

// TestRun_CountParityUnderAnOpenWriteTransaction exercises Verify's
// count-parity arm end to end with a genuinely live writer on the source —
// the scenario Run's baseline-before-Snapshot comment is written for, and
// the one its sibling above cannot reach (that fixture's table is `rows`, so
// it rides the absent-table arm and never counts anything).
//
// Flake-freedom comes from SQLite's isolation, not from timing. The writer
// holds an OPEN, UNCOMMITTED transaction across the whole of Run, and
// uncommitted rows are invisible to every reader — so Run's baseline count
// and VACUUM INTO's snapshot point necessarily agree, and the one-row
// tolerance is never even approached. There is no window to lose a race in,
// which is what makes this deterministic by construction rather than by a
// generous tolerance or a scheduling handshake.
//
// I chose this over pausing a background writer for the duration of Run:
// both keep the count stable, but a paused writer proves only that a writer
// EXISTED, while an open transaction means the database is genuinely being
// written to throughout — closer to `docker exec … spendrop backup` against
// a live server, and with no channels or polling to get wrong. Neither shape
// can cover a write LANDING inside the baseline→snapshot window; that is
// inherently non-deterministic here and is owned by
// TestVerify_RowCountOffByOne{Below,Above}.
func TestRun_CountParityUnderAnOpenWriteTransaction(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dst := filepath.Join(tmp, "dst.db")

	srcDB, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer srcDB.Close()

	if _, err := srcDB.Exec(`CREATE TABLE transactions (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < 100; i++ {
		if _, err := srcDB.Exec(`INSERT INTO transactions (id) VALUES (?)`, i); err != nil {
			t.Fatalf("seed insert %d: %v", i, err)
		}
	}

	// Open a write transaction and leave it open across Run.
	tx, err := srcDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin writer tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for i := 100; i < 150; i++ {
		if _, err := tx.Exec(`INSERT INTO transactions (id) VALUES (?)`, i); err != nil {
			t.Fatalf("in-flight insert %d: %v", i, err)
		}
	}

	if err := Run(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Run with an open write transaction on the source: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit writer tx: %v", err)
	}
	committed = true

	// The source moved on; the backup is the point-in-time copy. Asserting
	// both halves is what makes this a snapshot test rather than a "did it
	// error" test: 150 here proves the writer's work was real and did land,
	// 100 there proves the backup excluded work that had not committed when
	// it was taken.
	var srcCount int
	if err := srcDB.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&srcCount); err != nil {
		t.Fatalf("count source after commit: %v", err)
	}
	if srcCount != 150 {
		t.Errorf("source count = %d, want 150 — the in-flight writes did not land", srcCount)
	}

	backupDB, err := sql.Open("sqlite3", "file:"+dst+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backupDB.Close()
	var backupCount int
	if err := backupDB.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&backupCount); err != nil {
		t.Fatalf("count backup: %v", err)
	}
	if backupCount != 100 {
		t.Errorf("backup count = %d, want 100 — uncommitted rows must not appear in a snapshot", backupCount)
	}

	if _, err := os.Stat(dst + ".sha256"); err != nil {
		t.Errorf("sidecar not written for a backup that passed count parity: %v", err)
	}
}

// TestSourceBaseline_WiresSizeDerivedParams pins the two assignments that
// join sourceBaseline's measurements to the VerifyParams it hands Verify:
//
//	params.MaxSize     = 10 * srcSize
//	params.QueryBudget = budgetForSize(srcSize)
//
// Both ends were already tested — budgetForSize has its own table test,
// Verify honours whatever it is given — while the lines JOINING them were
// deletable with the whole repo green. That is the wiring-seam shape this
// repo has shipped before, and QueryBudget's wiring is specifically the
// boot-loop guard: with that line gone, the pre-migration path silently
// reverts to the fixed 30s default that a large legacy database can
// outgrow, which is the exact fail-closed crash loop the sizing exists to
// prevent.
//
// Deleting either assignment leaves the field at its zero value, and every
// expectation below is non-zero, so each deletion fails this test alone.
func TestSourceBaseline_WiresSizeDerivedParams(t *testing.T) {
	// liveDBSize sums the main file and its -wal. Measuring the same way
	// here, rather than hardcoding a number, keeps the assertion true as
	// the fixture evolves — what is pinned is the RELATIONSHIP.
	measure := func(t *testing.T, path string) int64 {
		t.Helper()
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat source: %v", err)
		}
		total := fi.Size()
		if wal, err := os.Stat(path + "-wal"); err == nil {
			total += wal.Size()
		}
		return total
	}

	t.Run("populated source takes the count arm and gets both derived params", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.db")
		// Held open on purpose: closing the last handle checkpoints the
		// WAL away, and the -wal is what makes this cover the summation
		// rather than just the main file's size.
		srcDB := populateSourceDB(t, src)
		defer srcDB.Close()

		if wal, err := os.Stat(src + "-wal"); err != nil || wal.Size() == 0 {
			t.Fatalf("fixture has no non-empty -wal (err=%v) — the summation would not be covered", err)
		}

		params, err := sourceBaseline(context.Background(), src, testBusyTimeout)
		if err != nil {
			t.Fatalf("sourceBaseline: %v", err)
		}
		size := measure(t, src)

		if params.ExpectedTxCount != 3 {
			t.Errorf("ExpectedTxCount = %d, want 3", params.ExpectedTxCount)
		}
		if params.ExpectTransactionsAbsent {
			t.Error("ExpectTransactionsAbsent = true for a source that HAS the table")
		}
		if want := 10 * size; params.MaxSize != want {
			t.Errorf("MaxSize = %d, want %d (10× main+wal) — the assignment is unwired", params.MaxSize, want)
		}
		if want := budgetForSize(size); params.QueryBudget != want {
			t.Errorf("QueryBudget = %v, want %v — the assignment is unwired, so a fail-closed caller silently gets the 30s default", params.QueryBudget, want)
		}
	})

	t.Run("table-less source takes the absence arm and still gets both derived params", func(t *testing.T) {
		// The size wiring must not live inside the has-the-table branch.
		// A first-boot source is exactly where an oversized legacy
		// database would need the scaled budget most, so a mutant that
		// moved either assignment under `if present` has to fail here.
		tmp := t.TempDir()
		src := filepath.Join(tmp, "firstboot.db")
		srcDB, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			t.Fatalf("open src: %v", err)
		}
		defer srcDB.Close()
		if _, err := srcDB.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
			t.Fatalf("create schema_migrations: %v", err)
		}

		params, err := sourceBaseline(context.Background(), src, testBusyTimeout)
		if err != nil {
			t.Fatalf("sourceBaseline: %v", err)
		}
		size := measure(t, src)

		if !params.ExpectTransactionsAbsent {
			t.Error("ExpectTransactionsAbsent = false for a source without the table")
		}
		if params.ExpectedTxCount != 0 {
			t.Errorf("ExpectedTxCount = %d, want 0 on the absence arm", params.ExpectedTxCount)
		}
		if want := 10 * size; params.MaxSize != want {
			t.Errorf("MaxSize = %d, want %d — the size wiring must not be scoped to the count arm", params.MaxSize, want)
		}
		if want := budgetForSize(size); params.QueryBudget != want {
			t.Errorf("QueryBudget = %v, want %v — the budget wiring must not be scoped to the count arm", params.QueryBudget, want)
		}
	})
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

	got, err := Sha256File(path)
	if err != nil {
		t.Fatalf("Sha256File: %v", err)
	}

	h := sha256.Sum256(payload)
	want := hex.EncodeToString(h[:])
	if got != want {
		t.Errorf("Sha256File = %s, want %s", got, want)
	}
}

func TestSha256File_MissingFile(t *testing.T) {
	if _, err := Sha256File(filepath.Join(t.TempDir(), "missing.bin")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
