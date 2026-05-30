package api

import (
	"database/sql"
	"sync"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// Handler holds dependencies for all API handlers.
type Handler struct {
	queries  *database.Queries
	db       *sql.DB
	txnStore *database.TransactionStore

	apiTokenStore             *database.ApiTokenStore
	loginFailureLimiter       *ratelimit.Bucket // keyed by client IP, consumed only by /api/auth/login failures
	createTokenLimiter        *ratelimit.Bucket // keyed by user id (as string), 5 hits / rolling hour
	pushTestLimiter           *ratelimit.Bucket // keyed by user id (as string), 5 test-push sends / rolling hour
	passwordChangeFailLimiter *ratelimit.Bucket // keyed by user id (as string), 5 wrong-current-password attempts / 15 min

	// pushSender fans out Web Push notifications. nil => the feature is a
	// no-op: the public vapid route 404s and the subscribe/test handlers
	// report disabled. Set once at startup via SetPushSender when
	// cfg.Push.Enabled. Read-only after startup, like the other handler deps.
	pushSender *push.Sender

	// pushTesterForBudgetAlerts is a TEST-ONLY override for the push
	// dispatcher consulted by evaluateBudgetAlerts before pushSender, so unit
	// tests can capture fan-out without a real VAPID keypair. Production code
	// never sets this; a non-nil value outside a _test.go file is a bug.
	pushTesterForBudgetAlerts pushDispatcher

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

	// summaryCache holds pre-marshalled /api/homepage/summary payloads
	// keyed by the SHA-256 hash of the bearer token. Each token gets its
	// own TTL slot (15 s) so inserting a new transaction from token T2
	// does not prematurely bust T1's cached entry.
	//
	// The cache uses the CALLER'S clock (not limiterClock) so that tests
	// driving NewHandlerWithClock with a fixedClock can advance time and
	// observe TTL expiry deterministically. Production code uses realClock{}
	// via NewHandler, which is equivalent to limiterClock but kept separate
	// to preserve the test-clock contract.
	summaryCache *summaryCache

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
	clock := ratelimit.RealClock()
	appClock := realClock{}
	return &Handler{
		queries:                   queries,
		db:                        db,
		txnStore:                  database.NewTransactionStore(db, queries),
		apiTokenStore:             database.NewApiTokenStore(db, queries),
		loginFailureLimiter:       ratelimit.NewBucket(getRateLimitMax(), rateLimitTickerWindow, clock),
		createTokenLimiter:        ratelimit.NewBucket(5, time.Hour, clock),
		pushTestLimiter:           ratelimit.NewBucket(5, time.Hour, clock),
		passwordChangeFailLimiter: ratelimit.NewBucket(5, 15*time.Minute, clock),
		clock:                     appClock,
		summaryCache:              newSummaryCache(appClock),
	}
}

// NewHandlerWithClock is the test-only constructor for Handlers that need a
// fixed time source. Production code must call NewHandler instead so a
// fixedClock cannot leak into a running server. Kept as a separate function
// rather than a functional option so a misuse in production grep's easily
// (`NewHandlerWithClock` outside a _test.go file is a bug).
func NewHandlerWithClock(queries *database.Queries, db *sql.DB, clock Clock) *Handler {
	limiterClock := ratelimit.RealClock()
	return &Handler{
		queries:                   queries,
		db:                        db,
		txnStore:                  database.NewTransactionStore(db, queries),
		apiTokenStore:             database.NewApiTokenStore(db, queries),
		loginFailureLimiter:       ratelimit.NewBucket(getRateLimitMax(), rateLimitTickerWindow, limiterClock),
		createTokenLimiter:        ratelimit.NewBucket(5, time.Hour, limiterClock),
		pushTestLimiter:           ratelimit.NewBucket(5, time.Hour, limiterClock),
		passwordChangeFailLimiter: ratelimit.NewBucket(5, 15*time.Minute, limiterClock),
		clock:                     clock,
		summaryCache:              newSummaryCache(clock), // use caller's clock so tests can control TTL expiry
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

// SetPushSender installs the Web Push fan-out sender. Called once from
// cmd/spendrop/main.go at startup, only when cfg.Push.Enabled — when push is
// disabled the field stays nil and every push handler treats that as
// "feature off." Mirrors SetIntegrityResult: a single post-construction
// seam so NewHandler's signature stays unchanged and the 50+ test call sites
// compile untouched. Not guarded by a mutex because it is set exactly once
// during single-threaded startup before the HTTP server binds.
func (h *Handler) SetPushSender(s *push.Sender) {
	h.pushSender = s
}
