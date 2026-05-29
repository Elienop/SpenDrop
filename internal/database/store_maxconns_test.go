package database

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// TestStore_ConcurrentWritersSerialize_NoLockError pins the production
// invariant that a single-connection *sql.DB (SetMaxOpenConns(1), as main.go
// now configures) lets concurrent writers serialize through one OS handle
// instead of racing two pool connections for the write lock. With the pool
// capped at one connection, N goroutines each issuing a store.Create must all
// succeed with no "database is locked" error and the final live count must
// equal N.
//
// This is the behavioral counterpart to the cmd/spendrop openDB pin: it would
// previously have been able to interleave two writers on two pool connections;
// the cap removes that race entirely.
func TestStore_ConcurrentWritersSerialize_NoLockError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "serialize.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The change under test: pin the handle to a single connection so writers
	// serialize rather than race two OS connections for the SQLite write lock.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		t.Fatalf("ping test db: %v", err)
	}

	opts := MigrationOptions{
		DBPath:      dbPath,
		SnapshotDir: filepath.Join(dir, "snapshots"),
		BusyTimeout: 5 * time.Second,
	}
	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	q := New(db)
	store := NewTransactionStore(db, q)

	userID := seedTestStoreUser(t, q, "writer")
	catID := seedTestStoreCategory(t, q, "ConcCat")
	date, err := time.Parse("2006-01-02", "2026-05-29")
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = store.Create(context.Background(), userID, CreateTransactionParams{
				UserID:      userID,
				Date:        date,
				AmountCents: int64(100 + i),
				Description: "concurrent",
				CategoryID:  catID,
			})
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("concurrent Create #%d failed: %v", i, e)
		}
	}

	count, err := q.CountAllTransactions(context.Background())
	if err != nil {
		t.Fatalf("CountAllTransactions: %v", err)
	}
	if count.Live != int64(n) {
		t.Errorf("live transaction count = %d, want %d", count.Live, n)
	}
}
