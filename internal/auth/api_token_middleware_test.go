package auth

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// setupMiddlewareTest returns a file-backed SQLite DB with migrations applied
// plus a per-IP bucket with a 5s window (not 10min so tests expire quickly).
func setupMiddlewareTest(t *testing.T) (q *database.Queries, db *sql.DB, bucket *ratelimit.Bucket, stop func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := database.RunMigrations(db, database.MigrationOptions{
		DBPath:      dbPath,
		SnapshotDir: filepath.Join(dir, "snapshots"),
		BusyTimeout: 5 * time.Second,
	}); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	q = database.New(db)
	bucket = ratelimit.NewBucket(30, 5*time.Second, ratelimit.RealClock())
	stop = func() {
		bucket.Stop()
		_ = db.Close()
	}
	return q, db, bucket, stop
}

// seedUserAndLiveToken creates one user + one live token. Tests needing
// specific expires_at/revoked_at modify the row directly via db.
func seedUserAndLiveToken(t *testing.T, q *database.Queries, username string) (userID int64, plaintext string) {
	t.Helper()
	u, err := q.CreateUser(context.Background(), database.CreateUserParams{
		Username:     username,
		PasswordHash: "$2a$10$fake.bcrypt.hash.for.test",
		DisplayName:  username,
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	pt, hash, prefix, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	_, err = q.CreateAPIToken(context.Background(), database.CreateAPITokenParams{
		UserID:      u.ID,
		Name:        username + "-token",
		TokenHash:   hash,
		TokenPrefix: prefix,
		ExpiresAt:   sql.NullTime{},
	})
	if err != nil {
		t.Fatalf("create api_token: %v", err)
	}
	return u.ID, pt
}

// terminalHandler is the `next` passed to RequireAPIToken. Records the
// attached user via the captured pointer (nil is allowed).
func terminalHandler(captured *database.User) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			u, _ := r.Context().Value(UserContextKey).(database.User)
			*captured = u
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
}

func doBearerRequest(
	t *testing.T,
	mw func(http.Handler) http.Handler,
	next http.Handler,
	bearer, ip string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/homepage/summary", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if ip == "" {
		ip = "1.2.3.4:5678"
	}
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	return rec
}

func assertOpaqueUnauthorized(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"error":"invalid or missing token"}`
	if got != want {
		t.Errorf("body mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestRequireAPIToken_BadInputShape_401(t *testing.T) {
	q, _, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	mw := RequireAPIToken(q, bucket)
	cases := []struct {
		name   string
		header string
	}{
		{"missing header", ""},
		{"wrong prefix", "Token spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG_abc123"}, // Linkding-style footgun
		{"bad regex (too short)", "Bearer spdr_short"},
		{"bad CRC32", "Bearer spdr_abcdefghijklmnopqrstuvwxyz_000000"}, // shape OK, checksum wrong
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/homepage/summary", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			req.RemoteAddr = "1.2.3.4:5678"
			rec := httptest.NewRecorder()
			mw(terminalHandler(nil)).ServeHTTP(rec, req)
			assertOpaqueUnauthorized(t, rec)
		})
	}
}

func TestRequireAPIToken_InvalidTokenFormat_DoesNotHitDatabase(t *testing.T) {
	_, db, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	spy := &spyQueries{}
	mw := RequireAPIToken(spy.wrap(db), bucket)
	// Pipe 100 malformed tokens through; none should hit the DB.
	for i := 0; i < 100; i++ {
		doBearerRequest(t, mw, terminalHandler(nil),
			fmt.Sprintf("garbage-%d", i), "1.2.3.4:5678")
	}
	if n := atomic.LoadInt64(&spy.getByHashCount); n != 0 {
		t.Errorf("malformed tokens should not hit DB; GetAPITokenByHash called %d times", n)
	}
}

func TestRequireAPIToken_UnknownHash_401(t *testing.T) {
	q, _, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	mw := RequireAPIToken(q, bucket)
	// Well-formed token that has never been inserted — passes regex + CRC32
	// but GetAPITokenByHash returns sql.ErrNoRows.
	pt, _, _, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rec := doBearerRequest(t, mw, terminalHandler(nil), pt, "1.2.3.4:5678")
	assertOpaqueUnauthorized(t, rec)
}

func TestRequireAPIToken_InactiveToken_401(t *testing.T) {
	// Both branches exercise GetAPITokenByHash's WHERE clause
	// (revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now)).
	cases := []struct {
		name   string
		mutate func(t *testing.T, q *database.Queries, db *sql.DB, userID int64)
	}{
		{"revoked", func(t *testing.T, q *database.Queries, _ *sql.DB, userID int64) {
			tokens, err := q.ListAPITokensForUser(context.Background(), userID)
			if err != nil || len(tokens) != 1 {
				t.Fatalf("list tokens: err=%v len=%d", err, len(tokens))
			}
			if _, err := q.RevokeAPIToken(context.Background(), database.RevokeAPITokenParams{
				ID: tokens[0].ID, UserID: userID,
			}); err != nil {
				t.Fatalf("revoke: %v", err)
			}
		}},
		{"expired", func(t *testing.T, _ *database.Queries, db *sql.DB, userID int64) {
			if _, err := db.ExecContext(context.Background(),
				`UPDATE api_tokens SET expires_at = datetime('now','-1 hour') WHERE user_id = ?`,
				userID); err != nil {
				t.Fatalf("backdate expires_at: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, db, bucket, stop := setupMiddlewareTest(t)
			defer stop()
			userID, plaintext := seedUserAndLiveToken(t, q, "alice")
			tc.mutate(t, q, db, userID)
			mw := RequireAPIToken(q, bucket)
			rec := doBearerRequest(t, mw, terminalHandler(nil), plaintext, "1.2.3.4:5678")
			assertOpaqueUnauthorized(t, rec)
		})
	}
}

func TestRequireAPIToken_ExpiredToken_TouchIsNotUpdated(t *testing.T) {
	q, db, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	userID, plaintext := seedUserAndLiveToken(t, q, "alice")
	_, err := db.ExecContext(context.Background(),
		`UPDATE api_tokens SET expires_at = datetime('now','-1 hour'), last_used_at = NULL WHERE user_id = ?`,
		userID)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}

	mw := RequireAPIToken(q, bucket)
	doBearerRequest(t, mw, terminalHandler(nil), plaintext, "1.2.3.4:5678")

	// The touch is synchronous inside ServeHTTP — by the time
	// doBearerRequest returns, any UPDATE has either committed or been
	// skipped. No polling needed.
	var lastUsed sql.NullTime
	if err := db.QueryRowContext(context.Background(),
		`SELECT last_used_at FROM api_tokens WHERE user_id = ?`, userID,
	).Scan(&lastUsed); err != nil {
		t.Fatalf("query last_used_at: %v", err)
	}
	if lastUsed.Valid {
		t.Errorf("expired token should not update last_used_at, but got %v", lastUsed.Time)
	}
}

// spyQueries + countingDBTX count api_tokens SELECTs without instrumenting
// production code. Used only by InvalidTokenFormat_DoesNotHitDatabase to
// prove malformed input short-circuits before the DB.
type spyQueries struct {
	getByHashCount int64
}

func (s *spyQueries) wrap(db *sql.DB) *database.Queries {
	return database.New(&countingDBTX{db: db, spy: s})
}

type countingDBTX struct {
	db  *sql.DB
	spy *spyQueries
}

func (c *countingDBTX) ExecContext(ctx context.Context, q string, args ...interface{}) (sql.Result, error) {
	return c.db.ExecContext(ctx, q, args...)
}
func (c *countingDBTX) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return c.db.PrepareContext(ctx, q)
}
func (c *countingDBTX) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	// Match only the GetAPITokenByHash query body, not all api_tokens
	// reads — otherwise a future query (e.g. ListAPITokensForUser firing
	// before the shape check) could silently inflate the count.
	if strings.Contains(q, "token_hash = ?") {
		atomic.AddInt64(&c.spy.getByHashCount, 1)
	}
	return c.db.QueryContext(ctx, q, args...)
}
func (c *countingDBTX) QueryRowContext(ctx context.Context, q string, args ...interface{}) *sql.Row {
	// Match only the GetAPITokenByHash query body, not all api_tokens
	// reads — otherwise a future query (e.g. ListAPITokensForUser firing
	// before the shape check) could silently inflate the count.
	if strings.Contains(q, "token_hash = ?") {
		atomic.AddInt64(&c.spy.getByHashCount, 1)
	}
	return c.db.QueryRowContext(ctx, q, args...)
}

func TestRequireAPIToken_AuthFailureRateLimit_429AfterThreshold(t *testing.T) {
	q, _, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	mw := RequireAPIToken(q, bucket)
	// Fire 30 unknown-hash requests — exactly at the limit, all 401.
	for i := 0; i < 30; i++ {
		pt, _, _, _ := GenerateAPIToken()
		rec := doBearerRequest(t, mw, terminalHandler(nil), pt, "9.9.9.9:1234")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("hit %d: expected 401, got %d", i, rec.Code)
		}
	}
	// 31st request from same IP: bucket is exhausted, middleware short-circuits
	// BEFORE the DB lookup and returns 429 with Retry-After.
	pt, _, _, _ := GenerateAPIToken()
	rec := doBearerRequest(t, mw, terminalHandler(nil), pt, "9.9.9.9:1234")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 31st hit, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("expected Retry-After header on 429")
	}
	if !strings.Contains(rec.Body.String(), `"rate limit"`) {
		t.Errorf("429 body should contain 'rate limit', got: %s", rec.Body.String())
	}
}

func TestRequireAPIToken_AuthFailureRateLimit_MalformedGibberishDoesNotConsume(t *testing.T) {
	q, _, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	mw := RequireAPIToken(q, bucket)
	// 100 malformed-shape attempts from one IP.
	for i := 0; i < 100; i++ {
		doBearerRequest(t, mw, terminalHandler(nil),
			fmt.Sprintf("garbage-%d", i), "7.7.7.7:1234")
	}
	// One valid-shape unknown-hash attempt from the SAME IP — must still be
	// 401 (the bucket was never consumed by the 100 malformed tries), not
	// 429 (would fire only if the bucket was already exhausted).
	pt, _, _, _ := GenerateAPIToken()
	rec := doBearerRequest(t, mw, terminalHandler(nil), pt, "7.7.7.7:1234")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (malformed input should not consume bucket), got %d", rec.Code)
	}
}

func TestRequireAPIToken_ValidToken_AttachesUserToContext(t *testing.T) {
	q, _, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	_, plaintext := seedUserAndLiveToken(t, q, "alice")

	var attached database.User
	mw := RequireAPIToken(q, bucket)
	rec := doBearerRequest(t, mw, terminalHandler(&attached), plaintext, "1.2.3.4:5678")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if attached.Username != "alice" {
		t.Errorf("context user: want alice, got %q", attached.Username)
	}
}

func TestRequireAPIToken_ValidToken_TouchesLastUsedWithin60sWindow(t *testing.T) {
	q, db, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	userID, plaintext := seedUserAndLiveToken(t, q, "alice")
	mw := RequireAPIToken(q, bucket)
	rec := doBearerRequest(t, mw, terminalHandler(nil), plaintext, "198.51.100.7:1234")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// The touch is synchronous inside ServeHTTP — by the time
	// doBearerRequest returns, the UPDATE has either committed or
	// silently failed (best-effort). No polling needed.
	var lastUsedAt sql.NullTime
	var lastUsedIP sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT last_used_at, last_used_ip FROM api_tokens WHERE user_id = ?`, userID,
	).Scan(&lastUsedAt, &lastUsedIP); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !lastUsedAt.Valid {
		t.Fatal("expected last_used_at to be set after a valid request")
	}
	if !lastUsedIP.Valid || lastUsedIP.String != "198.51.100.7" {
		t.Errorf("last_used_ip: want 198.51.100.7, got %v", lastUsedIP)
	}
}

// TestRequireAPIToken_TouchRecordsTheUnmaskedIPv6Address guards the forensic
// column against the rate-limit masking.
//
// The middleware used to derive ONE value and use it for both the rate-limit
// bucket and last_used_ip. Once the bucket key became a network prefix, reusing
// it here would have written "2001:db8:1:2::/64" into the column the token list
// shows the operator — turning the only per-token "used from somewhere
// unexpected" signal into a prefix that every device in the house shares.
func TestRequireAPIToken_TouchRecordsTheUnmaskedIPv6Address(t *testing.T) {
	q, db, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	userID, plaintext := seedUserAndLiveToken(t, q, "alice")
	mw := RequireAPIToken(q, bucket)
	rec := doBearerRequest(t, mw, terminalHandler(nil), plaintext, "[2001:db8:1:2::7]:1234")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var lastUsedIP sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT last_used_ip FROM api_tokens WHERE user_id = ?`, userID,
	).Scan(&lastUsedIP); err != nil {
		t.Fatalf("query last_used_ip: %v", err)
	}
	if !lastUsedIP.Valid || lastUsedIP.String != "2001:db8:1:2::7" {
		t.Errorf("last_used_ip: want the full address 2001:db8:1:2::7, got %v — the "+
			"rate-limit key is being persisted and the forensic value is lost", lastUsedIP)
	}
}

// TestRequireAPIToken_OneIPv6PrefixSharesTheAuthFailBucket pins the middleware
// WIRING, not just the key helper: the bucket must be consumed under the masked
// key. Before the mask, a client rotating through its own /64 got a fresh
// bucket per request and authFailLimiter never engaged at all.
func TestRequireAPIToken_OneIPv6PrefixSharesTheAuthFailBucket(t *testing.T) {
	q, _, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	// setupMiddlewareTest builds a 30-hit bucket. A well-formed but unknown
	// token consumes one hit per request (the shape pre-filter passes, the hash
	// lookup misses), so the 31st request from the same /64 must be refused.
	unknown, _, _, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	mw := RequireAPIToken(q, bucket)

	for i := 0; i < 30; i++ {
		remote := fmt.Sprintf("[2001:db8:1:2::%x]:40000", i+1)
		rec := doBearerRequest(t, mw, terminalHandler(nil), unknown, remote)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d from %s: got %d, want 401", i, remote, rec.Code)
		}
	}

	rec := doBearerRequest(t, mw, terminalHandler(nil), unknown, "[2001:db8:1:2::ff]:40000")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("31st request from the same /64: got %d, want 429 — every address in "+
			"the prefix is minting its own bucket, so the limiter never engages", rec.Code)
	}

	// A different /64 must still be its own bucket, or one attacker locks out
	// unrelated households.
	other := doBearerRequest(t, mw, terminalHandler(nil), unknown, "[2001:db8:9:9::1]:40000")
	if other.Code != http.StatusUnauthorized {
		t.Errorf("an unrelated /64: got %d, want 401 — separate prefixes are sharing a bucket",
			other.Code)
	}
}

func TestRequireAPIToken_ValidToken_SkipsTouchIfRecentlyUpdated(t *testing.T) {
	q, db, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	userID, plaintext := seedUserAndLiveToken(t, q, "alice")
	// Set last_used_at to 30 seconds ago (within the 60s debounce window)
	// and a sentinel IP that would be overwritten IF the middleware fires
	// an UPDATE.
	_, err := db.ExecContext(context.Background(),
		`UPDATE api_tokens SET last_used_at = datetime('now','-30 seconds'),
		     last_used_ip = 'SENTINEL' WHERE user_id = ?`, userID)
	if err != nil {
		t.Fatalf("seed last_used_at: %v", err)
	}

	mw := RequireAPIToken(q, bucket)
	doBearerRequest(t, mw, terminalHandler(nil), plaintext, "10.0.0.42:1234")

	var lastUsedIP sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT last_used_ip FROM api_tokens WHERE user_id = ?`, userID,
	).Scan(&lastUsedIP); err != nil {
		t.Fatalf("query last_used_ip: %v", err)
	}
	// Either the Go-side fast-path skipped the touch entirely, OR the sync
	// UPDATE fired but the SQL WHERE clause rejected it. Either way: sentinel survives.
	if lastUsedIP.String != "SENTINEL" {
		t.Errorf("debounced request should not have overwritten last_used_ip; got %q", lastUsedIP.String)
	}
}

func TestRequireAPIToken_ValidToken_TouchConcurrentWritersOnlyOneWins(t *testing.T) {
	q, db, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	userID, plaintext := seedUserAndLiveToken(t, q, "alice")
	// Clear last_used_at so EVERY concurrent request passes the Go-side
	// fast-path check. The SQL WHERE is the only gate under test.
	_, err := db.ExecContext(context.Background(),
		`UPDATE api_tokens SET last_used_at = NULL WHERE user_id = ?`, userID)
	if err != nil {
		t.Fatalf("clear last_used_at: %v", err)
	}

	mw := RequireAPIToken(q, bucket)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			doBearerRequest(t, mw, terminalHandler(nil), plaintext,
				fmt.Sprintf("10.0.0.%d:5555", i+1))
		}(i)
	}
	wg.Wait()

	// Exactly 1 row has last_used_at set — SQLite's single-writer lock plus
	// the SQL WHERE clause means 19 of 20 racing UPDATEs affect zero rows.
	// No post-wait sleep needed: the touch is synchronous inside ServeHTTP,
	// so wg.Wait() already guarantees every UPDATE has committed or failed.
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND last_used_at IS NOT NULL`,
		userID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected exactly 1 row with last_used_at set, got %d", n)
	}
}

func TestRequireAPIToken_AllAuthFailures_SameErrorBody(t *testing.T) {
	q, db, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	// Seed one user + one token + one revoked + one expired so we have every
	// DB state represented.
	_, liveTok := seedUserAndLiveToken(t, q, "alice")
	userBob, revokedTok := seedUserAndLiveToken(t, q, "bob")
	toks, _ := q.ListAPITokensForUser(context.Background(), userBob)
	_, _ = q.RevokeAPIToken(context.Background(), database.RevokeAPITokenParams{ID: toks[0].ID, UserID: userBob})
	userCarol, expiredTok := seedUserAndLiveToken(t, q, "carol")
	_, _ = db.ExecContext(context.Background(),
		`UPDATE api_tokens SET expires_at = datetime('now','-1 hour') WHERE user_id = ?`, userCarol)

	unknownPt, _, _, _ := GenerateAPIToken()

	cases := []struct {
		name   string
		bearer string
		preset func(r *http.Request)
	}{
		{"no header", "", func(r *http.Request) { r.Header.Del("Authorization") }},
		{"wrong prefix", "", func(r *http.Request) { r.Header.Set("Authorization", "Token "+liveTok) }},
		{"bad regex", "spdr_short", nil},
		{"bad crc", "spdr_abcdefghijklmnopqrstuvwxyz_000000", nil},
		{"unknown hash", unknownPt, nil},
		{"revoked", revokedTok, nil},
		{"expired", expiredTok, nil},
	}
	mw := RequireAPIToken(q, bucket)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/homepage/summary", nil)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.preset != nil {
				tc.preset(req)
			}
			req.RemoteAddr = "1.2.3.4:5678"
			rec := httptest.NewRecorder()
			mw(terminalHandler(nil)).ServeHTTP(rec, req)
			assertOpaqueUnauthorized(t, rec)
		})
	}
}

func TestRequireAPIToken_BearerRoute_ResponseHasNoSetCookie(t *testing.T) {
	q, _, bucket, stop := setupMiddlewareTest(t)
	defer stop()

	_, plaintext := seedUserAndLiveToken(t, q, "alice")
	mw := RequireAPIToken(q, bucket)

	// Include BOTH a valid bearer and a fake session cookie on the same
	// request — the middleware must not write Set-Cookie on the response,
	// and the terminal handler (which stands in for a future bearer-only
	// handler) must not either.
	req := httptest.NewRequest(http.MethodGet, "/api/homepage/summary", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.AddCookie(&http.Cookie{Name: "session", Value: strings.Repeat("a", 64)})
	req.RemoteAddr = "1.2.3.4:5678"
	rec := httptest.NewRecorder()
	mw(terminalHandler(nil)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if setCookies := rec.Header()["Set-Cookie"]; len(setCookies) != 0 {
		t.Errorf("bearer route must not write Set-Cookie; got %v", setCookies)
	}
}
