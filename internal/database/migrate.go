package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/backup"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrationOptions carries the inputs RunMigrations needs beyond the raw
// *sql.DB handle. It is a struct rather than a widening parameter list so
// future phases can add knobs (e.g. a "skip snapshot" flag for read-only
// mounts) without breaking call sites.
//
// DBPath is the filesystem location of the live database. SnapshotDir is
// where pre-migration snapshots are written and pruned to the most-recent
// few. BusyTimeout is the SQLite busy_timeout applied to the read
// connection SnapshotForMigration opens; pass the same value the server
// uses (cfg.SQLite.BusyTimeout is the typical source).
type MigrationOptions struct {
	DBPath      string
	SnapshotDir string
	BusyTimeout time.Duration
}

// migrationSnapshotKeep is the hardcoded retention count for pre-migration
// snapshots. Phase 4.1's spec recommends hardcoding this (rather than
// making it env-configurable) because the artifact is only load-bearing in
// the window between "migration just ran" and "operator verified the app
// came up cleanly". Anything older has been superseded by newer snapshots
// or by the sibling Tier 1 scheduled backups. Three is enough to survive
// two back-to-back restarts without losing the pristine pre-upgrade copy.
//
// It must not drop below 3. The failure path can hold two exemptions at
// once (the floor-version pristine copy and the bracket anchor), and the
// one remaining slot is what keeps the snapshot the "restore from <path>"
// error names on disk. At keep <= 2 that file could be pruned in the same
// breath as the error naming it.
const migrationSnapshotKeep = 3

// RunMigrations applies every .sql migration file embedded in
// internal/database/migrations that has not yet been recorded in the
// schema_migrations tracking table. It is idempotent: if all migrations
// are already applied, it is a no-op and takes no snapshot.
//
// Before applying any pending migration, RunMigrations calls
// SnapshotForMigration to write a VACUUM INTO copy of the live database
// to opts.SnapshotDir. If the snapshot fails, RunMigrations returns an
// error prefixed with "refusing to migrate" and no migration row is
// inserted into schema_migrations — the server process will not start
// without a recovery anchor. Note: ensureMigrationsTable runs as the
// first step of every invocation, so a refuse-to-migrate return on a
// fresh database leaves an empty schema_migrations table on disk; the
// user data is untouched but the tracking table now exists. If a
// migration *apply* fails after the snapshot, the returned error names
// the snapshot path so the operator has a straight-line recovery
// instruction ("restore from <path>").
//
// RunMigrations prunes opts.SnapshotDir on both exits: after a
// successful apply it keeps the `migrationSnapshotKeep` most recent
// snapshots; after a FAILED apply it prunes to the same bound but
// exempts up to two files so a crash-looping migration cannot fill the
// disk yet never loses its pristine pre-upgrade copy — the oldest
// snapshot at or above the FIRST pending migration (the pristine copy,
// pinned by a floor that a newly shipped migration cannot rotate away)
// and the bracket anchor, the oldest snapshot for the current target
// version. Prune failures are logged and never abort startup or mask
// the migration error.
//
// The pin is self-limiting: the success path passes the zero-value
// pruneExemptions, so the first migration run that actually succeeds
// releases every pinned file back into the ordinary newest-keep rule.
func RunMigrations(db *sql.DB, opts MigrationOptions) error {
	if err := ensureMigrationsTable(db); err != nil {
		return err
	}

	pending, err := listPendingMigrations(db)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	// Derive the target-version label for the snapshot filename from the
	// last (highest-sorted) pending migration. Using the highest pending
	// version lets an operator glance at snapshot filenames and see the
	// upgrade bracket: the snapshot captures state *before* this version
	// was applied.
	targetVersion := strings.TrimSuffix(pending[len(pending)-1], ".sql")

	// context.Background() is deliberate here: we are running at process
	// start before any server context exists, and we specifically do NOT
	// want a fast shutdown signal cancelling VACUUM INTO mid-copy. A
	// partial .db sitting in the snapshot directory would have no matching
	// sidecar (backup.Run cleans its own failure path but cannot intercept
	// SIGKILL) and nothing automatic would later catch it — recovery would
	// need an operator-run `sha256sum -c`. Let the snapshot run to
	// completion.
	snapPath, err := SnapshotForMigration(context.Background(), opts.DBPath, opts.SnapshotDir, targetVersion, opts.BusyTimeout)
	if err != nil {
		// Both arms refuse to migrate — a rollback anchor we cannot
		// trust is worth no more than one we could not write. They are
		// worded apart because the likely cause differs: a write
		// failure is a disk or mount problem, while a verification
		// failure usually means the copy did not match the live
		// database. Only usually, though — Verify also fails when it
		// could not check at all (stat/open failure, deadline mid-read),
		// which points back at the volume. The wrapped chain names the
		// step that failed, so the operator gets the specific cause
		// rather than our guess at it. backup.Run attempts to remove
		// the file in both cases and reports if it could not, so a
		// bogus anchor cannot be left behind silently.
		if errors.Is(err, backup.ErrVerifyFailed) {
			return fmt.Errorf("pre-migration snapshot failed verification (refusing to migrate): %w", err)
		}
		return fmt.Errorf("pre-migration snapshot failed (refusing to migrate): %w", err)
	}
	log.Printf("Pre-migration snapshot: %s", snapPath)

	if err := applyPendingMigrations(db, pending); err != nil {
		// Crash-loop guard (B3): each retry lands a fresh snapshot
		// (seconds-precision names never collide across restarts), and
		// before this call existed nothing pruned on the failure path —
		// a failing migration under Docker restart filled the disk one
		// full DB copy per attempt. Prune here too, but never the
		// pristine pre-upgrade copy: once a partial apply has committed
		// earlier migrations it is the only full-rollback point. It is
		// pinned by the floor (which survives targetVersion rotating when
		// a new migration ships mid-loop) and, when that is a different
		// file, by the bracket anchor. Best-effort — a prune error is
		// logged and must never mask the migration error.
		if perr := pruneMigrationSnapshots(opts.SnapshotDir, migrationSnapshotKeep, failurePruneExemptions(pending)); perr != nil {
			log.Printf("WARN: migration snapshot prune (failure path) failed: %v", perr)
		}
		return fmt.Errorf("migration failed (restore from %s): %w", snapPath, err)
	}

	// Success releases every pin: the zero value is "no exemptions", so
	// the directory falls straight back to the newest-keep rule.
	if err := pruneMigrationSnapshots(opts.SnapshotDir, migrationSnapshotKeep, pruneExemptions{}); err != nil {
		log.Printf("WARN: migration snapshot prune failed: %v", err)
	}
	return nil
}

// failurePruneExemptions derives the failure-path exemptions from the
// pending list RunMigrations already holds: Floor is the FIRST (lowest)
// pending version — the rotation-proof pin on the pristine pre-upgrade
// copy — and Anchor is the LAST (highest) pending version, which is the
// targetVersion the current bracket's snapshots are named for.
//
// An empty pending list yields the zero value (no exemptions). That is
// unreachable in production, because RunMigrations returns early when
// nothing is pending, but the function stays total so a future caller
// cannot index a nil slice through it.
func failurePruneExemptions(pending []string) pruneExemptions {
	if len(pending) == 0 {
		return pruneExemptions{}
	}
	return pruneExemptions{
		Anchor: strings.TrimSuffix(pending[len(pending)-1], ".sql"),
		Floor:  strings.TrimSuffix(pending[0], ".sql"),
	}
}

// ensureMigrationsTable creates the schema_migrations tracking table if
// it doesn't already exist. It's called as the first step of every
// RunMigrations invocation so subsequent queries against the table can
// assume it's present.
func ensureMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	return nil
}

// listPendingMigrations returns every migration filename present in the
// embedded migrations FS that does not have a matching row in
// schema_migrations. The returned slice is sorted by filename; because we
// prefix migrations with a zero-padded numeric id, filename order is
// apply order.
//
// Implementation note: we load all applied versions into a set with one
// query, then diff against the FS entries, rather than firing one
// SELECT-COUNT per migration. That's not a performance concern (this
// runs once per startup, N is small), but the set-diff version is
// easier to reason about and only has one place to fail.
func listPendingMigrations(db *sql.DB) ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	applied := map[string]struct{}{}
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[v] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}

	var pending []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if _, ok := applied[e.Name()]; ok {
			continue
		}
		pending = append(pending, e.Name())
	}
	sort.Strings(pending)
	return pending, nil
}

// applyPendingMigrations applies each of `names` in order, each inside
// its own transaction, and records it in schema_migrations on success.
// If any migration fails to apply, the transaction rolls back and the
// function returns immediately — subsequent pending migrations are left
// for the next invocation (which will re-snapshot and re-try).
func applyPendingMigrations(db *sql.DB, names []string) error {
	for _, name := range names {
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		log.Printf("Applying migration: %s", name)

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for migration %s: %w", name, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
