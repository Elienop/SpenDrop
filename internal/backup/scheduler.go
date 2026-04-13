package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Scheduler owns the periodic-backup goroutine. Its fields are the same
// knobs operators set via BACKUP_* env vars; main constructs a Scheduler
// directly from cfg.Backup + cfg.DBPath + cfg.SQLite.BusyTimeout. Keeping
// the struct's fields as primitive types (not a nested *config.Config)
// preserves the backup package's zero dependency on internal/config.
//
// A *Scheduler is configured once, started once, and stopped by cancelling
// the context passed to RunLoop. Do not mutate any field after RunLoop has
// been started — the scheduler intentionally holds no mutex, and the -race
// detector will correctly flag a concurrent mutation as a data race.
type Scheduler struct {
	Enabled     bool
	Dir         string
	Interval    time.Duration
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
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
	logger.Printf("backup: scheduler starting, interval=%s dir=%s keep=(%d daily, %d weekly, %d monthly)",
		s.Interval, s.Dir, s.KeepDaily, s.KeepWeekly, s.KeepMonthly)

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

	// Step 1: measure the live transaction count. This is the
	// verification baseline — the number Verify will compare against.
	liveCount, err := countLiveTransactions(ctx, s.DBPath, s.BusyTimeout)
	if err != nil {
		logger.Printf("backup: error counting live transactions in %s: %v", s.DBPath, err)
		return
	}

	// Step 2: find the previous successful backup's size to set MaxSize.
	// An error here is non-fatal — we treat it as "no baseline available"
	// and let Verify run without an upper bound. The first-ever run in a
	// new directory is the common case, and it is indistinguishable from
	// "dir exists but is empty" or "dir does not exist yet", all of which
	// map to prevSize=0.
	prevSize, prevErr := previousBackupSize(s.Dir)
	if prevErr != nil {
		logger.Printf("backup: warning: reading previous backup size: %v", prevErr)
	}
	var maxSize int64
	if prevSize > 0 {
		maxSize = 10 * prevSize
	}

	// Step 3: take the snapshot.
	started := time.Now()
	if err := Snapshot(ctx, s.DBPath, s.BusyTimeout, dst); err != nil {
		logger.Printf("backup: error writing %s: %v", dst, err)
		return
	}

	// From here on the backup directory exists and any subsequent failure
	// path leaves the ticker able to retry. Schedule the prune for all
	// post-Snapshot exits via defer so the retention policy keeps running
	// regardless of whether Verify, WriteSidecar, or the success path is
	// what finally returns.
	defer s.pruneAndLog(now, logger)

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
		return
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
}

// pruneAndLog runs Prune and reports the outcome. Split out so runOnce can
// defer it regardless of whether the post-Snapshot path ends in verify
// failure, sidecar failure, or success — the retention policy is orthogonal
// to the fate of the current tick's file.
func (s *Scheduler) pruneAndLog(now time.Time, logger *log.Logger) {
	kept, removed, err := Prune(s.Dir, now, s.KeepDaily, s.KeepWeekly, s.KeepMonthly)
	if err != nil {
		logger.Printf("backup: prune error in %s: %v", s.Dir, err)
		return
	}
	if len(removed) > 0 {
		logger.Printf("backup: pruned %d file(s), %d kept", len(removed), len(kept))
	}
}

// renameCorrupt renames a failed-verification backup to "*.corrupt" so an
// operator can inspect the file without it being mistaken for a trusted
// backup. The rename is best-effort: if it fails, we log both errors and
// leave the file where it is — the missing sidecar is enough of a signal
// to later phases that the file is not trusted, and a future prune tick
// will sweep it under normal retention.
//
// The renamed file also no longer matches ParseFilename's prefix, so the
// GFS prune logic automatically leaves it alone. That is deliberate: the
// operator decides when to delete a *.corrupt file, not the scheduler.
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
func previousBackupSize(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read backup directory: %w", err)
	}

	// Build a lookup of .sha256 names for O(1) sidecar presence checks.
	sidecars := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		sidecars[e.Name()] = true
	}

	var (
		newestTS   time.Time
		newestSize int64
		haveAny    bool
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ts, perr := ParseFilename(name)
		if perr != nil {
			continue
		}
		if !sidecars[name+".sha256"] {
			continue
		}
		if !haveAny || ts.After(newestTS) {
			info, err := e.Info()
			if err != nil {
				continue
			}
			newestTS = ts
			newestSize = info.Size()
			haveAny = true
		}
	}
	return newestSize, nil
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
