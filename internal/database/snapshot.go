package database

// This file implements pre-migration database snapshots — the Phase 4.1
// consumer of the Tier 1 backup primitive. Before RunMigrations applies any
// pending schema migration, SnapshotForMigration takes a VACUUM INTO copy
// of the live database to a sibling directory so an operator can recover
// by file-copy if the migration goes wrong. pruneMigrationSnapshots keeps
// that directory bounded to a small, hardcoded number of most-recent
// snapshots.
//
// The actual VACUUM INTO / sidecar write is delegated to backup.Run; we
// deliberately avoid a second implementation of that logic here because a
// divergence between the two paths would weaken the invariants Tier 1's
// tests already enforce.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/backup"
)

const (
	// migrationSnapshotPrefix distinguishes these snapshots from the
	// scheduled backups produced by Tier 1's scheduler (which use the
	// "spendrop-" prefix). An operator listing the backup directory
	// should be able to tell routine backups from emergency
	// pre-migration snapshots at a glance.
	migrationSnapshotPrefix = "pre-migration-"

	// migrationSnapshotSuffix matches Tier 1's "Z.db" suffix: capital-Z
	// denotes UTC, ".db" is the SQLite file extension.
	migrationSnapshotSuffix = "Z.db"

	// migrationSnapshotTimeLayout is a UTC, seconds-precision, no-colon
	// time layout. It deliberately differs from Tier 1's *minute*
	// precision: if a migration fails and the operator immediately
	// restarts the container (the typical recovery path), the next
	// RunMigrations call may land within the same minute as the failed
	// one, and backup.Snapshot refuses to overwrite an existing
	// destination. Seconds precision collapses the collision window from
	// ~1min to ~1s, making crash-loop retries operator-friendly. The
	// literal "Z" is appended by migrationSnapshotSuffix (not passed
	// through time.Format) because Go's time package treats a bare Z as
	// the RFC 3339 zone designator and would drop it.
	migrationSnapshotTimeLayout = "2006-01-02T150405"
)

// SnapshotForMigration writes a VACUUM INTO copy of the database at dbPath
// to snapshotDir under the canonical name:
//
//	pre-migration-<version>-<UTC YYYY-MM-DDTHHMMSS>Z.db
//
// with an adjacent .sha256 sidecar. version must be the bare migration
// name *without* the .sql suffix (e.g. "005_transactions_soft_delete"); the
// caller is expected to produce it by trimming .sql from the pending
// migration filename. The function creates snapshotDir if it does not
// exist and returns the full path of the written .db file on success.
//
// This is a thin orchestration layer over backup.Run, the Tier 1 primitive
// that performs the atomic snapshot + sidecar write with rollback on
// failure. We do NOT re-implement VACUUM INTO here; a second copy would
// drift from the verified Tier 1 path the moment either changed.
//
// version must not contain the "-" character: the canonical filename uses
// "-" as the separator between version and timestamp, and parseback
// (pruneMigrationSnapshots's predicate) depends on that separator being
// unambiguous. Migration versions in this repo use only [0-9A-Za-z_], so
// in practice this restriction is never tripped — the check exists to
// fail-loud if a future author adds a dashed migration name.
func SnapshotForMigration(ctx context.Context, dbPath, snapshotDir, version string, busyTimeout time.Duration) (string, error) {
	if dbPath == "" {
		return "", errors.New("SnapshotForMigration: dbPath must not be empty")
	}
	if snapshotDir == "" {
		return "", errors.New("SnapshotForMigration: snapshotDir must not be empty")
	}
	if version == "" {
		return "", errors.New("SnapshotForMigration: version must not be empty")
	}
	if strings.Contains(version, "-") {
		return "", fmt.Errorf("SnapshotForMigration: version must not contain '-' (got %q)", version)
	}

	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return "", fmt.Errorf("create migration snapshot dir: %w", err)
	}

	name := formatMigrationSnapshotName(version, time.Now())
	dst := filepath.Join(snapshotDir, name)

	if err := backup.Run(ctx, dbPath, busyTimeout, dst); err != nil {
		return "", fmt.Errorf("snapshot %s: %w", dst, err)
	}
	return dst, nil
}

// formatMigrationSnapshotName builds a canonical pre-migration snapshot
// filename. t is normalized to UTC inside this function so callers in
// different local timezones produce identical names for the same instant.
func formatMigrationSnapshotName(version string, t time.Time) string {
	return migrationSnapshotPrefix + version + "-" + t.UTC().Format(migrationSnapshotTimeLayout) + migrationSnapshotSuffix
}

// parseMigrationSnapshotName extracts the version and timestamp from a canonical
// pre-migration snapshot filename. It returns (version, time, true) for names that
// match the shape, and ("", zero, false) otherwise. pruneMigrationSnapshots
// uses the ok predicate as a filter so operator files (or sibling Tier 1
// scheduled backups that happen to share the directory) are left alone.
//
// The timestamp portion is always exactly tsLen chars and sits at the tail
// of the name, preceded by a "-" separator. We cannot use strings.LastIndex
// to locate that separator because the timestamp itself contains "-"
// characters; a fixed-offset slice is the only reliable approach.
//
// Note: the parser does NOT enforce the "no dash in version" guard that
// SnapshotForMigration applies at write time. A hand-dropped file named
// pre-migration-foo-bar-<ts>Z.db parses successfully (the "foo-bar" slice
// sits harmlessly to the left of the final "-<ts>" segment) and would be
// treated as a prune candidate. Operators must not drop files with the
// canonical prefix into the snapshot directory unless they intend them to
// be managed by this module.
func parseMigrationSnapshotName(name string) (string, time.Time, bool) {
	if !strings.HasPrefix(name, migrationSnapshotPrefix) {
		return "", time.Time{}, false
	}
	if !strings.HasSuffix(name, migrationSnapshotSuffix) {
		return "", time.Time{}, false
	}
	mid := name[len(migrationSnapshotPrefix) : len(name)-len(migrationSnapshotSuffix)]

	const tsLen = len(migrationSnapshotTimeLayout)
	// Minimum mid: at least one version char + "-" + timestamp.
	if len(mid) < tsLen+2 {
		return "", time.Time{}, false
	}
	sep := len(mid) - tsLen - 1
	if mid[sep] != '-' {
		return "", time.Time{}, false
	}
	ts := mid[sep+1:]
	t, err := time.ParseInLocation(migrationSnapshotTimeLayout, ts, time.UTC)
	if err != nil {
		return "", time.Time{}, false
	}
	return mid[:sep], t, true
}

// pruneMigrationSnapshots removes all but the `keep` most recent
// pre-migration snapshots from dir. "Most recent" is determined by the
// timestamp encoded in the filename, not by filesystem mtime — mtime is
// not a reliable ordering under tools like rsync or `cp -p`.
//
// When anchorVersion is non-empty, the oldest snapshot encoding that
// version (name as tiebreaker) is exempt from removal and the rest
// compete for keep-1 slots; when empty, the original newest-keep rule
// applies unchanged. An anchorVersion matching no file degrades to the
// empty behavior.
//
// Files that do not match the canonical pre-migration-*Z.db shape are
// ignored: operator files and the sibling Tier 1 scheduled backups both
// live in the same tree, and we must not touch either. For each snapshot
// we remove, we also best-effort remove the adjacent .sha256 sidecar.
//
// Per-file remove failures are logged and pruning continues: the goal is
// to keep the directory from growing unbounded, not to guarantee
// bit-perfect cleanup. Only a read-dir failure surfaces as a return
// error, because that indicates the prune logic couldn't even enumerate
// its input.
func pruneMigrationSnapshots(dir string, keep int, anchorVersion string) error {
	if keep < 0 {
		return fmt.Errorf("pruneMigrationSnapshots: keep must be non-negative (got %d)", keep)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // nothing to prune
		}
		return fmt.Errorf("read snapshot dir: %w", err)
	}

	type snapFile struct {
		name    string
		version string
		ts      time.Time
	}
	var snaps []snapFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ver, ts, ok := parseMigrationSnapshotName(e.Name())
		if !ok {
			continue
		}
		snaps = append(snaps, snapFile{name: e.Name(), version: ver, ts: ts})
	}

	// Anchor exemption (B3): when anchorVersion is set, the OLDEST
	// snapshot carrying that version is the bracket's pristine
	// pre-upgrade copy — the only full-rollback point once a partial
	// apply has committed earlier migrations — and is exempt from
	// deletion absolutely (even at keep=0). The remaining files then
	// compete for keep-1 slots so total retention stays ≤ keep. With
	// anchorVersion == "" (the success path) behavior is byte-identical
	// to the original newest-keep rule.
	anchorIdx := -1
	if anchorVersion != "" {
		for i, s := range snaps {
			if s.version != anchorVersion {
				continue
			}
			if anchorIdx == -1 {
				anchorIdx = i
				continue
			}
			a := snaps[anchorIdx]
			if s.ts.Before(a.ts) || (s.ts.Equal(a.ts) && s.name < a.name) {
				anchorIdx = i
			}
		}
	}

	candidates := snaps
	slots := keep
	if anchorIdx != -1 {
		candidates = make([]snapFile, 0, len(snaps)-1)
		for i, s := range snaps {
			if i != anchorIdx {
				candidates = append(candidates, s)
			}
		}
		if slots > 0 {
			slots--
		}
	}

	if len(candidates) <= slots {
		return nil
	}

	// Sort oldest → newest so we can trim the head cleanly. SliceStable
	// with a filename tiebreaker gives deterministic ordering when two
	// snapshots share the same second (distinct version strings can still
	// produce distinct-but-same-second filenames), so prune decisions are
	// reproducible across runs.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].ts.Equal(candidates[j].ts) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].ts.Before(candidates[j].ts)
	})

	toRemove := candidates[:len(candidates)-slots]
	for _, s := range toRemove {
		dbPath := filepath.Join(dir, s.name)
		sidecarPath := dbPath + ".sha256"
		// If the .db removal fails, leave the sidecar in place so the
		// .db/.sidecar pair stays consistent for a later prune. Deleting
		// the sidecar while the .db is still on disk would orphan the
		// checksum and a subsequent prune would silently remove the .db
		// without any "this file was trusted" record.
		if err := os.Remove(dbPath); err != nil {
			log.Printf("prune migration snapshot %s: %v", dbPath, err)
			continue
		}
		if err := os.Remove(sidecarPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("prune migration sidecar %s: %v", sidecarPath, err)
		}
	}
	return nil
}
