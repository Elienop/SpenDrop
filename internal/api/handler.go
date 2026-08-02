package api

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/elienop/spendrop/internal/backup"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
	"github.com/elienop/spendrop/internal/ratelimit"
	"github.com/elienop/spendrop/internal/sse"
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

	// Background push delivery state (see deliverInBackground), all guarded by
	// pushMu. Every field is usable at its zero value, so a Handler built by any
	// constructor — or as a bare literal in a test — starts with delivery
	// working and nothing in flight.
	//
	// pushInFlight counts fan-outs admitted and not yet finished. It is the ONE
	// source of truth: the admission cap reads it, the drain waits on it, and a
	// worker decrements it as its very last act, so the number a timed-out drain
	// reports is exact rather than an approximation that lags the real
	// population.
	//
	// pushDraining is set while a drain is parked, and blocks new admissions.
	// That is what makes the counter safe: it can never go 0 -> positive while a
	// waiter is waiting on it. A successful drain clears the flag; a timed-out
	// one deliberately does not, because its waiter is still parked and the only
	// caller that times out is a process already on its way out.
	//
	// pushIdle is non-nil only while a drain is parked, and is closed when
	// pushInFlight reaches 0. A channel rather than a sync.Cond because the drain
	// has to select against a context.
	pushMu       sync.Mutex
	pushInFlight int
	pushDraining bool
	pushIdle     chan struct{}

	// broker fans out coarse SSE "invalidate" hints to every connected
	// household device after a committed mutation. nil => the feature is a
	// no-op (publishInvalidate returns immediately). Set once at startup via
	// SetEventBroker after NewRouterWithHandler; read-only thereafter, like
	// pushSender. No mutex: set once during single-threaded startup before the
	// HTTP server binds, and the hub itself is internally goroutine-safe.
	broker *sse.Hub

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

	// backupStatus reads the scheduled-backup subsystem's health snapshot
	// for /healthz/data. nil => this binary never installed a source, and
	// the health payload omits the `backups` block entirely rather than
	// asserting anything about it.
	//
	// A func rather than a *backup.Scheduler so the health tests can drive
	// every failure state without a filesystem, and so the api package
	// depends on backup only for its Status type. Set once at startup via
	// SetBackupStatusSource, like SetPushSender; the function itself is
	// backup.Scheduler.Status, which takes its own mutex, so calling it
	// concurrently with the scheduler goroutine is safe.
	backupStatus func() backup.Status
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

// WaitForPushDelivery blocks until every background push fan-out this Handler
// started has finished, or until ctx is done — whichever comes first. It returns
// nil only when the drain completed.
//
// While it is parked, NEW fan-outs are refused rather than queued (see
// admitDelivery). During shutdown that is the honest outcome — a notification
// accepted at that point would be abandoned seconds later anyway — and it is
// also what makes the counter safe to wait on: nothing can push it back above
// zero underneath a waiter.
//
// Called from cmd/spendrop/main.go during graceful shutdown, AFTER the HTTP
// server has stopped (so no new fan-out can be queued) and BEFORE the database
// handle closes (a fan-out that sees a 404/410 prunes the dead subscription row,
// and that write needs the handle). A notification for a save the household just
// made should survive a container restart, which on this deployment happens on
// every app update. That ordering is pinned by
// TestShutdownDrainsPushDeliveryAfterTheServerStops in cmd/spendrop.
//
// On timeout the caller is told exactly how many deliveries were abandoned
// rather than being left to guess.
func (h *Handler) WaitForPushDelivery(ctx context.Context) error {
	h.pushMu.Lock()
	if h.pushInFlight == 0 {
		h.pushMu.Unlock()
		return nil
	}
	h.pushDraining = true
	if h.pushIdle == nil {
		h.pushIdle = make(chan struct{})
	}
	idle := h.pushIdle
	h.pushMu.Unlock()

	select {
	case <-idle:
		h.pushMu.Lock()
		h.pushDraining = false
		h.pushMu.Unlock()
		return nil
	case <-ctx.Done():
		h.pushMu.Lock()
		defer h.pushMu.Unlock()
		if h.pushInFlight == 0 {
			// The last worker finished in the same instant the deadline expired;
			// select picks at random between two ready cases, so report the
			// drain that actually happened rather than "0 still delivering".
			h.pushDraining = false
			return nil
		}
		return fmt.Errorf("%d push fan-out(s) still delivering: %w", h.pushInFlight, ctx.Err())
	}
}

// SetEventBroker installs the SSE fan-out hub. Called once from
// cmd/spendrop/main.go at startup, after NewRouterWithHandler. Mirrors
// SetPushSender: a single post-construction seam so NewHandler's signature
// stays unchanged and the 50+ test call sites compile untouched. Not guarded
// by a mutex because it is set exactly once during single-threaded startup
// before the HTTP server binds.
func (h *Handler) SetEventBroker(b *sse.Hub) {
	h.broker = b
}

// SetBackupStatusSource installs the reader for the scheduled-backup
// subsystem's health snapshot. Called once from cmd/spendrop/main.go with
// scheduler.Status, UNCONDITIONALLY — including when backups are disabled, so
// that a scrape can tell "the operator turned backups off" apart from "this
// binary does not report on backups at all". Mirrors SetPushSender: a single
// post-construction seam, no mutex, set during single-threaded startup before
// the HTTP server binds.
func (h *Handler) SetBackupStatusSource(fn func() backup.Status) {
	h.backupStatus = fn
}

// publishInvalidate fans out a coarse SSE invalidation hint for the given
// resource names to every connected household device. POST-COMMIT and
// BEST-EFFORT, mirroring the notifyTxn* / evaluateBudgetAlerts contract: it
// returns nothing, never errors, never rolls back, and never affects the HTTP
// response. A nil broker (push/SSE-disabled handlers, every unit test built
// via NewHandler) makes it a no-op. The underlying Hub.Publish is itself
// non-blocking, so this cannot stall a request handler even under a backlog.
func (h *Handler) publishInvalidate(resources ...string) {
	if h.broker == nil {
		return
	}
	h.broker.Publish(resources...)
}
