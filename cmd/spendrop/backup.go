// SpenDrop CLI subcommands.
//
// This file implements dispatchSubcommand plus the `backup` subcommand body.
// Additional subcommands live in sibling files (e.g. audit.go) and are
// dispatched from the switch below. The real backup primitive lives in
// internal/backup — see backup.Run for the VACUUM INTO logic, filename
// format, and load-bearing comments about DSN/TOCTOU/etc.
//
// Subcommands are intended to be invoked from inside the running container:
//
//	docker exec spendrop ./spendrop backup /app/data/backups/test.db
//	docker exec spendrop ./spendrop audit --transaction-id 1234

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/backup"
	"github.com/elienop/spendrop/internal/config"
)

// dispatchSubcommand inspects os.Args[1] and either runs a CLI subcommand and
// returns (handled=true, exitCode), or returns (handled=false, 0) to let main
// fall through to the normal HTTP-server path.
//
// Subcommands:
//
//	spendrop backup <destination-path>
//	spendrop audit [--transaction-id <id>] [--since <time>] [--limit <n>]
//
// Adding more subcommands later: extend the switch and document the usage
// line.
func dispatchSubcommand(ctx context.Context, cfg *config.Config) (handled bool, exitCode int) {
	if len(os.Args) < 2 {
		return false, 0
	}
	switch os.Args[1] {
	case "audit":
		return true, auditCmd(ctx, cfg, os.Args[2:])
	case "backup":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: spendrop backup <destination-path>")
			return true, 2
		}
		dst := os.Args[2]
		// Refuse destinations whose basename starts with "spendrop-". That
		// prefix is the scheduler's GFS namespace (see
		// internal/backup/filename.go): Prune walks the backup directory,
		// parses any file matching that prefix as an auto-backup, and
		// deletes it if it does not fall inside one of the keep buckets. A
		// manual CLI snapshot that happens to land in the same dir with
		// the same prefix would be indistinguishable from a retired auto
		// backup and could be pruned on the next scheduler tick — the
		// exact opposite of what an operator running a manual backup
		// wants. Keeping the two namespaces disjoint at the CLI boundary
		// is the simplest way to make this impossible; operators who want
		// a one-off snapshot simply pick any other prefix.
		if strings.HasPrefix(filepath.Base(dst), "spendrop-") {
			fmt.Fprintln(os.Stderr, "refusing 'spendrop-' prefix: that namespace is reserved for scheduled backups")
			return true, 2
		}
		// Cap the copy so a stuck writer or a stalled disk surfaces as
		// a failed exit rather than running unbounded. The cap is
		// derived from the database's own size rather than hard-coded
		// (it used to be a flat 5 minutes): Run now copies AND
		// verifies, so a fixed wall clock that a legitimately large
		// database can outgrow would turn "your backup takes a while"
		// into "your backup is impossible". backup.RunTimeout floors at
		// a generous value, so a household-sized DB — which finishes in
		// well under a second — is unaffected.
		//
		// This does NOT guarantee the command always returns: it bounds
		// VACUUM INTO only. Verify enforces its own budget, and the
		// sidecar's SHA-256 pass is unbounded by anything. See
		// backup.RunTimeout for the full accounting.
		backupCtx, cancel := context.WithTimeout(ctx, backup.RunTimeout(cfg.DBPath))
		defer cancel()
		startedAt := time.Now()
		if err := backup.Run(backupCtx, cfg.DBPath, cfg.SQLite.BusyTimeout, dst); err != nil {
			// Verification failure is reported distinctly from a write
			// failure, because it usually points somewhere else — at
			// the copy or the live database rather than at the target
			// disk. Deliberately not stated more strongly than that:
			// Verify also fails when it could not CHECK (a stat/open
			// failure, a deadline mid-read), which points back at the
			// volume. The wrapped chain names the actual step, so it is
			// printed rather than summarised.
			//
			// "No sidecar was written" is unconditionally true on this
			// path. The fate of the .db is NOT asserted here: Run
			// removes it, but if that removal failed the error text
			// says so, which is why err is printed in full.
			if errors.Is(err, backup.ErrVerifyFailed) {
				fmt.Fprintf(os.Stderr, "backup failed verification; no sidecar was written: %v\n", err)
				return true, 1
			}
			fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
			return true, 1
		}
		fmt.Printf("backup ok: %s (verified, %s)\n", dst, time.Since(startedAt).Round(time.Millisecond))
		return true, 0
	default:
		return false, 0
	}
}
