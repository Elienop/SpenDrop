// SpenDrop CLI `audit` subcommand — dumps rows from the transaction_audit
// table for operator forensics.
//
// The read path is deliberately CLI-only: no REST endpoint returns audit
// rows because the audit log is an operator tool, not a user-facing
// feature. Running `spendrop audit` from inside the container (or via
// `docker exec spendrop ./spendrop audit ...`) is how an operator answers
// questions like "who deleted transaction 1234?" or "what rows did the
// Apr-13 bulk rename actually touch?". Keeping this off the HTTP surface
// also means we do not need to worry about actor redaction, per-user
// authorization, or leaking historical PII through the browser.
//
// Output is one JSON object per line so the command composes cleanly with
// `jq`, `grep`, and `less`. Operators who want a pretty table can pipe
// through `jq -r` with a format string of their choice.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/elienop/spendrop/internal/config"
	"github.com/elienop/spendrop/internal/database"
)

// sqliteDatetimeFormat matches the TEXT layout SQLite's CURRENT_TIMESTAMP
// writes into DATETIME columns. The transaction_audit.occurred_at column
// is stored in this format, and the ListRecentTransactionAudit query
// compares it lexicographically against the Since parameter — so the CLI
// must format its --since flag in the same layout or the range filter
// silently under-matches. Any RFC3339 string with a "T" separator would
// compare incorrectly against rows written with a space separator.
const sqliteDatetimeFormat = "2006-01-02 15:04:05"

// auditCmd parses `audit` subcommand flags and prints matching rows as one
// JSON object per line. Two modes:
//
//   - `--transaction-id N`: every audit row for a single transaction,
//     ordered oldest-first (readable as the full lifecycle of that row).
//   - no transaction ID: recent rows globally, ordered newest-first,
//     bounded by `--since` (default: 24h ago) and `--limit` (default: 100).
//
// The summary row for bulk operations (transaction_id = 0) appears only
// in the recent-rows mode because its transaction_id is the sentinel
// BulkAuditTransactionID; operators investigating a bulk mutation find it
// by timestamp, not by ID.
func auditCmd(ctx context.Context, cfg *config.Config, args []string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: spendrop audit [--transaction-id <id>] [--since <time>] [--limit <n>]")
		fs.PrintDefaults()
	}
	var (
		transactionID int64
		since         string
		limit         int
	)
	fs.Int64Var(&transactionID, "transaction-id", 0, "list every audit row for this transaction ID (oldest first)")
	fs.StringVar(&since, "since", "", "list rows at or after this time; accepts YYYY-MM-DD or RFC3339 (default: 24h ago)")
	fs.IntVar(&limit, "limit", 100, "maximum number of rows to print (must be > 0)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if limit <= 0 {
		fmt.Fprintln(os.Stderr, "--limit must be positive")
		return 2
	}
	if transactionID < 0 {
		fmt.Fprintln(os.Stderr, "--transaction-id must be non-negative")
		return 2
	}

	// openDB pins the handle to a single connection, matching the server
	// handle and the backup helpers (serialize on one conn).
	sqlDB, err := openDB(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer sqlDB.Close()

	q := database.New(sqlDB)
	enc := json.NewEncoder(os.Stdout)

	// transaction-id mode: ListTransactionAuditByID returns every row for
	// a given transaction in chronological order, capped SQL-side by
	// --limit. Pushing the cap into the query (rather than slicing in Go)
	// means a pathological row with a million audit entries does not blow
	// the CLI's memory before it can print the first line.
	if transactionID > 0 {
		rows, err := q.ListTransactionAuditByID(ctx, database.ListTransactionAuditByIDParams{
			TransactionID: transactionID,
			Limit:         int64(limit),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "list audit rows: %v\n", err)
			return 1
		}
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				fmt.Fprintf(os.Stderr, "encode row: %v\n", err)
				return 1
			}
		}
		return 0
	}

	// Recent-rows mode. Format --since in the SQLite TEXT datetime layout
	// (see sqliteDatetimeFormat). --since is parsed as UTC if it carries
	// no timezone so `spendrop audit --since 2026-04-13` works as "any row
	// on or after 2026-04-13 00:00 UTC".
	var sinceTime time.Time
	switch {
	case since == "":
		sinceTime = time.Now().UTC().Add(-24 * time.Hour)
	default:
		parsed, perr := parseAuditSince(since)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "--since: %v\n", perr)
			return 2
		}
		sinceTime = parsed
	}

	rows, err := q.ListRecentTransactionAudit(ctx, database.ListRecentTransactionAuditParams{
		Since: sinceTime.UTC().Format(sqliteDatetimeFormat),
		Limit: int64(limit),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "list audit rows: %v\n", err)
		return 1
	}
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			fmt.Fprintf(os.Stderr, "encode row: %v\n", err)
			return 1
		}
	}
	return 0
}

// parseAuditSince accepts a user-supplied --since value in one of two
// layouts and returns the equivalent time.Time. The date-only form is
// interpreted as UTC midnight so the filter semantics do not depend on
// the operator's local clock.
func parseAuditSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%q is not YYYY-MM-DD or RFC3339", s)
}
