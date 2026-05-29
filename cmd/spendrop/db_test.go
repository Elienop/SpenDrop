package main

import (
	"path/filepath"
	"testing"
)

// TestMainDBHandle_PinsMaxOpenConnsToOne asserts the production *sql.DB handle
// opened by openDB serializes on a single OS connection, matching the
// "No concurrent writers — serialize writes" invariant and the
// backup/verify/scheduler helpers that already pin to one.
func TestMainDBHandle_PinsMaxOpenConnsToOne(t *testing.T) {
	cfg := dispatchCfg(t, filepath.Join(t.TempDir(), "pin.db"))

	db, err := openDB(cfg)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
}
