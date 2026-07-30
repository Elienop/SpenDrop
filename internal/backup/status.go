package backup

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Outcome classifies how the most recent backup tick ended.
//
// verify_failed and error are deliberately separate. They read the same in a
// dashboard that only counts failures, but they call for different operator
// responses: verify_failed means a snapshot IS being produced and is being
// rejected as untrustworthy (look at the quarantined file, check the source
// database), while error means no snapshot was produced at all (check the
// volume, permissions, disk space). Collapsing them into "failed" would throw
// away the only bit that tells those apart without reading the log.
//
// The values are the strings that reach the operator verbatim, so they are
// snake_case to match the /healthz/data field-naming convention.
type Outcome string

const (
	// OutcomeUnknown is the zero value: no tick has completed yet. It is the
	// legitimate state of a disabled scheduler and of the first moments after
	// boot, and consumers must NOT treat it as a failure.
	OutcomeUnknown Outcome = ""
	// OutcomeSuccess means the tick wrote, verified and sidecar'd a backup.
	OutcomeSuccess Outcome = "success"
	// OutcomeVerifyFailed means the snapshot was written but rejected by
	// Verify and quarantined to *.corrupt.
	OutcomeVerifyFailed Outcome = "verify_failed"
	// OutcomeError means the tick never produced a trusted backup for some
	// other reason: the source could not be read, VACUUM INTO failed, or the
	// sidecar could not be written.
	OutcomeError Outcome = "error"
)

// Status is an immutable snapshot of everything the backup subsystem knows
// about its own health. It exists because every one of these conditions was
// previously visible ONLY in the container log — an operator who never greps
// it discovers a broken backup at restore time, which is the worst possible
// moment.
//
// Status carries no filesystem paths and no database contents by design: it is
// consumed by an unauthenticated health endpoint.
type Status struct {
	// Enabled mirrors Scheduler.Enabled. False means the operator turned
	// backups off, which is a configuration choice and not a fault — a
	// consumer must not read "no backups on disk" as a problem when this is
	// false.
	Enabled bool

	// Interval mirrors Scheduler.Interval. Exposed so a consumer can judge
	// staleness against the configured cadence instead of hard-coding a
	// threshold that is wrong for anyone who tuned BACKUP_INTERVAL.
	Interval time.Duration

	// LastRunAt is the scheduler clock instant of the most recent tick,
	// successful or not. Zero means no tick has completed — distinguish that
	// from a failure before alerting.
	LastRunAt time.Time

	// LastOutcome is how that tick ended.
	LastOutcome Outcome

	// NewestBackupAt is the timestamp encoded in the filename of the newest
	// TRUSTED backup on disk: a name that parses AND has a matching .sha256
	// sidecar. Zero means there is no restore point at all.
	//
	// Filename timestamp rather than mtime, matching previousBackupSize, so
	// the value is stable across an NTP step or a filesystem with coarse
	// mtime.
	NewestBackupAt time.Time

	// TrustedCount is how many restore points exist. Zero with Enabled true
	// is the loudest state the subsystem has.
	TrustedCount int

	// QuarantinedCount is how many *.corrupt files survive after retention
	// ran. Informational: their presence is forensic evidence, and they
	// legitimately outlive the failure that produced them (KeepCorrupt keeps
	// the newest samples on purpose).
	QuarantinedCount int

	// PruneFailedCount is how many files the last prune tick tried and failed
	// to remove. Non-zero means retention is not being enforced — the volume
	// will grow without bound — but the existing backups are unaffected.
	PruneFailedCount int
}

// Status returns a copy of the current snapshot. Safe to call concurrently
// with the scheduler goroutine; Enabled and Interval are filled from the
// configured fields rather than the mutable snapshot because they are set
// before RunLoop starts and never change.
func (s *Scheduler) Status() Status {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	st := s.status
	st.Enabled = s.Enabled
	st.Interval = s.Interval
	return st
}

// recordTick publishes ONE tick as ONE observation: how the tick ended and
// what it learned about BACKUP_DIR, under a single statusMu acquisition.
//
// Atomicity is the point, not an implementation detail. Publishing the run
// stamp and the directory scan through two separate acquisitions let a
// Status() read land between them and see a tick that had run, had succeeded,
// and reported zero restore points — which /healthz/data maps to 503. There is
// no ordering of two sections that fixes it; only one section does.
//
// Called from every exit path of runOnce, including the early ones — a tick
// that failed before writing anything is still an observation, and reporting
// it as "never ran" would hide exactly the failure the operator needs to see.
//
// An UNOBSERVED directory leaves the counts alone. The previous scan is the
// last thing we actually saw on the volume, and overwriting it with zeros
// because this tick could not read the directory would manufacture a "no
// backups on disk" alarm out of a read failure. Which is also why the counts
// live behind obs.observed rather than being written unconditionally: a long
// run of failing ticks still reports the age of the last good backup.
func (s *Scheduler) recordTick(at time.Time, outcome Outcome, obs dirObservation) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.LastRunAt = at
	s.status.LastOutcome = outcome
	if obs.observed {
		s.status.NewestBackupAt = obs.scan.newestTrustedAt
		s.status.TrustedCount = obs.scan.trustedCount
		s.status.QuarantinedCount = obs.scan.quarantinedCount
		s.status.PruneFailedCount = obs.pruneFailed
	}
}

// dirObservation is what a single pruneAndReport call established about
// BACKUP_DIR. It exists so the scan and the tick's outcome can be handed to
// recordTick together instead of being published independently.
//
// observed false means the directory could not be read this tick, so the scan
// and pruneFailed fields carry nothing and must not be published.
type dirObservation struct {
	observed    bool
	scan        dirScan
	pruneFailed int
}

// dirScan is one pass over the backup directory. It answers the questions
// previousBackupSize already had to answer (which file is the newest trusted
// backup, and how big is it) plus the two counts the health endpoint reports,
// so the whole thing costs a single ReadDir per tick rather than one per
// caller.
//
// It deliberately does NOT evaluate retention — which files SHOULD be here is
// Prune's business, and duplicating that logic is how the two drift.
type dirScan struct {
	newestTrustedAt   time.Time
	newestTrustedSize int64
	trustedCount      int
	quarantinedCount  int
}

// scanBackupDir walks dir once and classifies its contents.
//
// "Trusted" means a name that round-trips through ParseFilename AND has a
// matching .sha256 sidecar. A file that matches the name but lacks a sidecar
// is ignored: by the Phase 1.3 invariant it was never verified, so counting it
// as a restore point (or using its size as a baseline) would propagate
// whatever caused the sidecar to go missing.
//
// "Quarantined" means a name that parses only after stripping a ".corrupt"
// suffix. That narrowness is load-bearing for the same reason it is in Prune:
// genuine operator files (a README, a manual export) must fall through
// unclassified rather than being counted as backup artefacts.
//
// A missing directory returns a zero scan and a nil error. That is the normal
// state of a fresh install and of every tick before the first snapshot lands,
// and it is honestly described by "zero backups" rather than by an error.
func scanBackupDir(dir string) (dirScan, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return dirScan{}, nil
		}
		return dirScan{}, fmt.Errorf("read backup directory: %w", err)
	}

	// Sidecar lookup built first so the classification pass is O(1) per file.
	sidecars := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			sidecars[e.Name()] = true
		}
	}

	var scan dirScan
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if ts, perr := ParseFilename(name); perr == nil {
			if !sidecars[name+".sha256"] {
				continue
			}
			scan.trustedCount++
			if scan.newestTrustedAt.IsZero() || ts.After(scan.newestTrustedAt) {
				info, ierr := e.Info()
				if ierr != nil {
					continue
				}
				scan.newestTrustedAt = ts
				scan.newestTrustedSize = info.Size()
			}
			continue
		}
		if stripped, ok := strings.CutSuffix(name, ".corrupt"); ok {
			if _, perr := ParseFilename(stripped); perr == nil {
				scan.quarantinedCount++
			}
		}
	}
	return scan, nil
}
