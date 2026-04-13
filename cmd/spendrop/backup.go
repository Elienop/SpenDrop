// SpenDrop CLI subcommands.
//
// This file implements the `backup` subcommand. It produces a consistent,
// WAL-aware copy of the live SpenDrop database to a target path using
// SQLite's online-backup primitive (VACUUM INTO), and writes a sidecar
// SHA-256 checksum file at "<dst>.sha256".
//
// The subcommand is intended to be invoked from inside the running container:
//
//	docker exec spendrop ./spendrop backup /app/data/backups/test.db
//
// VACUUM INTO is atomic, page-consistent across the WAL, and produces a fully
// defragmented copy in one statement. It does not create -wal/-shm companion
// files alongside the destination, so a backup produced by this subcommand is
// a single self-contained file plus its sidecar.

package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/elienop/spendrop/internal/config"
)

// runBackup writes a snapshot of the live database at cfg.DBPath to dst, then
// writes a sidecar SHA-256 checksum file at dst+".sha256". It refuses to
// overwrite an existing destination so a misconfigured second invocation
// cannot silently destroy a prior backup.
//
// The function uses VACUUM INTO under the hood, which is the SQLite online
// backup primitive: it acquires a read snapshot of the source, copies every
// page through the page cache, and produces a defragmented destination in a
// single atomic statement. Concurrent writers to the source are blocked only
// for the duration the snapshot is acquired (microseconds), not for the full
// copy. The destination contains a consistent point-in-time snapshot of the
// source: any in-flight transaction either commits before the snapshot point
// (and is in the backup) or after (and is not). There is no torn-write window.
func runBackup(ctx context.Context, cfg *config.Config, dst string) error {
	if dst == "" {
		return fmt.Errorf("backup destination path must not be empty")
	}

	// Refuse to overwrite an existing file. A second invocation against the
	// same target is almost certainly an operator mistake; fail loudly
	// instead of silently destroying the prior backup.
	//
	// Note: this is a TOCTOU check — a second process could create dst
	// between our Stat and VACUUM INTO. That is acceptable here because
	// (a) this subcommand is operator-invoked, not racing automation, and
	// (b) VACUUM INTO itself refuses to write into an existing file, so
	// the worst case is a loud error from SQLite instead of a loud error
	// from us. Phase 1.2's scheduled-snapshot path will want a stronger
	// guard (O_EXCL create-to-reserve, then rename-from-tmp) because it
	// runs unattended — do NOT copy this stat-then-act shape there.
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("refusing to overwrite existing file: %s", dst)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat destination: %w", err)
	}

	// Refuse if a stale sidecar exists with no companion DB file. That state
	// only arises from a previously-failed backup run; preserving it lets the
	// operator notice and clean up rather than us silently overwriting.
	sidecarPath := dst + ".sha256"
	if _, err := os.Stat(sidecarPath); err == nil {
		return fmt.Errorf("refusing to overwrite existing sidecar: %s", sidecarPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat sidecar: %w", err)
	}

	// mkdir -p the parent so the operator does not have to pre-create it.
	if parent := filepath.Dir(dst); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create backup directory: %w", err)
		}
	}

	// Open the source with a minimal DSN: just the busy_timeout. Do NOT
	// reuse cfg.SQLiteDSN() here — that DSN sets _journal_mode=WAL, which
	// go-sqlite3 executes as a pragma write at connection-open time. A
	// backup primitive's contract is "read-only snapshot"; it must not
	// silently mutate the source database's journal mode as a side effect
	// of being run. The server process has already put the DB in WAL mode
	// at startup, so we inherit that mode; we only need the busy_timeout
	// so a brief write-lock hold from the server does not immediately
	// error us out. The CLI runs as a separate OS process; SQLite's WAL
	// mode + busy_timeout handles cross-process concurrency for us.
	dsn := fmt.Sprintf("%s?_busy_timeout=%d", cfg.DBPath, cfg.SQLite.BusyTimeout.Milliseconds())
	src, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open source database: %w", err)
	}
	defer src.Close()

	// Pin to a single connection so PRAGMA / VACUUM INTO state is consistent.
	src.SetMaxOpenConns(1)

	if err := src.PingContext(ctx); err != nil {
		return fmt.Errorf("ping source database: %w", err)
	}

	// VACUUM INTO does not accept parameter substitution for the destination
	// path: the SQLite grammar parses it as a string literal at prepare
	// time, not as a bindable expression. (The data-stewardship plan's
	// pseudocode shows `VACUUM INTO ?` — that is wrong; this comment is
	// why we diverge.) We must inline the path as a SQLite string literal,
	// which means single-quoting it and doubling any embedded single
	// quotes per SQLite syntax. Backslashes are NOT escape characters in
	// SQLite literals, so doubling single quotes is sufficient to
	// neutralize any path the operator can pass on the command line.
	stmt := "VACUUM INTO " + quoteSQLiteString(dst)
	if _, err := src.ExecContext(ctx, stmt); err != nil {
		// Best-effort cleanup of any partial files VACUUM INTO may have
		// left behind on failure. In practice VACUUM INTO produces no
		// -wal/-shm/-journal companions, but sweep them defensively so a
		// partial state can never be mistaken for a valid backup by any
		// later phase. Ignored if the files do not exist. dst is already
		// visible to the operator on the command line, so we don't repeat
		// it in the wrapped error.
		for _, ext := range []string{"", "-wal", "-shm", "-journal"} {
			_ = os.Remove(dst + ext)
		}
		return fmt.Errorf("VACUUM INTO: %w", err)
	}

	// Compute the hash of the resulting file and write the sidecar in
	// `sha256sum` format ("<hex>  <basename>\n"). The sidecar's existence is
	// the marker every later phase will use to decide whether a file is
	// trusted; we only write it after the backup is fully on disk.
	sum, err := sha256File(dst)
	if err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("hash backup: %w", err)
	}

	// Write the sidecar via write-to-tmp + atomic rename. A straight
	// WriteFile can leave a partial sidecar at the final path if the
	// process is killed, the disk fills, or a syscall is interrupted
	// mid-write — and the next run's orphan-sidecar guard above would
	// then refuse to proceed against a valid backup. The tmp file lives
	// in the same directory as the destination so the rename stays on
	// one filesystem and is atomic on every supported OS.
	sidecarContent := fmt.Sprintf("%s  %s\n", sum, filepath.Base(dst))
	sidecarTmp := sidecarPath + ".tmp"
	if err := os.WriteFile(sidecarTmp, []byte(sidecarContent), 0o644); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("write sidecar tmp: %w", err)
	}
	if err := os.Rename(sidecarTmp, sidecarPath); err != nil {
		_ = os.Remove(sidecarTmp)
		_ = os.Remove(dst)
		return fmt.Errorf("rename sidecar: %w", err)
	}

	return nil
}

// quoteSQLiteString returns s wrapped in single quotes, with any embedded
// single quotes doubled per SQLite literal-string syntax. This is the
// canonical way to inline a string into a SQL statement when parameter
// substitution is not available (as with VACUUM INTO).
func quoteSQLiteString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\'')
		} else {
			out = append(out, s[i])
		}
	}
	out = append(out, '\'')
	return string(out)
}

// sha256File returns the lowercase hex SHA-256 digest of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

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
		// Cap the run at 5 minutes. A VACUUM INTO on a typical
		// household-sized SpenDrop database completes in well under a
		// second; if we are still running after 5 minutes something is
		// badly wrong (stuck writer, runaway DB growth, disk stall) and
		// we want to surface it as a failed exit rather than hang the
		// operator's shell indefinitely.
		backupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		startedAt := time.Now()
		if err := runBackup(backupCtx, cfg, dst); err != nil {
			fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
			return true, 1
		}
		fmt.Printf("backup ok: %s (%s)\n", dst, time.Since(startedAt).Round(time.Millisecond))
		return true, 0
	default:
		return false, 0
	}
}
