// Package backup implements SpenDrop's scheduled-and-on-demand database
// snapshot primitive. Snapshot writes a consistent, WAL-aware copy of the
// live SpenDrop database to a target path using SQLite's online-backup
// primitive (VACUUM INTO). Verify runs the three trust checks against a
// written snapshot. WriteSidecar writes a sidecar SHA-256 checksum file at
// "<dst>.sha256".
//
// THE PACKAGE INVARIANT: a "<dst>.sha256" sidecar is written only after
// Verify has passed against that exact file. The sidecar is what every
// later phase — prune, /healthz/data's trusted_count, the restore drill —
// reads as "this file is trusted", so a sidecar on an unverified file is a
// lie told to a future operator mid-incident. Both paths that produce a
// backup honour it: Run (the CLI subcommand and pre-migration snapshots)
// and Scheduler.runOnce (scheduled backups, which inline the same sequence
// so they can quarantine a failure to *.corrupt rather than delete it).
//
// The package is also used by the `spendrop backup` CLI subcommand, which is
// intended to be invoked from inside the running container:
//
//	docker exec spendrop ./spendrop backup /app/data/backups/test.db
//
// VACUUM INTO is atomic, page-consistent across the WAL, and produces a fully
// defragmented copy in one statement. It does not create -wal/-shm companion
// files alongside the destination, so a backup produced by Snapshot is a
// single self-contained file plus its (optional) sidecar.
package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ErrVerifyFailed tags a Run failure that came from Verify rather than from
// writing the snapshot: the copy was produced, and then it did not pass.
// Callers use errors.Is to separate that from a write failure, which has a
// different operator response.
//
// It does NOT mean "the copy is definitively wrong". Verify fails both when
// it CHECKED and found a mismatch (integrity_check, row parity, size) and
// when it could not check at all (stat backup, open backup, a QueryBudget
// deadline mid-read) — the latter usually points at the volume, not the
// data. The wrapped chain names the step that actually failed, which is the
// signal an operator should act on; do not read the sentinel alone as a
// verdict about the database. (A separate could-not-check sentinel was
// considered and deliberately deferred: the wrapped step name already
// carries it, and a second sentinel would need every caller to handle three
// cases instead of two.)
var ErrVerifyFailed = errors.New("backup verification failed")

// Run writes a VERIFIED snapshot of the live database at dbPath to dst,
// then writes a sidecar SHA-256 checksum file at dst+".sha256". It requires
// dbPath to exist already, and refuses to overwrite an existing destination
// so a misconfigured second invocation cannot silently destroy a prior
// backup.
//
// Run takes primitive arguments rather than a *config.Config so the backup
// package has zero dependency on internal/config. busyTimeout is the SQLite
// busy-timeout to apply to the read connection; pass the same value your
// server uses (cfg.SQLite.BusyTimeout is the typical source).
//
// The sequence is baseline → Snapshot → Verify → WriteSidecar, the same
// order Scheduler.runOnce runs inline. Run and runOnce now diverge ONLY in
// how they handle failure, never in whether they verify:
//
//   - Verify failure: Run removes the .db; runOnce renames it to *.corrupt
//     for operator forensics. Run's callers are one-shot (an operator at a
//     terminal, a boot-time migration guard) and both want the filesystem
//     to look untouched after a failed command; the scheduler is a loop
//     whose evidence would otherwise be overwritten by the next tick.
//   - Sidecar failure: Run removes the .db so an operator-invoked backup
//     never leaves a half-state to clean up by hand; runOnce deliberately
//     keeps the verified-but-unsidecar'd file.
//
// On any failure Run writes no sidecar and ATTEMPTS to remove the .db it
// wrote. It cannot guarantee the removal succeeds — a read-only mount or a
// vanished directory can defeat it — so if the remove fails, that failure is
// appended to the returned error rather than swallowed. The guarantee is
// therefore: no sidecar ever, and either no .db or an error that says a .db
// was left and where. The returned error always names the step that failed;
// Verify failures additionally wrap ErrVerifyFailed.
func Run(ctx context.Context, dbPath string, busyTimeout time.Duration, dst string) error {
	// The source must already exist. go-sqlite3 CREATES a missing database
	// file on open, and a brand-new table-less SQLite file is exactly one
	// page — which clears Verify's floor (the test is <, not <=) and, having
	// no "transactions" table, clears the absence arm too. So without this
	// stat, a misconfigured DBPath produced a fully verified, sidecar'd
	// backup of a database that never existed: the trust marker earned by
	// nothing at all, which is precisely what verifying Run exists to stop.
	//
	// The check belongs to Run rather than to any one caller: both callers
	// have a real source in every legitimate scenario, and doing it here —
	// before sourceBaseline — means a missing source is never OPENED, so
	// the create-on-open side effect cannot fire at all.
	//
	// TOCTOU between this stat and the opens below is accepted: a source
	// that vanishes in that window fails the snapshot loudly, which is the
	// safe direction.
	srcInfo, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("cannot read source database %s: %w", dbPath, err)
	}

	// Existence is not validity. SQLite treats a 0-byte file as a valid
	// EMPTY database, so a DB_PATH pointing at a touch'd file — a fresh
	// bind mount, a volume that never got populated — sails through the
	// stat above, has no "transactions" table so it rides the absence arm,
	// and VACUUM INTO emits a 4096-byte copy that clears the size floor
	// (measured: exactly minBackupSize, and the test is <, not <=). Every
	// check passes and the file gets a full trust sidecar for a backup
	// containing nothing.
	//
	// The scheduler already fails closed on this input, so Run was the last
	// way to mint the artifact B8 exists to prevent. The legitimate
	// first-boot source is unaffected: a real but table-less SQLite file is
	// at least one page, and in practice 12 KB by the time
	// ensureMigrationsTable has run (measured).
	if srcInfo.Size() == 0 {
		return fmt.Errorf("source database %s is empty (0 bytes): refusing to write a backup that would contain no data", dbPath)
	}

	// Baseline the source BEFORE snapshotting. VACUUM INTO takes a
	// point-in-time snapshot, and measuring first is what makes Verify's
	// one-row tolerance the right shape: a write that lands between the
	// measurement and the snapshot point is forgiven, a missing thousand
	// rows is not. Measuring AFTER would compare the backup against a
	// source that has moved on, which is not a check of the copy.
	//
	// This matters on the CLI path specifically: `docker exec spendrop
	// ./spendrop backup ...` runs against a live server that is still
	// accepting writes.
	params, err := sourceBaseline(ctx, dbPath, busyTimeout)
	if err != nil {
		return err
	}

	if err := Snapshot(ctx, dbPath, busyTimeout, dst); err != nil {
		return err
	}

	if verifyErr := Verify(dst, params); verifyErr != nil {
		failure := fmt.Errorf("%w: %w", ErrVerifyFailed, verifyErr)
		if rmErr := os.Remove(dst); rmErr != nil {
			// Say so rather than dropping it. A rejected copy that
			// survives on disk is the one thing worse than no backup:
			// it has no sidecar, so nothing automatic will look at it
			// again, and an operator reading `ls` sees a plausible
			// file. errors.Is(…, ErrVerifyFailed) still holds through
			// the added wrap.
			//
			// Not covered by a test: reaching it needs os.Remove to
			// fail on a file Snapshot has just successfully created,
			// which means flipping the parent directory to read-only
			// midway through this function — there is no seam to do
			// that without a hook existing purely for the test. What
			// IS tested is the claim side: neither the doc comment nor
			// the CLI asserts the cleanup succeeded.
			failure = fmt.Errorf("%w; additionally, could not remove the rejected copy at %s: %w", failure, dst, rmErr)
		}
		return failure
	}

	if _, err := WriteSidecar(dst); err != nil {
		// Roll back the snapshot. The sidecar's existence is the
		// marker every later phase uses to decide whether a file is
		// trusted, so an orphan .db with no sidecar would be in a
		// half-state the operator would have to clean up manually.
		// For the CLI path, "clean up manually" is exactly the
		// friction we want to avoid: the operator just ran a command
		// and got an error, so the filesystem should look the same
		// as before the command ran. If the rollback itself fails we
		// report that too — the half-state is exactly what the caller
		// needs told, and silently claiming a clean failure would send
		// them off without the one fact that matters.
		if rmErr := os.Remove(dst); rmErr != nil {
			return fmt.Errorf("%w; additionally, could not remove the unsidecar'd snapshot at %s: %w", err, dst, rmErr)
		}
		return err
	}
	return nil
}

// sourceBaseline measures everything Verify needs to know about the source
// database before the snapshot is taken: what the row count should be (or
// that there should be no "transactions" table at all), how large the
// backup may legitimately be, and how long verification may take.
//
// It is a separate function from Run so the "measure the source" step
// cannot drift into the middle of the snapshot sequence, where it would
// stop being a baseline.
func sourceBaseline(ctx context.Context, dbPath string, busyTimeout time.Duration) (VerifyParams, error) {
	var params VerifyParams

	present, err := sourceHasTransactionsTable(ctx, dbPath, busyTimeout)
	if err != nil {
		return VerifyParams{}, err
	}
	if present {
		count, err := countLiveTransactions(ctx, dbPath, busyTimeout)
		if err != nil {
			return VerifyParams{}, err
		}
		params.ExpectedTxCount = count
	} else {
		// First boot: migration 001 creates "transactions" and the
		// pre-migration snapshot runs before any migration applies.
		// The backup is expected to lack the table too — see
		// VerifyParams.ExpectTransactionsAbsent for why this is an
		// assertion rather than a skip.
		params.ExpectTransactionsAbsent = true
	}

	// Size the bound and the budget from the source. A stat failure here
	// degrades both to their "unmeasured" values (no upper bound, floor
	// budget) rather than failing the run: the source being unreadable is
	// about to surface from Snapshot with a far better error, and neither
	// degraded value can turn an unverified backup into a trusted one.
	srcSize, sizeErr := liveDBSize(dbPath)
	if sizeErr != nil {
		srcSize = 0
	}
	if srcSize > 0 {
		// Same derivation as the scheduler (see runOnce step 2): 10× the
		// LIVE SOURCE, never the previous backup. VACUUM INTO output is
		// normally smaller than its source, so a copy 10× the source is a
		// copy bug — and a source-relative bound is stateless, so it
		// cannot wedge the way a backup-relative one did.
		params.MaxSize = 10 * srcSize
	}
	params.QueryBudget = budgetForSize(srcSize)

	return params, nil
}

// sourceHasTransactionsTable reports whether the live database at dbPath
// has a "transactions" table. It uses the same minimal DSN as Snapshot and
// countLiveTransactions (busy_timeout only, no _journal_mode pragma), so
// probing never changes the source's journal MODE — which is the actual
// hazard documented at Snapshot, since the server has already put the
// database in WAL and a pragma write here would silently take it out.
//
// That is a narrower claim than "the probe does not touch the source", and
// deliberately so: closing the connection checkpoints on last close, so
// probing a source that carries a -wal can absorb it into the main file and
// delete the -wal/-shm. That is standard SQLite behaviour, loses no data,
// and is what the parent process would do anyway — but it is a write, so do
// not describe this function as side-effect-free.
//
// The table name is hard-coded to match Verify and countLiveTransactions
// for the same reason those two hard-code it: the whole point is parity
// between what the source has and what the backup has, and a parameter
// would let a caller ask the two questions about different tables.
func sourceHasTransactionsTable(ctx context.Context, dbPath string, busyTimeout time.Duration) (bool, error) {
	dsn := fmt.Sprintf("%s?_busy_timeout=%d", dbPath, busyTimeout.Milliseconds())
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return false, fmt.Errorf("open source database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var n int64
	const q = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'transactions'`
	if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return false, fmt.Errorf("probe source for transactions table: %w", err)
	}
	return n > 0, nil
}

// RunTimeout returns the wall-clock cap a caller should put on the context
// it passes to Run against the database at dbPath. Run does not apply one
// to itself — a primitive should not impose a deadline its caller cannot
// see — so a caller that wants one (the CLI subcommand) derives it here
// rather than hard-coding a constant a large database can outgrow.
//
// Run makes FOUR O(file size) passes, and they are bounded differently — be
// precise about this, because the gaps are real:
//
//   - sourceBaseline's COUNT(*) over "transactions" observes the caller's
//     ctx, so this figure bounds it.
//   - VACUUM INTO observes the caller's ctx, so this figure bounds it too.
//   - Verify runs on its own internal QueryBudget and never observes ctx.
//   - WriteSidecar's SHA-256 hash (Sha256File's io.Copy) observes NEITHER.
//     It is unbounded: a read stall there is not surfaced by any deadline,
//     and no value returned here can change that. Bounding it would mean
//     threading a ctx through the hash path, which is a separate change.
//
// The figure is twice budgetForSize so the caller's cap is comfortably the
// looser of the constraints it does govern — a run that dies on a deadline
// should die on the one sized for the work that stalled, not on an
// operator-facing cap that happened to be tighter.
func RunTimeout(dbPath string) time.Duration {
	size, err := liveDBSize(dbPath)
	if err != nil {
		size = 0 // budgetForSize turns this into the floor
	}
	return 2 * budgetForSize(size)
}

// Snapshot writes a VACUUM INTO copy of the database at dbPath to dst. It
// refuses to overwrite an existing dst or an orphan sidecar at dst+".sha256".
// On VACUUM INTO failure, it sweeps any stale companion files it may have
// left behind at dst / dst-wal / dst-shm / dst-journal.
//
// This is the lower-level primitive that both Run and the scheduler use.
// Both need the snapshot and the sidecar write to be separate operations
// because Phase 1.3 verification runs between them: the sidecar is the
// "this file is trusted" marker and must not be written for a file that
// failed verification. Calling Snapshot + WriteSidecar with no Verify
// between them breaks the package invariant — use Run.
//
// The function uses VACUUM INTO, which is the SQLite online backup
// primitive: it acquires a read snapshot of the source, copies every page
// through the page cache, and produces a defragmented destination in a
// single atomic statement. Concurrent writers to the source are blocked
// only for the duration the snapshot is acquired (microseconds), not for
// the full copy. The destination contains a consistent point-in-time
// snapshot of the source: any in-flight transaction either commits before
// the snapshot point (and is in the backup) or after (and is not). There
// is no torn-write window.
func Snapshot(ctx context.Context, dbPath string, busyTimeout time.Duration, dst string) error {
	if dst == "" {
		return fmt.Errorf("backup destination path must not be empty")
	}

	// Refuse to overwrite an existing file. A second invocation against the
	// same target is almost certainly an operator mistake; fail loudly
	// instead of silently destroying the prior backup.
	//
	// Note: this is a TOCTOU check — a second process could create dst
	// between our Stat and VACUUM INTO. That is acceptable here because
	// (a) this subcommand is operator-invoked, not racing automation, and
	// (b) VACUUM INTO itself refuses to write into an existing file, so
	// the worst case is a loud error from SQLite instead of a loud error
	// from us. The Phase 1.2 scheduled-snapshot path avoids the race
	// entirely by generating a fresh minute-precision filename per tick
	// via FormatFilename — two ticks in the same minute on the same node
	// is the only way to collide, and that would require a misconfigured
	// BACKUP_INTERVAL < 1m, which Validate() rejects.
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("refusing to overwrite existing file: %s", dst)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat destination: %w", err)
	}

	// Refuse if a stale sidecar exists with no companion DB file. That state
	// only arises from a previously-failed backup run; preserving it lets the
	// operator notice and clean up rather than us silently overwriting.
	sidecarPath := dst + ".sha256"
	if _, err := os.Stat(sidecarPath); err == nil {
		return fmt.Errorf("refusing to overwrite existing sidecar: %s", sidecarPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat sidecar: %w", err)
	}

	// mkdir -p the parent so the operator does not have to pre-create it.
	if parent := filepath.Dir(dst); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create backup directory: %w", err)
		}
	}

	// Open the source with a minimal DSN: just the busy_timeout. Do NOT
	// reuse cfg.SQLiteDSN() here — that DSN sets _journal_mode=WAL, which
	// go-sqlite3 executes as a pragma write at connection-open time. A
	// backup primitive's contract is "read-only snapshot"; it must not
	// silently mutate the source database's journal mode as a side effect
	// of being run. The server process has already put the DB in WAL mode
	// at startup, so we inherit that mode; we only need the busy_timeout
	// so a brief write-lock hold from the server does not immediately
	// error us out. The CLI runs as a separate OS process; SQLite's WAL
	// mode + busy_timeout handles cross-process concurrency for us.
	dsn := fmt.Sprintf("%s?_busy_timeout=%d", dbPath, busyTimeout.Milliseconds())
	src, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open source database: %w", err)
	}
	defer src.Close()

	// Pin to a single connection so PRAGMA / VACUUM INTO state is consistent.
	src.SetMaxOpenConns(1)

	if err := src.PingContext(ctx); err != nil {
		return fmt.Errorf("ping source database: %w", err)
	}

	// VACUUM INTO does not accept parameter substitution for the destination
	// path: the SQLite grammar parses it as a string literal at prepare
	// time, not as a bindable expression. (The data-stewardship plan's
	// pseudocode shows `VACUUM INTO ?` — that is wrong; this comment is
	// why we diverge.) We must inline the path as a SQLite string literal,
	// which means single-quoting it and doubling any embedded single
	// quotes per SQLite syntax. Backslashes are NOT escape characters in
	// SQLite literals, so doubling single quotes is sufficient to
	// neutralize any path the operator can pass on the command line.
	stmt := "VACUUM INTO " + quoteSQLiteString(dst)
	if _, err := src.ExecContext(ctx, stmt); err != nil {
		// Best-effort cleanup of any partial files VACUUM INTO may have
		// left behind on failure. In practice VACUUM INTO produces no
		// -wal/-shm/-journal companions, but sweep them defensively so a
		// partial state can never be mistaken for a valid backup by any
		// later phase. Ignored if the files do not exist. dst is already
		// visible to the operator on the command line, so we don't repeat
		// it in the wrapped error.
		for _, ext := range []string{"", "-wal", "-shm", "-journal"} {
			_ = os.Remove(dst + ext)
		}
		return fmt.Errorf("VACUUM INTO: %w", err)
	}

	return nil
}

// WriteSidecar computes sha256(dst) and writes dst+".sha256" via
// write-to-tmp + atomic rename. It returns the hex digest so callers can
// log or re-use it without re-reading the file. Assumes the caller has
// already ensured no sidecar exists at the target path — Snapshot enforces
// that invariant upstream, so Run / runOnce can call this without an
// additional check.
//
// A straight WriteFile-to-final-path would risk a partial sidecar at the
// final path if the process is killed, the disk fills, or a syscall is
// interrupted mid-write — and the next run's orphan-sidecar guard would
// then refuse to proceed against a valid backup. The tmp file lives in the
// same directory as the destination so the rename stays on one filesystem
// and is atomic on every supported OS.
func WriteSidecar(dst string) (string, error) {
	sum, err := Sha256File(dst)
	if err != nil {
		return "", fmt.Errorf("hash backup: %w", err)
	}

	sidecarPath := dst + ".sha256"
	sidecarContent := fmt.Sprintf("%s  %s\n", sum, filepath.Base(dst))
	sidecarTmp := sidecarPath + ".tmp"
	if err := os.WriteFile(sidecarTmp, []byte(sidecarContent), 0o644); err != nil {
		// A partial tmp could sit on disk if WriteFile failed mid-write.
		// The next run's orphan-sidecar guard looks at the final path
		// (not the .tmp suffix), so the leak is invisible to future
		// runs — but a best-effort unlink keeps the backup directory
		// clean for humans reading `ls`.
		_ = os.Remove(sidecarTmp)
		return "", fmt.Errorf("write sidecar tmp: %w", err)
	}
	if err := os.Rename(sidecarTmp, sidecarPath); err != nil {
		_ = os.Remove(sidecarTmp)
		return "", fmt.Errorf("rename sidecar: %w", err)
	}
	return sum, nil
}

// quoteSQLiteString returns s wrapped in single quotes, with any embedded
// single quotes doubled per SQLite literal-string syntax. This is the
// canonical way to inline a string into a SQL statement when parameter
// substitution is not available (as with VACUUM INTO).
func quoteSQLiteString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\'')
		} else {
			out = append(out, s[i])
		}
	}
	out = append(out, '\'')
	return string(out)
}

// Sha256File returns the lowercase hex SHA-256 digest of the file at path.
// Exported because Phase 1.3's verification pass may reuse it to re-check a
// backup against its sidecar, and WriteSidecar calls it to compute the
// digest it writes into the sidecar file.
func Sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
