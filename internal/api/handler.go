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

	// clock is the time source every reports/dashboard handler reads for
	// "current date" decisions (year-over-year default year, rolling
	// trend windows, YTD end-of-month). Phase 3.2 introduces it so the
	// report test suite can drive handlers from a frozen instant without
	// touching the global wall clock.
	//
	// NewHandler initializes this to realClock{}; NewHandlerWithClock
	// accepts a caller-supplied implementation for test setup. All
	// non-test constructors must route through NewHandler so production
	// code never has a chance to pick up a fixedClock accidentally.
	clock Clock

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
//
// Phase 3.2 added a clock dependency for the reports/dashboard surface.
// NewHandler keeps the same `(queries, db)` signature and initializes the
// clock to realClock{} internally, again so the 50+ pre-existing test call
// sites compile unchanged and every production caller picks up real wall
// time. Tests that need a frozen instant must go through NewHandlerWithClock.
func NewHandler(queries *database.Queries, db *sql.DB) *Handler {
	return &Handler{
		queries:  queries,
		db:       db,
		txnStore: database.NewTransactionStore(db, queries),
		clock:    realClock{},
	}
}

// NewHandlerWithClock is the test-only constructor for Handlers that need a
// fixed time source. Production code must call NewHandler instead so a
// fixedClock cannot leak into a running server. Kept as a separate function
// rather than a functional option so a misuse in production grep's easily
// (`NewHandlerWithClock` outside a _test.go file is a bug).
func NewHandlerWithClock(queries *database.Queries, db *sql.DB, clock Clock) *Handler {
	return &Handler{
		queries:  queries,
		db:       db,
		txnStore: database.NewTransactionStore(db, queries),
		clock:    clock,
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
