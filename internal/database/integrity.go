package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// IntegrityOK is the string SQLite returns from PRAGMA integrity_check /
// PRAGMA quick_check when the database is healthy. Any other value — a
// list of error messages — indicates corruption.
//
// Both pragmas return a single row with the column value set to "ok" on
// success, and one or more rows (or a multi-line string) describing the
// first errors encountered on failure. Callers compare against this
// constant rather than string-literalling "ok" at each site so the
// intent is obvious and greppable.
const IntegrityOK = "ok"

// IntegrityCheck runs the full `PRAGMA integrity_check` against db and
// returns the concatenated result. A healthy database returns exactly
// IntegrityOK ("ok"); a corrupt database returns a multi-line list of
// the first problems SQLite found (the default limit is 100 errors).
//
// This check walks every page in the database file and verifies every
// index — at 10K rows it is sub-second, but on a multi-GB database it
// can take minutes. It should never be exposed to an unauthenticated
// HTTP endpoint; callers route it through the startup path and the
// daily background ticker instead.
//
// The caller-provided ctx governs cancellation; SQLite will abort a
// long-running check if the context is cancelled mid-scan.
func IntegrityCheck(ctx context.Context, db *sql.DB) (string, error) {
	return runIntegrityPragma(ctx, db, "PRAGMA integrity_check")
}

// QuickCheck runs the fast `PRAGMA quick_check` which validates the
// page structure without cross-checking indexes against the tables
// they back. At household scale it is sub-millisecond, so it is safe
// to call synchronously from per-request paths such as `/healthz/data`.
//
// quick_check catches the common failure modes (truncated files,
// torn page writes, malformed pages) but will miss silent index
// corruption where the index entries no longer match the row data.
// The daily full IntegrityCheck exists to catch that class.
func QuickCheck(ctx context.Context, db *sql.DB) (string, error) {
	return runIntegrityPragma(ctx, db, "PRAGMA quick_check")
}

// runIntegrityPragma is the shared implementation for IntegrityCheck
// and QuickCheck. Both pragmas return a result set with a single
// string column; on success the set has exactly one row with value
// "ok"; on failure it has one row per error (and can contain many).
// We materialize every row into a single newline-joined string so the
// caller can either compare against IntegrityOK for the happy path
// or log the full result on failure.
//
// The pragma name is passed in literally (never from user input) so
// it cannot be parameterised; SQLite does not allow `?` placeholders
// in PRAGMA statements in any case.
func runIntegrityPragma(ctx context.Context, db *sql.DB, pragma string) (string, error) {
	rows, err := db.QueryContext(ctx, pragma)
	if err != nil {
		return "", fmt.Errorf("%s: %w", pragma, err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", fmt.Errorf("%s scan: %w", pragma, err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("%s rows: %w", pragma, err)
	}
	return strings.Join(lines, "\n"), nil
}

// RunIntegrityCheckAtStartup runs the full IntegrityCheck synchronously
// during process boot. It is expected to be called immediately after
// RunMigrations and before the HTTP server binds a port, so that a
// corrupt database is detected before any new write can roll forward
// on top of the damage.
//
// The function returns (result, error) rather than calling log.Fatalf
// itself so that callers in tests can exercise both the happy and
// sad path without the process exiting. Production main.go is
// expected to treat any return where err != nil OR result != "ok"
// as fatal and exit non-zero with the full result in the log line —
// the log message is the operator's only clue to the nature of the
// corruption, so it must not be truncated.
func RunIntegrityCheckAtStartup(ctx context.Context, db *sql.DB) (string, error) {
	return IntegrityCheck(ctx, db)
}
