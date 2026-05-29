package main

import (
	"database/sql"
	"fmt"

	"github.com/elienop/spendrop/internal/config"
)

// openDB opens the production SQLite handle from the config-derived DSN and
// pins it to a single connection.
//
// SetMaxOpenConns(1) is load-bearing, not a tuning knob: sql.Open returns a
// *sql.DB backed by a connection POOL, and with no cap database/sql opens as
// many OS-level SQLite connections as concurrent goroutines demand. The
// project contract is single-writer serialization ("No concurrent writers —
// serialize writes"), which is structurally guaranteed only when the pool is
// capped at one connection. WAL mode + _busy_timeout makes a contender wait
// rather than fail, but it does not make concurrent writers serialize — it
// only makes the loser retry. The backup/verify/scheduler helpers already pin
// to one; this is the shared open site for the long-lived server handle and
// the short-lived audit CLI, both of which must honor the same invariant.
func openDB(cfg *config.Config) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", cfg.SQLiteDSN())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
