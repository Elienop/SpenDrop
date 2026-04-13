package api

import (
	"database/sql"

	"github.com/elienop/spendrop/internal/database"
)

// Handler holds dependencies for all API handlers.
type Handler struct {
	queries  *database.Queries
	db       *sql.DB
	txnStore *database.TransactionStore
}

// NewHandler creates a new Handler with the given dependencies.
//
// The txnStore is derived from queries+db rather than accepted as a third
// parameter so that every existing NewHandler(q, db) call site — there are
// fifty-plus of them across the *_test.go files — automatically picks up
// audit logging without needing a signature change. Every mutating
// transaction endpoint routes through txnStore so the audit row commits in
// the same SQL transaction as the data row.
func NewHandler(queries *database.Queries, db *sql.DB) *Handler {
	return &Handler{
		queries:  queries,
		db:       db,
		txnStore: database.NewTransactionStore(db, queries),
	}
}
