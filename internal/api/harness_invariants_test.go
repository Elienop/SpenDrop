package api

import "testing"

// ptr returns a pointer to v, for populating optional request fields such as
// transactionRequest.Notes where nil and empty mean different things.
func ptr[T any](v T) *T { return &v }

// TestSetupTestDB_MatchesProductionPoolAndFKInvariants guards the two DSN/pool
// properties that make whole classes of bug visible to this suite.
//
// Production opens its handle via openDB (cmd/spendrop/db.go) with
// SetMaxOpenConns(1), and derives the DSN from Config.SQLiteDSN
// (internal/config/config.go) which emits _foreign_keys=on. When the test
// harness diverged from either, two critical defects shipped undetected:
//
//   - an unbounded test pool hid handleExportTransactions issuing a second
//     query while its cursor still held the only production connection;
//   - foreign keys being off hid handleDeleteUser letting ON DELETE CASCADE
//     destroy every transaction a departed member had created.
//
// If this test fails, do not "fix" it by relaxing the assertion — the harness
// has drifted from production and the suite has gone blind again.
func TestSetupTestDB_MatchesProductionPoolAndFKInvariants(t *testing.T) {
	_, db := setupTestDB(t)

	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("test pool cap = %d, want 1 to match openDB's SetMaxOpenConns(1)", got)
	}

	var fkEnabled int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fkEnabled); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1 to match Config.SQLiteDSN", fkEnabled)
	}
}
