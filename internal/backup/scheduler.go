package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scheduler owns the periodic-backup goroutine. Its fields are the same
// knobs operators set via BACKUP_* env vars; main constructs a Scheduler
// directly from cfg.Backup + cfg.DBPath + cfg.SQLite.BusyTimeout. Keeping
// the struct's fields as primitive types (not a nested *config.Config)
// preserves the backup package's zero dependency on internal/config.
//
// A *Scheduler is configured once, started once, and stopped by cancelling
// the context passed to RunLoop. Do not mutate any CONFIGURATION field after
// RunLoop has been started — those are read without synchronisation, and the
// -race detector will correctly flag a concurrent mutation as a data race. The
// one exception is the health snapshot behind statusMu, which exists precisely
// so a reader outside the goroutine (the /healthz/data handler) can observe
// progress safely; read it through Status, never by touching the field.
type Scheduler struct {
	Enabled     bool
	Dir         string
	Interval    time.Duration
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	// KeepCorrupt bounds the quarantine. A failed verify renames the backup
	// to .corrupt and never writes a sidecar, so the size baseline never
	// advances and a stuck failure used to deposit one full-size copy of the
	// database per tick, forever, onto the same volume as the live database.
	KeepCorrupt int
	DBPath      string
	BusyTimeout time.Duration

	// Now is an injectable clock used to generate backup filenames and
	// stamp Prune's "as of" time. It does NOT drive runOnce's elapsed
	// duration measurement — that uses the real wall clock via time.Now
	// so a fake clock in tests cannot skew the log's "wrote X in Y"
	// timing. Tests drive Now with a monotonic counter to avoid
	// colliding filenames across rapid ticks; production leaves it nil
	// and the scheduler falls back to time.Now.
	Now func() time.Time

	// Logger receives operator-facing status lines. If nil, log.Default()
	// is used so the scheduler inherits the process's default output.
	// Kept as a concrete *log.Logger instead of an interface because
	// (a) main.go already uses log.Printf everywhere, and (b) one concrete
	// type means zero new abstractions to review.
	Logger *log.Logger

	// statusMu guards status, the health snapshot served by Status(). Written
	// once or twice per tick by the scheduler goroutine and read by the
	// /healthz/data handler on every scrape. A plain Mutex rather than an
	// RWMutex: both critical sections are a handful of field copies, and at
	// one write per BACKUP_INTERVAL against a scrape every few seconds there
	// is no reader contention worth optimising for.
	statusMu sync.Mutex
	status   Status
}

// RunLoop runs the scheduler until ctx is cancelled. It returns immediately
// if Enabled is false. When Enabled is true it fires one backup on entry
// (acceptance criterion: "starting the container with no backup env vars set
// produces a backup file within 60 seconds of startup") and then one per
// ticker fire.
//
// The loop never exits on a runOnce error — individual failures are logged
// and the next tick still fires. The only exit condition is ctx.Done.
func (s *Scheduler) RunLoop(ctx context.Context) {
	if !s.Enabled {
		return
	}

	logger := s.logger()
	logger.Printf("backup: scheduler starting, interval=%s dir=%s keep=(%d daily, %d weekly, %d monthly, %d quarantined)",
		s.Interval, s.Dir, s.KeepDaily, s.KeepWeekly, s.KeepMonthly, s.KeepCorrupt)

	// Guard the startup fire against an already-cancelled ctx. The window
	// is tiny but real: if main receives SIGTERM between `go
	// scheduler.RunLoop(cleanupCtx)` and the goroutine actually being
	// scheduled, cleanupCancel() can fire first. Without this check we
	// would still start a full VACUUM INTO against a dead context —
	// go-sqlite3 would abort the statement, but only after we opened a
	// connection and pinged the DB, wasting the shutdown budget. Checking
	// ctx.Done() here is also the only path by which the startup fire
	// respects cancellation at all, so TestScheduler_CancelStopsLoop's
	// "cancel stops the loop" contract actually holds for it.
	select {
	case <-ctx.Done():
		logger.Printf("backup: scheduler stopping before first run (ctx cancelled)")
		return
	default:
	}

	// Fire once immediately so a freshly-deployed container produces a
	// baseline file within seconds, not at the end of the first interval.
	s.runOnce(ctx, logger)

	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Printf("backup: scheduler stopping (ctx cancelled)")
			return
		case <-ticker.C:
			s.runOnce(ctx, logger)
		}
	}
}

// runOnce performs a single Phase 1.3 backup cycle: measure the live row
// count, snapshot, verify, write the sidecar, log success, then prune.
// Errors are logged and swallowed; the loop relies on this method never
// returning an error so any bug in a given iteration cannot take down the
// server.
//
// The sequence mirrors the Phase 1.3 plan:
//
//  1. Count live transactions — the verification baseline. Doing this
//     BEFORE Snapshot is important: VACUUM INTO takes a point-in-time
//     snapshot, and a write that lands between our count and the snapshot
//     is exactly what RowCountTolerance exists to forgive.
//  2. Measure the previous successful backup's size. Verify uses 10× that
//     as the MaxSize cap; 0 means "no upper bound" which is correct for
//     the first-ever backup in a new directory.
//  3. Snapshot.
//  4. Verify. On failure, rename to *.corrupt for operator forensics and
//     short-circuit — the next tick gets another shot at producing a
//     trusted backup. The sidecar is deliberately NOT written for a failed
//     verify: sidecar presence is the "this backup is trusted" marker
//     every later phase uses, and writing one for a corrupted file would
//     violate that invariant.
//  5. WriteSidecar — only after Verify passes. A sidecar-write failure
//     here leaves a verified-but-untrusted .db, which is unfortunate but
//     strictly less bad than an unverified .db with a sidecar.
//  6. Log the success line in the plan-specified format.
//  7. Prune.
//
// Prune runs on steps 4, 5, and 6 (i.e., after Snapshot succeeds) because
// by that point we know s.Dir exists on disk — Snapshot's MkdirAll ensures
// it. Prune is intentionally skipped when Snapshot itself fails so a first-
// run-against-a-broken-DB does not also produce a "prune error: no such
// directory" log line on top of the real error.
func (s *Scheduler) runOnce(ctx context.Context, logger *log.Logger) {
	now := s.now()
	dst := filepath.Join(s.Dir, FormatFilename(now))

	// ONE tick must reach Status() as ONE observation.
	//
	// Both of the deferred calls below feed the same snapshot, and they are
	// registered so that LIFO runs retention FIRST and the publish LAST. The
	// publish then takes statusMu exactly once, with the tick's outcome and
	// the post-prune directory scan already in hand.
	//
	// This used to be two independent statusMu sections — the outcome from one
	// defer, the directory scan from another — and a Status() read landing
	// between them saw a tick that had run, had succeeded, and reported zero
	// restore points. /healthz/data maps precisely that triple to 503, so a
	// scrape in the gap invented a "nothing to restore" alarm against a volume
	// holding a full retention set. Only the first tick after a start can show
	// it, which is exactly when a container is being health-checked.
	//
	// Swapping the two was NOT the fix: that moves the window rather than
	// closing it, and lets a reader pair fresh counts with the PREVIOUS tick's
	// outcome — briefly HIDING a failure instead of briefly inventing one.
	//
	// Default to error: the value only becomes success or verify_failed where
	// the code below can prove it, so a path added later that returns without
	// saying anything is reported as a failure rather than silently inheriting
	// the previous tick's success.
	outcome := OutcomeError
	var obs dirObservation
	defer func() { s.recordTick(now, outcome, obs) }()

	// Retention runs on EVERY exit path, including the early ones.
	//
	// This was previously deferred only after Snapshot succeeded, which made
	// the quarantine bound unreachable in the exact state it exists to
	// resolve: a BACKUP_DIR volume full of `.corrupt` copies, where the
	// snapshot is what fails. Placing it here rather than merely before
	// Snapshot also covers a failure in step 1 — reclaiming space must not
	// depend on the source database being readable this tick. Prune tolerates
	// a missing directory, so this is harmless before the first backup.
	defer func() { obs = s.pruneAndReport(now, logger) }()

	// Step 1: measure the live transaction count. This is the
	// verification baseline — the number Verify will compare against.
	liveCount, err := countLiveTransactions(ctx, s.DBPath, s.BusyTimeout)
	if err != nil {
		logger.Printf("backup: error counting live transactions in %s: %v", s.DBPath, err)
		return
	}

	// Step 2: bound the backup's size against its OWN SOURCE, not against the
	// last trusted backup.
	//
	// The previous rule was maxSize = 10 x the newest SIDECAR'D backup. A
	// failed verify writes no sidecar, so the baseline could never advance
	// past a failure: once the live DB outgrew 10x the last good backup, every
	// subsequent run failed the size check, was quarantined, wrote no sidecar,
	// and re-read the same stale baseline. Backups stopped permanently while
	// the app stayed healthy and /healthz stayed green. Measured trigger from
	// a fresh install: the first backup captures a ~212 KB empty database, and
	// importing roughly 2,700 transactions (about two years at 4/day) crosses
	// the cap — on an app built to ingest the user's spreadsheet history, that
	// is the expected first week, not a corner case.
	//
	// Comparing against the source is stateless, so it cannot freeze, and it
	// still catches what the check is for: VACUUM INTO output is normally
	// SMALLER than its source, so a copy 10x the source is a copy bug.
	srcSize, srcErr := liveDBSize(s.DBPath)
	if srcErr != nil {
		logger.Printf("backup: warning: measuring source database size: %v", srcErr)
	}
	var maxSize int64
	if srcSize > 0 {
		maxSize = 10 * srcSize
	}

	// Step 3: take the snapshot.
	started := time.Now()
	if err := Snapshot(ctx, s.DBPath, s.BusyTimeout, dst); err != nil {
		logger.Printf("backup: error writing %s: %v", dst, err)
		return
	}

	// Step 4: verify the snapshot against the baseline.
	params := VerifyParams{
		ExpectedTxCount: liveCount,
		MaxSize:         maxSize,
		// RowCountTolerance=0 → Verify defaults it to 1, which the plan
		// documents as the "deliberate, not sloppy" tolerance for writes
		// that land between our count and VACUUM INTO's snapshot point.
	}
	if verifyErr := Verify(dst, params); verifyErr != nil {
		s.renameCorrupt(dst, verifyErr, logger)
		outcome = OutcomeVerifyFailed
		return
	}

	// The previous-backup comparison survives as a WARNING only. It used to
	// gate the verify, which is what let a single large jump wedge backups
	// forever; as a log line it still tells an operator that something grew
	// unexpectedly, without the power to quarantine a faithful copy.
	if prevSize, prevErr := previousBackupSize(s.Dir); prevErr == nil && prevSize > 0 {
		if fi, statErr := os.Stat(dst); statErr == nil && fi.Size() > 10*prevSize {
			logger.Printf("backup: warning: %s is %d bytes, more than 10x the previous backup (%d bytes) — confirm this growth is expected",
				filepath.Base(dst), fi.Size(), prevSize)
		}
	}

	// Step 5: write the sidecar. Verified + sidecar = trusted backup.
	sum, err := WriteSidecar(dst)
	if err != nil {
		logger.Printf("backup: wrote and verified %s but sidecar failed: %v", dst, err)
		return
	}

	// Step 6: emit the plan-specified success line. Re-stat the file so
	// the logged size is the canonical post-write size, not a value
	// captured earlier during Verify. We deliberately do NOT plumb the
	// size through Verify's return — adding a return value to widen the
	// API just to save one syscall is poor trade, and "unknown" is a
	// correct fallback for a file that has already been verified and
	// sidecar'd (the sidecar is the trusted marker; a missing size in
	// the log is cosmetic). If this stat ever returns an error in
	// practice, something has gone very wrong between sidecar write and
	// log emission, which the "unknown" token surfaces cleanly for an
	// operator grepping the log.
	sizeDisplay := "unknown"
	if info, statErr := os.Stat(dst); statErr == nil {
		sizeDisplay = formatBytes(info.Size())
	}
	logger.Printf("backup ok: %s (%s, %d rows, sha256 %s) in %s",
		filepath.Base(dst), sizeDisplay, liveCount, shortenHash(sum),
		time.Since(started).Round(time.Millisecond))
	outcome = OutcomeSuccess
}

// pruneAndReport runs Prune and returns what the tick learned about BACKUP_DIR.
// Split out so runOnce can defer it regardless of whether the post-Snapshot
// path ends in verify failure, sidecar failure, or success — the retention
// policy is orthogonal to the fate of the current tick's file.
//
// It deliberately does NOT touch the health snapshot: the caller folds this
// return value and the tick's outcome into a single recordTick, so one tick
// reaches Status() as one observation. See runOnce's deferred publish.
func (s *Scheduler) pruneAndReport(now time.Time, logger *log.Logger) dirObservation {
	kept, removed, failed, err := Prune(s.Dir, now, s.KeepDaily, s.KeepWeekly, s.KeepMonthly, s.KeepCorrupt)
	if err != nil {
		// Now that prune also runs when Snapshot fails, a not-yet-created
		// backup directory is an ordinary state rather than a fault. Report
		// the zero state as OBSERVED rather than returning silently: "the
		// directory does not exist" and "the directory holds no restore point"
		// are the same fact to anyone asking whether a restore is possible,
		// and staying quiet here would leave the health endpoint reporting the
		// last observation from before the volume disappeared.
		if errors.Is(err, fs.ErrNotExist) {
			return dirObservation{observed: true}
		}
		logger.Printf("backup: prune error in %s: %v", s.Dir, err)
		// Deliberately NOT observed: the directory could not be read, so we
		// know nothing new about its contents. Reporting zeros here would
		// manufacture a "no backups on disk" alarm out of a read failure.
		//
		// unreadable carries the fault itself, which is a different fact from
		// the counts and is the only thing this tick actually established. It
		// matters on its own: Prune's ReadDir is the call that just failed, so
		// retention is not running and the quarantine is no longer bounded.
		return dirObservation{unreadable: true}
	}

	// Post-prune directory scan. Runs after Prune so the counts describe what
	// is actually on the volume now, not what was there before retention.
	//
	// Prune succeeding and the scan failing is a narrow window, but it is the
	// same class of fault and gets the same encoding: unknown counts plus an
	// explicit "could not read the directory".
	var obs dirObservation
	if scan, scanErr := scanBackupDir(s.Dir); scanErr == nil {
		obs = dirObservation{observed: true, scan: scan, pruneFailed: len(failed)}
	} else {
		logger.Printf("backup: could not scan %s for health reporting: %v", s.Dir, scanErr)
		obs = dirObservation{unreadable: true}
	}
	if len(removed) > 0 {
		logger.Printf("backup: pruned %d file(s), %d kept", len(removed), len(kept))
	}
	// Logged whether or not anything was removed. Gating every line on
	// len(removed) > 0 meant "tried to delete 4, deleted 0" produced silence,
	// which reads exactly like a healthy directory that needed no pruning.
	if len(failed) > 0 {
		logger.Printf("backup: could not remove %d file(s) in %s (retention is not being "+
			"enforced; check permissions and whether the volume is read-only): %s",
			len(failed), s.Dir, strings.Join(failed, ", "))
	}

	// Every backup file Prune knows about either survived (kept) or resisted
	// deletion (failed); anything in removed is gone. So kept+failed empty means
	// there is no copy of the database left in BACKUP_DIR at all.
	//
	// Honouring BACKUP_KEEP_CORRUPT=0 is what makes this reachable: with a
	// sustained verify failure every backup ends up quarantined, and an explicit
	// 0 then sweeps the lot. The operator's 0 is deliberately NOT overridden —
	// silently substituting the default is the bug this branch fixed, and they
	// asked for the bound on a volume that presumably needs it. But "bound the
	// quarantine" and "delete the last remaining copy of the database" are
	// different acts, and only the first one was opted into knowingly. Say so,
	// loudly, rather than leaving the discovery for a restore attempt.
	//
	// This cannot become routine noise: pruneAndReport is deferred from runOnce,
	// so on any successful tick the file just written is in kept. A missing
	// directory returns above, which keeps a fresh install quiet until the first
	// snapshot has actually been attempted.
	if len(kept) == 0 && len(failed) == 0 {
		logger.Printf("backup: WARNING: no backup files remain in %s — there is no copy of "+
			"the database left on disk. Retention is doing exactly what it was configured "+
			"to do (keep=%d daily, %d weekly, %d monthly, %d quarantined); if a run of "+
			"failed verifications quarantined everything, raise BACKUP_KEEP_CORRUPT and "+
			"investigate why verification is failing.",
			s.Dir, s.KeepDaily, s.KeepWeekly, s.KeepMonthly, s.KeepCorrupt)
	}

	return obs
}

// renameCorrupt renames a failed-verification backup to "*.corrupt" so an
// operator can inspect the file without it being mistaken for a trusted
// backup. The rename is best-effort: if it fails, we log both errors and
// leave the file where it is — the missing sidecar is enough of a signal
// to later phases that the file is not trusted, and a future prune tick
// will sweep it under normal retention.
//
// The renamed file no longer matches ParseFilename, so it is outside the GFS
// buckets — but it is NOT unbounded. Prune strips the ".corrupt" suffix,
// re-parses, and keeps only the newest KeepCorrupt samples. Retaining every
// sample forever was the original behaviour and it was a disk-exhaustion bug:
// each file is a full-size copy of the database, BACKUP_DIR shares a volume
// with the live database, and a sustained verify failure deposits one per
// tick. Evidence needs one or two samples, not all of them.
func (s *Scheduler) renameCorrupt(dst string, verifyErr error, logger *log.Logger) {
	corruptPath := dst + ".corrupt"
	if renameErr := os.Rename(dst, corruptPath); renameErr != nil {
		logger.Printf("backup: verification failed for %s (%v); rename to .corrupt also failed: %v",
			filepath.Base(dst), verifyErr, renameErr)
		return
	}
	logger.Printf("backup: verification failed for %s (%v); renamed to %s",
		filepath.Base(dst), verifyErr, filepath.Base(corruptPath))
}

func (s *Scheduler) logger() *log.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return log.Default()
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// countLiveTransactions returns the current row count of the "transactions"
// table in the live database. It opens its own short-lived connection with
// the same minimal DSN Snapshot uses — just the busy_timeout, no
// _journal_mode pragma — so the call is free of side effects on the live
// database (see the long comment in Snapshot for why DSN hygiene matters).
//
// The table name is hard-coded to match Verify, which is deliberate: the
// row-count parity check is the only reason we call this function, and
// parameterizing the table name would invite the caller to pass a
// different value in each place and lose the parity guarantee.
func countLiveTransactions(ctx context.Context, dbPath string, busyTimeout time.Duration) (int64, error) {
	dsn := fmt.Sprintf("%s?_busy_timeout=%d", dbPath, busyTimeout.Milliseconds())
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return 0, fmt.Errorf("open live db: %w", err)
	}
	defer db.Close()
	// Pin to a single connection so the COUNT runs against one consistent
	// snapshot. Not strictly required for a single SELECT, but cheap
	// insurance and it matches Snapshot's pattern.
	db.SetMaxOpenConns(1)

	var n int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM transactions").Scan(&n); err != nil {
		return 0, fmt.Errorf("count live transactions: %w", err)
	}
	return n, nil
}

// liveDBSize returns the on-disk footprint of the source database: the main
// file plus its -wal, since uncheckpointed pages live there and VACUUM INTO's
// output includes them. A missing -wal is normal (checkpointed or non-WAL) and
// contributes zero rather than an error.
func liveDBSize(dbPath string) (int64, error) {
	fi, err := os.Stat(dbPath)
	if err != nil {
		return 0, fmt.Errorf("stat database: %w", err)
	}
	total := fi.Size()
	if wal, err := os.Stat(dbPath + "-wal"); err == nil {
		total += wal.Size()
	}
	return total, nil
}

// previousBackupSize returns the byte size of the newest trusted backup in
// dir. A trusted backup is a .db file whose name parses via ParseFilename
// AND which has a matching .sha256 sidecar. Files that match the name but
// lack a sidecar are ignored, because by the Phase 1.3 invariant they are
// not trusted and using their size as the baseline would propagate
// whatever caused the sidecar to go missing.
//
// Returns (0, nil) when the directory is empty or does not exist yet —
// "no previous backup" is not an error, it is the normal state of a fresh
// install and of the first scheduler tick.
//
// "Newest" is determined by the timestamp encoded in the filename, not
// mtime, to make the function deterministic under wall-clock perturbations
// (NTP step, container restart, filesystem without mtime precision).
//
// The walk itself lives in scanBackupDir, shared with the health snapshot, so
// there is exactly one definition of "trusted" in the package.
func previousBackupSize(dir string) (int64, error) {
	scan, err := scanBackupDir(dir)
	if err != nil {
		return 0, err
	}
	return scan.newestTrustedSize, nil
}

// formatBytes renders a byte count in the "4.8 MB" style the Phase 1.3
// acceptance log format calls for. Uses binary (1024-based) units so a
// "1.0 MB" file is actually 1 MiB on disk — the operator-facing log needs
// to match the number `ls -lh` reports for the same file.
func formatBytes(n int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// shortenHash returns the first 12 chars of a hex digest followed by a
// horizontal-ellipsis, matching git's abbreviated-hash convention. 12
// chars is a pragmatic compromise: long enough to be nearly collision-free
// within a single backup directory, short enough to keep log lines terse.
// Inputs shorter than the cutoff are returned unchanged with no ellipsis,
// because appending "…" to a value that was not actually truncated would
// misreport the hash's length to an operator reading the log.
func shortenHash(sum string) string {
	const shortLen = 12
	if len(sum) <= shortLen {
		return sum
	}
	return sum[:shortLen] + "…"
}
