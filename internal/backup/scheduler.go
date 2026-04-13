package backup

import (
	"context"
	"log"
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

// runOnce performs a single backup + prune cycle. Errors are logged and
// swallowed; the loop relies on this method never returning an error so
// any bug in a given iteration cannot take down the server.
func (s *Scheduler) runOnce(ctx context.Context, logger *log.Logger) {
	now := s.now()
	dst := filepath.Join(s.Dir, FormatFilename(now))

	started := time.Now()
	if err := Run(ctx, s.DBPath, s.BusyTimeout, dst); err != nil {
		logger.Printf("backup: error writing %s: %v", dst, err)
		return
	}
	logger.Printf("backup: wrote %s in %s", dst, time.Since(started).Round(time.Millisecond))

	kept, removed, err := Prune(s.Dir, now, s.KeepDaily, s.KeepWeekly, s.KeepMonthly)
	if err != nil {
		logger.Printf("backup: prune error in %s: %v", s.Dir, err)
		return
	}
	if len(removed) > 0 {
		logger.Printf("backup: pruned %d file(s), %d kept", len(removed), len(kept))
	}
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
