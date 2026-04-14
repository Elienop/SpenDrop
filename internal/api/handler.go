package api

import (
	"database/sql"
	"sync"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// Handler holds dependencies for all API handlers.
type Handler struct {
	queries  *database.Queries
	db       *sql.DB
	txnStore *database.TransactionStore

	// integrityMu guards lastIntegrityCheckAt / lastIntegrityCheckResult.
	// The `/healthz/data` handler reads these under an RLock on every
	// request (so monitoring scrapers at 1 QPS cannot stampede a single
	// writer), while the startup path and the daily background ticker
	// in main.go write them under a full Lock once per interval. An
	// RWMutex is the right fit because reads dominate writes by several
	// orders of magnitude at the expected QPS.
	//
	// Zero-value state ("", time.Time{}) is the legitimate "never ran
	// yet" state; main.go is expected to call SetIntegrityResult at
	// startup immediately after migrations succeed so the first
	// /healthz/data request always sees a populated value, but the
	// /healthz/data handler still has to handle the zero value
	// gracefully in case an operator hits the endpoint during a boot
	// that's stuck before the startup check completes.
	integrityMu              sync.RWMutex
	lastIntegrityCheckAt     time.Time
	lastIntegrityCheckResult string
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

// SetIntegrityResult records the outcome of a PRAGMA integrity_check run
// along with the wall-clock instant at which it finished. Called from
// cmd/spendrop/main.go: once synchronously after migrations (for the
// startup check) and once per tick of the daily background goroutine.
// The /healthz/data handler reads the stored pair under the same mutex.
//
// Passing an empty result or a zero time is permitted — the zero value
// is the legitimate "never checked" state for a Handler constructed
// outside of main.go (for example, every NewHandler-based unit test),
// and the health endpoint degrades gracefully when it sees it.
func (h *Handler) SetIntegrityResult(at time.Time, result string) {
	h.integrityMu.Lock()
	defer h.integrityMu.Unlock()
	h.lastIntegrityCheckAt = at
	h.lastIntegrityCheckResult = result
}

// getIntegrityResult returns the most recently recorded
// integrity-check pair. Used by the /healthz/data handler. Package-private
// because external callers have no legitimate reason to poke at the
// cached value — they should either run their own check or read the
// HTTP endpoint.
func (h *Handler) getIntegrityResult() (time.Time, string) {
	h.integrityMu.RLock()
	defer h.integrityMu.RUnlock()
	return h.lastIntegrityCheckAt, h.lastIntegrityCheckResult
}
