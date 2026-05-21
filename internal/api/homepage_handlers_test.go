package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// --------------------------------------------------------------------------
// Test helpers
// --------------------------------------------------------------------------

// newHomepageTestRouter builds a minimal chi router that mounts ONLY the
// bearer-auth homepage subroute. Using a dedicated router (not the full
// NewRouter output) keeps each test hermetic and avoids interfering with
// session-auth middleware from the main /api tree.
func newHomepageTestRouter(t *testing.T, h *Handler, queries *database.Queries) chi.Router {
	t.Helper()
	limiter := ratelimit.NewBucket(30, 10*time.Minute, h.clock)
	r := chi.NewRouter()
	r.Route("/api/homepage", func(r chi.Router) {
		r.Use(auth.RequireAPIToken(queries, limiter))
		r.Get("/summary", h.handleHomepageSummary)
	})
	return r
}

// seedUserWithLiveToken inserts a user and a live API token, returning the
// user and the plaintext bearer string. Does not require a session — bearer
// tests pass the token directly in the Authorization header.
func seedUserWithLiveToken(t *testing.T, q *database.Queries, username string) (database.User, string) {
	t.Helper()
	auth.SetBcryptCostForTesting()
	hash, err := auth.HashPassword("irrelevant-" + username)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := q.CreateUser(context.Background(), database.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		DisplayName:  username,
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}

	plaintext, tokenHash, prefix, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate api token: %v", err)
	}
	_, err = q.CreateAPIToken(context.Background(), database.CreateAPITokenParams{
		UserID:      user.ID,
		TokenHash:   tokenHash,
		TokenPrefix: prefix,
		Name:        "test-token-" + username,
	})
	if err != nil {
		t.Fatalf("create api token for %s: %v", username, err)
	}
	return user, plaintext
}

// seedExpenseCategory creates a category of type "expense" and returns its ID.
// The name is suffixed with the test name to avoid UNIQUE constraint collisions
// with the migration-seeded categories (e.g. "Food") in the test DB.
func seedExpenseCategory(t *testing.T, q *database.Queries, name string) int64 {
	t.Helper()
	uniqueName := name + "-" + t.Name()
	cat, err := q.CreateCategory(context.Background(), database.CreateCategoryParams{
		Name:      uniqueName,
		Type:      CategoryTypeExpense,
		SortOrder: 0,
	})
	if err != nil {
		t.Fatalf("seed expense category %q: %v", uniqueName, err)
	}
	return cat.ID
}

// seedIncomeCategory creates a category of type "income" and returns its ID.
// The name is suffixed with the test name to avoid UNIQUE constraint collisions
// with the migration-seeded categories in the test DB.
func seedIncomeCategory(t *testing.T, q *database.Queries, name string) int64 {
	t.Helper()
	uniqueName := name + "-" + t.Name()
	cat, err := q.CreateCategory(context.Background(), database.CreateCategoryParams{
		Name:      uniqueName,
		Type:      CategoryTypeIncome,
		SortOrder: 0,
	})
	if err != nil {
		t.Fatalf("seed income category %q: %v", uniqueName, err)
	}
	return cat.ID
}

// seedExpenseRow creates an expense transaction for userID at the given date
// with the given amount in cents. Phase 3.1b: the legacy REAL amount column
// was dropped in migration 010, so amount_cents is the only money column.
func seedExpenseRow(t *testing.T, q *database.Queries, userID, categoryID int64, date string, cents int64) database.Transaction {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %q: %v", date, err)
	}
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: cents,
		Description: "test-expense",
		CategoryID:  categoryID,
	})
	if err != nil {
		t.Fatalf("seed expense row: %v", err)
	}
	return txn
}

// seedIncomeRow creates an income transaction for userID at the given date
// with the given amount in cents.
func seedIncomeRow(t *testing.T, q *database.Queries, userID, categoryID int64, date string, cents int64) database.Transaction {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %q: %v", date, err)
	}
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: cents,
		Description: "test-income",
		CategoryID:  categoryID,
	})
	if err != nil {
		t.Fatalf("seed income row: %v", err)
	}
	return txn
}

// homepageRequest fires a bearer-authenticated GET /api/homepage/summary
// through the given router. bearer should be the plaintext token string.
func homepageRequest(t *testing.T, router chi.Router, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/homepage/summary", nil)
	req.RemoteAddr = "203.0.113.10:5678"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// decodeSummary decodes the response body into a homepageSummaryResponse.
func decodeSummary(t *testing.T, rec *httptest.ResponseRecorder) homepageSummaryResponse {
	t.Helper()
	var resp homepageSummaryResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode summary response: %v (body: %s)", err, rec.Body.String())
	}
	return resp
}

// --------------------------------------------------------------------------
// Tests (13 total — exact names required by spec)
// --------------------------------------------------------------------------

// TestSummary_RequiresBearer_401_WithSessionCookie verifies that a session
// cookie alone is rejected by the bearer-only middleware.
func TestSummary_RequiresBearer_401_WithSessionCookie(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	router := newHomepageTestRouter(t, h, q)

	// Create a user and session, but send the session cookie instead of a
	// bearer token. RequireAPIToken must reject it with 401.
	hash, _ := auth.HashPassword("pw")
	user, _ := q.CreateUser(context.Background(), database.CreateUserParams{
		Username: "alice", PasswordHash: hash, DisplayName: "alice", Role: "member",
	})
	sessionToken, _ := auth.GenerateSessionToken()
	_ = q.CreateSession(context.Background(), database.CreateSessionParams{
		Token:     sessionToken,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/homepage/summary", nil)
	req.RemoteAddr = "203.0.113.10:5678"
	req.AddCookie(&http.Cookie{Name: "session", Value: sessionToken})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d: %s", rec.Code, rec.Body.String())
	}
	// Middleware must not set a session cookie in the response.
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			t.Error("response should not set a session cookie on bearer-only route")
		}
	}
}

// TestSummary_AggregatesMonthlySpendCorrectly verifies that expenses on three
// dates in April 2026 are summed correctly and income is excluded.
func TestSummary_AggregatesMonthlySpendCorrectly(t *testing.T) {
	q, db := setupTestDB(t)
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	router := newHomepageTestRouter(t, h, q)

	user, bearer := seedUserWithLiveToken(t, q, "alice")
	catExp := seedExpenseCategory(t, q, "Food")
	catInc := seedIncomeCategory(t, q, "Salary")

	// 3 expenses: 100¢ + 250¢ + 300.25$ = should NOT happen — use cents.
	// Amounts: 200¢ + 20000¢ + 44925¢ = 65125¢ = $651.25
	seedExpenseRow(t, q, user.ID, catExp, "2026-04-01", 200)
	seedExpenseRow(t, q, user.ID, catExp, "2026-04-10", 20000)
	seedExpenseRow(t, q, user.ID, catExp, "2026-04-15", 44925)

	// Income sentinel: must NOT leak into MonthSpent
	seedIncomeRow(t, q, user.ID, catInc, "2026-04-01", 999999)

	rec := homepageRequest(t, router, bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeSummary(t, rec)

	if resp.MonthSpent != 651.25 {
		t.Errorf("MonthSpent: want 651.25, got %v", resp.MonthSpent)
	}
	if resp.AsOf != "2026-04-15T12:00:00Z" {
		t.Errorf("AsOf: want 2026-04-15T12:00:00Z, got %q", resp.AsOf)
	}
}

// TestSummary_HidesTombstoned is the mandatory CLAUDE.md soft-delete test:
// one live row (500¢) + one tombstoned row (999¢ sentinel) → MonthSpent==5.00.
func TestSummary_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	router := newHomepageTestRouter(t, h, q)

	user, bearer := seedUserWithLiveToken(t, q, "alice")
	catExp := seedExpenseCategory(t, q, "Food")

	// Live row: 500¢ = $5.00
	seedExpenseRow(t, q, user.ID, catExp, "2026-04-10", 500)
	// Tombstoned row: 999¢ sentinel that must NOT appear in the aggregate.
	txnTombstone := seedExpenseRow(t, q, user.ID, catExp, "2026-04-10", 999)
	if err := q.SoftDeleteTransaction(context.Background(), txnTombstone.ID); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	rec := homepageRequest(t, router, bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeSummary(t, rec)
	if resp.MonthSpent != 5.00 {
		t.Errorf("MonthSpent: want 5.00 (tombstoned row must be hidden), got %v", resp.MonthSpent)
	}
}

// TestSummary_ScopedToTokenOwnerUser verifies that alice's token only sees
// alice's transactions, not bob's.
func TestSummary_ScopedToTokenOwnerUser(t *testing.T) {
	q, db := setupTestDB(t)
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	router := newHomepageTestRouter(t, h, q)

	alice, aliceBearer := seedUserWithLiveToken(t, q, "alice")
	bob, _ := seedUserWithLiveToken(t, q, "bob")
	catExp := seedExpenseCategory(t, q, "Food")

	seedExpenseRow(t, q, alice.ID, catExp, "2026-04-10", 100) // $1.00
	seedExpenseRow(t, q, bob.ID, catExp, "2026-04-10", 9999)  // $99.99 — must not leak to alice

	rec := homepageRequest(t, router, aliceBearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeSummary(t, rec)
	if resp.MonthSpent != 1.00 {
		t.Errorf("MonthSpent: want 1.00 (alice only), got %v", resp.MonthSpent)
	}
}

// TestSummary_CurrencyFromSettings verifies that the currency field reflects
// the base_currency app setting and updates after a setting change (cache-bust
// by using a new token).
func TestSummary_CurrencyFromSettings(t *testing.T) {
	q, db := setupTestDB(t)
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	router := newHomepageTestRouter(t, h, q)

	_, bearer := seedUserWithLiveToken(t, q, "alice")

	// Default: USD
	rec := homepageRequest(t, router, bearer)
	resp := decodeSummary(t, rec)
	if resp.Currency != "USD" {
		t.Errorf("default currency: want USD, got %q", resp.Currency)
	}

	// After UpsertSetting with EUR, use a fresh token to bust the cache.
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   SettingBaseCurrency,
		Value: "EUR",
	}); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}

	// New user + token to avoid hitting the cached entry for the first bearer.
	_, bearer2 := seedUserWithLiveToken(t, q, "alice2")
	rec2 := homepageRequest(t, router, bearer2)
	resp2 := decodeSummary(t, rec2)
	if resp2.Currency != "EUR" {
		t.Errorf("after upsert: want EUR, got %q", resp2.Currency)
	}
}

// TestSummary_MonthRemainingIsSignedDifference verifies the signed arithmetic:
// (a) budget > spent → positive remaining; (b) budget < spent → negative.
func TestSummary_MonthRemainingIsSignedDifference(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)

	t.Run("under_budget", func(t *testing.T) {
		// Fresh DB + handler per sub-test to avoid cache / data pollution.
		q, db := setupTestDB(t)
		h := NewHandlerWithClock(q, db, fixedClock{t: now})
		router := newHomepageTestRouter(t, h, q)
		catExp := seedExpenseCategory(t, q, "Food")

		user, bearer := seedUserWithLiveToken(t, q, "alice")
		seedExpenseRow(t, q, user.ID, catExp, "2026-04-10", 1500) // $15.00

		// Budget: $20.00 → remaining = +$5.00
		if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
			Key:   SettingDefaultBudget,
			Value: "20.00",
		}); err != nil {
			t.Fatalf("upsert budget setting: %v", err)
		}
		rec := homepageRequest(t, router, bearer)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
		}
		resp := decodeSummary(t, rec)
		if resp.MonthRemaining != 5.00 {
			t.Errorf("under budget: want +5.00, got %v", resp.MonthRemaining)
		}
	})

	t.Run("over_budget", func(t *testing.T) {
		q, db := setupTestDB(t)
		h := NewHandlerWithClock(q, db, fixedClock{t: now})
		router := newHomepageTestRouter(t, h, q)
		catExp := seedExpenseCategory(t, q, "Food")

		user, bearer := seedUserWithLiveToken(t, q, "alice")
		seedExpenseRow(t, q, user.ID, catExp, "2026-04-10", 1500) // $15.00

		// Budget: $10.00 → remaining = -$5.00
		if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
			Key:   SettingDefaultBudget,
			Value: "10.00",
		}); err != nil {
			t.Fatalf("upsert budget setting: %v", err)
		}
		rec := homepageRequest(t, router, bearer)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
		}
		resp := decodeSummary(t, rec)
		if resp.MonthRemaining != -5.00 {
			t.Errorf("over budget: want -5.00, got %v", resp.MonthRemaining)
		}
	})
}

// TestSummary_TxnCountIsMonthToDateNotLifetime seeds 5 March rows and 3 April
// rows, then asserts TxnCount==3 with a clock at 2026-04-15.
func TestSummary_TxnCountIsMonthToDateNotLifetime(t *testing.T) {
	q, db := setupTestDB(t)
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	router := newHomepageTestRouter(t, h, q)

	user, bearer := seedUserWithLiveToken(t, q, "alice")
	catExp := seedExpenseCategory(t, q, "Food")

	// 5 March rows (not current month)
	for i := 0; i < 5; i++ {
		seedExpenseRow(t, q, user.ID, catExp, "2026-03-10", 100)
	}
	// 3 April rows (current month)
	for i := 0; i < 3; i++ {
		seedExpenseRow(t, q, user.ID, catExp, "2026-04-05", 100)
	}

	rec := homepageRequest(t, router, bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeSummary(t, rec)
	if resp.TxnCount != 3 {
		t.Errorf("TxnCount: want 3 (April only), got %d", resp.TxnCount)
	}
}

// TestSummary_MonthBoundaryReflectsHandlerClock verifies that the aggregation
// window switches exactly at month boundary. Clock at 2026-03-31T23:59Z sees
// a March row; clock at 2026-04-01T00:01Z (fresh token → cache-bust) sees 0.
func TestSummary_MonthBoundaryReflectsHandlerClock(t *testing.T) {
	// --- March: clock sees the March row ---
	q1, db1 := setupTestDB(t)
	march := time.Date(2026, 3, 31, 23, 59, 0, 0, time.UTC)
	h1 := NewHandlerWithClock(q1, db1, fixedClock{t: march})
	router1 := newHomepageTestRouter(t, h1, q1)

	user1, bearer1 := seedUserWithLiveToken(t, q1, "alice")
	catExp1 := seedExpenseCategory(t, q1, "Food")
	seedExpenseRow(t, q1, user1.ID, catExp1, "2026-03-31", 500) // $5.00

	rec1 := homepageRequest(t, router1, bearer1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("march: want 200, got %d", rec1.Code)
	}
	resp1 := decodeSummary(t, rec1)
	if resp1.MonthSpent != 5.00 {
		t.Errorf("march clock: want 5.00, got %v", resp1.MonthSpent)
	}

	// --- April: new handler + fresh token (cache-bust) sees 0 ---
	q2, db2 := setupTestDB(t)
	april := time.Date(2026, 4, 1, 0, 1, 0, 0, time.UTC)
	h2 := NewHandlerWithClock(q2, db2, fixedClock{t: april})
	router2 := newHomepageTestRouter(t, h2, q2)

	user2, bearer2 := seedUserWithLiveToken(t, q2, "alice")
	catExp2 := seedExpenseCategory(t, q2, "Food")
	// Seed the March row for the same user, but clock is now in April.
	seedExpenseRow(t, q2, user2.ID, catExp2, "2026-03-31", 500)

	rec2 := homepageRequest(t, router2, bearer2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("april: want 200, got %d", rec2.Code)
	}
	resp2 := decodeSummary(t, rec2)
	if resp2.MonthSpent != 0.00 {
		t.Errorf("april clock: want 0.00 (March row not in April window), got %v", resp2.MonthSpent)
	}
}

// TestSummary_CachedWithin15SecondsPerToken verifies the 15s TTL cache:
// two calls within the TTL window return byte-identical payloads; after
// advancing the clock by 16s a third call returns a fresh (different) payload.
func TestSummary_CachedWithin15SecondsPerToken(t *testing.T) {
	q, db := setupTestDB(t)
	// Use a mutable clock so we can advance time.
	start := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	mc := &mutableClock{t: start}
	h := NewHandlerWithClock(q, db, mc)
	router := newHomepageTestRouter(t, h, q)

	user, bearer := seedUserWithLiveToken(t, q, "alice")
	catExp := seedExpenseCategory(t, q, "Food")
	seedExpenseRow(t, q, user.ID, catExp, "2026-04-10", 1000) // $10.00

	// First call: cache miss, populates entry.
	rec1 := homepageRequest(t, router, bearer)
	if rec1.Code != http.StatusOK {
		t.Fatalf("call 1: want 200, got %d", rec1.Code)
	}
	body1 := rec1.Body.Bytes()

	// Second call within TTL: cache hit, byte-identical.
	// Advance by 5s (well within 15s TTL).
	mc.advance(5 * time.Second)
	rec2 := homepageRequest(t, router, bearer)
	if rec2.Code != http.StatusOK {
		t.Fatalf("call 2: want 200, got %d", rec2.Code)
	}
	body2 := rec2.Body.Bytes()

	if !bytes.Equal(body1, body2) {
		t.Errorf("call 2 (within TTL): expected byte-identical cached response\n  call1=%s\n  call2=%s", body1, body2)
	}

	// Third call after TTL expiry: new DB read → AsOf differs.
	mc.advance(16 * time.Second) // now +21s from start, TTL was 15s from start
	// Seed another row so the payload actually changes.
	seedExpenseRow(t, q, user.ID, catExp, "2026-04-15", 500)
	rec3 := homepageRequest(t, router, bearer)
	if rec3.Code != http.StatusOK {
		t.Fatalf("call 3: want 200, got %d", rec3.Code)
	}
	body3 := rec3.Body.Bytes()
	if bytes.Equal(body1, body3) {
		t.Error("call 3 (after TTL): expected fresh response, got same bytes as call 1")
	}
}

// mutableClock is a test-only Clock whose time can be advanced atomically.
// Used only in this file for cache TTL tests. Defined here rather than in
// helpers_test.go to keep the homepage tests self-contained.
type mutableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// TestSummary_CacheKeyIsTokenHash_NotUserID verifies that two tokens owned by
// the same user have independent cache slots. Token T2 sees a fresh total even
// though T1's slot is still warm; T1 continues to serve its cached value.
func TestSummary_CacheKeyIsTokenHash_NotUserID(t *testing.T) {
	q, db := setupTestDB(t)
	start := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	mc := &mutableClock{t: start}
	h := NewHandlerWithClock(q, db, mc)
	router := newHomepageTestRouter(t, h, q)

	// Create alice with two tokens (T1 and T2).
	alice, bearerT1 := seedUserWithLiveToken(t, q, "alice")
	// Create T2 directly without seedUserWithLiveToken (user already exists).
	plaintextT2, hashT2, prefixT2, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate T2: %v", err)
	}
	_, err = q.CreateAPIToken(context.Background(), database.CreateAPITokenParams{
		UserID:      alice.ID,
		TokenHash:   hashT2,
		TokenPrefix: prefixT2,
		Name:        "T2",
	})
	if err != nil {
		t.Fatalf("create T2: %v", err)
	}
	bearerT2 := plaintextT2

	catExp := seedExpenseCategory(t, q, "Food")
	seedExpenseRow(t, q, alice.ID, catExp, "2026-04-10", 1000) // initial: $10.00

	// Call T1 → populate T1's cache slot.
	rec1 := homepageRequest(t, router, bearerT1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("T1 call 1: %d", rec1.Code)
	}
	resp1 := decodeSummary(t, rec1)

	// Insert a new transaction AFTER T1 cached.
	seedExpenseRow(t, q, alice.ID, catExp, "2026-04-10", 500) // now $15.00 total

	// T2 call within T1's TTL: T2 has no cache entry yet → fresh DB read.
	mc.advance(5 * time.Second)
	rec2 := homepageRequest(t, router, bearerT2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("T2 call 1: %d", rec2.Code)
	}
	resp2 := decodeSummary(t, rec2)
	if resp2.MonthSpent != 15.00 {
		t.Errorf("T2 (fresh slot): want 15.00, got %v", resp2.MonthSpent)
	}

	// T1 again within its TTL: must still return the stale cached value.
	rec3 := homepageRequest(t, router, bearerT1)
	if rec3.Code != http.StatusOK {
		t.Fatalf("T1 call 2: %d", rec3.Code)
	}
	resp3 := decodeSummary(t, rec3)
	if resp3.MonthSpent != resp1.MonthSpent {
		t.Errorf("T1 second call (within TTL): want stale %v, got %v", resp1.MonthSpent, resp3.MonthSpent)
	}
}

// TestSummary_AsOfReflectsOriginalRunTimeNotCacheHit verifies that the as_of
// field does not update on a cache hit — it always reflects the instant at
// which the data was originally computed.
func TestSummary_AsOfReflectsOriginalRunTimeNotCacheHit(t *testing.T) {
	q, db := setupTestDB(t)
	t0 := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	mc := &mutableClock{t: t0}
	h := NewHandlerWithClock(q, db, mc)
	router := newHomepageTestRouter(t, h, q)

	_, bearer := seedUserWithLiveToken(t, q, "alice")

	// First call at T0.
	rec1 := homepageRequest(t, router, bearer)
	if rec1.Code != http.StatusOK {
		t.Fatalf("call 1: %d", rec1.Code)
	}
	resp1 := decodeSummary(t, rec1)
	if resp1.AsOf != "2026-04-15T12:00:00Z" {
		t.Errorf("call 1 AsOf: want 2026-04-15T12:00:00Z, got %q", resp1.AsOf)
	}

	// Advance clock by 5s (within TTL) and call again.
	mc.advance(5 * time.Second)
	rec2 := homepageRequest(t, router, bearer)
	if rec2.Code != http.StatusOK {
		t.Fatalf("call 2: %d", rec2.Code)
	}
	resp2 := decodeSummary(t, rec2)

	// as_of must still reflect T0, not T0+5s.
	if resp2.AsOf != resp1.AsOf {
		t.Errorf("call 2 (cache hit): AsOf should be unchanged; want %q, got %q", resp1.AsOf, resp2.AsOf)
	}
}

// TestSummary_RevokedTokenReturns401_DoesNotServeStaleCache verifies that
// revoking a token causes the middleware to reject it with 401 even if the
// cache would otherwise serve a stale hit.
func TestSummary_RevokedTokenReturns401_DoesNotServeStaleCache(t *testing.T) {
	q, db := setupTestDB(t)
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	router := newHomepageTestRouter(t, h, q)

	user, bearer := seedUserWithLiveToken(t, q, "alice")

	// Populate the cache with a successful call.
	rec1 := homepageRequest(t, router, bearer)
	if rec1.Code != http.StatusOK {
		t.Fatalf("pre-revoke call: want 200, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Revoke the token via the store. The actor context is minimal for a test.
	tok, err := q.GetAPITokenByHash(context.Background(), auth.HashAPIToken(bearer))
	if err != nil {
		t.Fatalf("get token by hash: %v", err)
	}
	actor := database.ActorContext{UserID: user.ID}
	if err := h.apiTokenStore.Revoke(context.Background(), actor, tok.ID); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	// Call with the revoked token; middleware should return 401, NOT a cached 200.
	rec2 := homepageRequest(t, router, bearer)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("post-revoke: want 401, got %d: %s", rec2.Code, rec2.Body.String())
	}
	// No session cookie should be set on a bearer-only route.
	for _, c := range rec2.Result().Cookies() {
		if c.Name == "session" {
			t.Error("response must not set a session cookie on bearer-only route")
		}
	}
}

// TestSummary_OverBudgetCategoriesIsZeroUntilFeatureLands asserts that the
// over_budget_categories field is always 0 while per-category budgets are not
// yet implemented.
func TestSummary_OverBudgetCategoriesIsZeroUntilFeatureLands(t *testing.T) {
	q, db := setupTestDB(t)
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	router := newHomepageTestRouter(t, h, q)

	user, bearer := seedUserWithLiveToken(t, q, "alice")
	catExp1 := seedExpenseCategory(t, q, "Food")
	catExp2 := seedExpenseCategory(t, q, "Transport")
	catExp3 := seedExpenseCategory(t, q, "Entertainment")

	// Multiple categories with substantial spend.
	seedExpenseRow(t, q, user.ID, catExp1, "2026-04-10", 50000)
	seedExpenseRow(t, q, user.ID, catExp2, "2026-04-10", 30000)
	seedExpenseRow(t, q, user.ID, catExp3, "2026-04-10", 20000)

	// Set a budget so there's something to compare against.
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   SettingDefaultBudget,
		Value: "10.00", // far below actual spend
	}); err != nil {
		t.Fatalf("upsert budget: %v", err)
	}

	rec := homepageRequest(t, router, bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Re-read body since decodeSummary consumes it.
	bodyBytes := rec.Body.Bytes()
	var resp homepageSummaryResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OverBudgetCategories != 0 {
		t.Errorf("OverBudgetCategories: want 0 (stub), got %d", resp.OverBudgetCategories)
	}
	// Sanity: the field must be present in the response JSON.
	if !strings.Contains(string(bodyBytes), `"over_budget_categories"`) {
		t.Error("response JSON missing over_budget_categories field")
	}
}
