// SpenDrop CLI subcommands.
//
// This file implements the `backup` subcommand dispatcher. The real backup
// primitive lives in internal/backup — see backup.Run for the VACUUM INTO
// logic, filename format, and load-bearing comments about DSN/TOCTOU/etc.
//
// The subcommand is intended to be invoked from inside the running container:
//
//	docker exec spendrop ./spendrop backup /app/data/backups/test.db

package main

import (
	"context"
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
//
// Adding more subcommands later: extend the switch and document the usage
// line.
func dispatchSubcommand(ctx context.Context, cfg *config.Config) (handled bool, exitCode int) {
	if len(os.Args) < 2 {
		return false, 0
	}
	switch os.Args[1] {
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
		// Cap the run at 5 minutes. A VACUUM INTO on a typical
		// household-sized SpenDrop database completes in well under a
		// second; if we are still running after 5 minutes something is
		// badly wrong (stuck writer, runaway DB growth, disk stall) and
		// we want to surface it as a failed exit rather than hang the
		// operator's shell indefinitely.
		backupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		startedAt := time.Now()
		if err := backup.Run(backupCtx, cfg.DBPath, cfg.SQLite.BusyTimeout, dst); err != nil {
			fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
			return true, 1
		}
		fmt.Printf("backup ok: %s (%s)\n", dst, time.Since(startedAt).Round(time.Millisecond))
		return true, 0
	default:
		return false, 0
	}
}
