package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	auth.SetBcryptCostForTesting()
}

// setupTestDB creates a file-backed SQLite database with migrations applied.
// Phase 4.1 forced the shift from `:memory:` to on-disk: RunMigrations now
// invokes SnapshotForMigration which opens its own read-only connection via
// a dbPath parameter, and for `:memory:` each connection sees an
// independent (empty) database. A file-backed temp DB inside t.TempDir()
// gives every connection the same view and is torn down automatically.
func setupTestDB(t *testing.T) (*database.Queries, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.RunMigrations(db, database.MigrationOptions{
		DBPath:      dbPath,
		SnapshotDir: filepath.Join(dir, "snapshots"),
		BusyTimeout: 5 * time.Second,
	}); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return database.New(db), db
}

// setupHandler creates a Handler backed by an in-memory test database.
func setupHandler(t *testing.T) *Handler {
	t.Helper()
	q, db := setupTestDB(t)
	return NewHandler(q, db)
}

// decodeResponse decodes a JSON response body into the given target.
func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(target); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

// --- writeJSON / writeError / decodeJSON helpers ---

func TestWriteJSON_SetsContentTypeAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]string{"hello": "world"})

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["hello"] != "world" {
		t.Errorf("expected hello=world, got %q", body["hello"])
	}
}

func TestWriteError_ReturnsErrorJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "bad input")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "bad input" {
		t.Errorf("expected error='bad input', got %q", body["error"])
	}
}

func TestDecodeJSON_ValidBody(t *testing.T) {
	body := strings.NewReader(`{"username":"alice","password":"secret123"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", "application/json")

	var data struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(httptest.NewRecorder(), req, &data); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if data.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", data.Username)
	}
	if data.Password != "secret123" {
		t.Errorf("expected password 'secret123', got %q", data.Password)
	}
}

func TestDecodeJSON_InvalidBody(t *testing.T) {
	body := strings.NewReader(`{invalid json`)
	req := httptest.NewRequest(http.MethodPost, "/", body)

	var data struct{ Name string }
	if err := decodeJSON(httptest.NewRecorder(), req, &data); err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// --- handleRegister ---

func TestHandleRegister_MissingUsername_Returns400(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	rec := httptest.NewRecorder()

	h.handleRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if resp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleRegister_MissingPassword_Returns400(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"username":"alice"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	rec := httptest.NewRecorder()

	h.handleRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRegister_ShortPassword_Returns400(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"username":"alice","password":"short"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	rec := httptest.NewRecorder()

	h.handleRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if !strings.Contains(resp["error"], "8") {
		t.Errorf("expected error mentioning 8 chars, got %q", resp["error"])
	}
}

func TestHandleRegister_FirstUser_GetsAdminRole(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"username":"alice","password":"longpassword","display_name":"Alice"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	rec := httptest.NewRecorder()

	h.handleRegister(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp userResponse
	decodeResponse(t, rec, &resp)
	if resp.Role != "admin" {
		t.Errorf("first user should be admin, got role %q", resp.Role)
	}
	if resp.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", resp.Username)
	}
	if resp.DisplayName != "Alice" {
		t.Errorf("expected display_name 'Alice', got %q", resp.DisplayName)
	}
}

func TestHandleRegister_SecondUser_GetsMemberRole(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	// Create first user (admin)
	body1 := strings.NewReader(`{"username":"alice","password":"longpassword","display_name":"Alice"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/register", body1)
	rec1 := httptest.NewRecorder()
	h.handleRegister(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first register failed: %d; body: %s", rec1.Code, rec1.Body.String())
	}

	// Enable registration so subsequent users can register
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   "registration_enabled",
		Value: "true",
	}); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}

	// Create second user (member)
	body2 := strings.NewReader(`{"username":"bob","password":"longpassword","display_name":"Bob"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/register", body2)
	rec2 := httptest.NewRecorder()
	h.handleRegister(rec2, req2)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec2.Code, rec2.Body.String())
	}
	var resp userResponse
	decodeResponse(t, rec2, &resp)
	if resp.Role != "member" {
		t.Errorf("second user should be member, got role %q", resp.Role)
	}
}

func TestHandleRegister_SetsSessionCookie(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	rec := httptest.NewRecorder()

	h.handleRegister(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie should be HttpOnly")
	}
	if sessionCookie.Path != "/" {
		t.Errorf("session cookie path should be '/', got %q", sessionCookie.Path)
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite should be Lax, got %v", sessionCookie.SameSite)
	}
	if sessionCookie.MaxAge != 30*24*3600 {
		t.Errorf("session cookie MaxAge should be %d, got %d", 30*24*3600, sessionCookie.MaxAge)
	}
	if sessionCookie.Value == "" {
		t.Error("session cookie value should not be empty")
	}
}

func TestHandleRegister_ResponseExcludesPasswordHash(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	rec := httptest.NewRecorder()

	h.handleRegister(rec, req)

	// Decode as raw map to check no password_hash field
	var raw map[string]any
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, exists := raw["password_hash"]; exists {
		t.Error("response should not include password_hash")
	}
}

func TestHandleRegister_DuplicateUsername_Returns409(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	body1 := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/register", body1)
	rec1 := httptest.NewRecorder()
	h.handleRegister(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first register failed: %d; body: %s", rec1.Code, rec1.Body.String())
	}

	// Enable registration so the second attempt reaches the duplicate-username check
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   "registration_enabled",
		Value: "true",
	}); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}

	body2 := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/register", body2)
	rec2 := httptest.NewRecorder()
	h.handleRegister(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate username, got %d; body: %s", rec2.Code, rec2.Body.String())
	}
}

func TestHandleRegister_InvalidJSON_Returns400(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	rec := httptest.NewRecorder()

	h.handleRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleLogin ---

func TestHandleLogin_ValidCredentials_Returns200(t *testing.T) {
	h := setupHandler(t)

	// Register a user first
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword","display_name":"Alice"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d", regRec.Code)
	}

	// Login
	loginBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	h.handleLogin(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", loginRec.Code, loginRec.Body.String())
	}

	var resp userResponse
	decodeResponse(t, loginRec, &resp)
	if resp.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", resp.Username)
	}
	if resp.DisplayName != "Alice" {
		t.Errorf("expected display_name 'Alice', got %q", resp.DisplayName)
	}
}

func TestHandleLogin_SetsSessionCookie(t *testing.T) {
	h := setupHandler(t)

	// Register
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)

	// Login
	loginBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	h.handleLogin(loginRec, loginReq)

	cookies := loginRec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set on login")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie should be HttpOnly")
	}
}

func TestHandleLogin_WrongPassword_Returns401(t *testing.T) {
	h := setupHandler(t)

	// Register
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)

	// Login with wrong password
	loginBody := strings.NewReader(`{"username":"alice","password":"wrongpasswd"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	h.handleLogin(loginRec, loginReq)

	if loginRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", loginRec.Code)
	}
}

func TestHandleLogin_NonexistentUser_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"username":"nobody","password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	rec := httptest.NewRecorder()

	h.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogin_NonexistentUser_RunsBcryptEqualizer(t *testing.T) {
	h := setupHandler(t)

	// Register a real user so the wrong-password (known-user) path is exercised.
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	h.handleRegister(httptest.NewRecorder(), regReq)

	// Wrong password for an existing user — pays the full bcrypt compare.
	wrongBody := strings.NewReader(`{"username":"alice","password":"wrongpasswd"}`)
	wrongReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", wrongBody)
	wrongRec := httptest.NewRecorder()
	h.handleLogin(wrongRec, wrongReq)

	// Unknown username — must emit a byte-identical 401, proving the user-miss
	// branch runs the bcrypt equaliser rather than short-circuiting. The
	// auth-package unit tests are the authoritative proof the compare executes
	// at the configured cost; here we lock the response equivalence.
	missBody := strings.NewReader(`{"username":"nobody","password":"longpassword"}`)
	missReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", missBody)
	missRec := httptest.NewRecorder()
	h.handleLogin(missRec, missReq)

	if missRec.Code != http.StatusUnauthorized {
		t.Errorf("user-miss: expected 401, got %d", missRec.Code)
	}
	if missRec.Code != wrongRec.Code {
		t.Errorf("user-miss status %d != wrong-password status %d", missRec.Code, wrongRec.Code)
	}
	if missRec.Body.String() != wrongRec.Body.String() {
		t.Errorf("user-miss body %q != wrong-password body %q", missRec.Body.String(), wrongRec.Body.String())
	}
}

func TestHandleLogin_InvalidJSON_Returns400(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	rec := httptest.NewRecorder()

	h.handleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleLogin_ResponseExcludesPasswordHash(t *testing.T) {
	h := setupHandler(t)

	// Register
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)

	// Login
	loginBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	h.handleLogin(loginRec, loginReq)

	var raw map[string]any
	if err := json.NewDecoder(strings.NewReader(loginRec.Body.String())).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, exists := raw["password_hash"]; exists {
		t.Error("response should not include password_hash")
	}
}

// --- handleLogout ---

func TestHandleLogout_WithValidSession_Returns200(t *testing.T) {
	h := setupHandler(t)

	// Register to get a session
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)

	// Get the session cookie
	var sessionToken string
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionToken = c.Value
			break
		}
	}
	if sessionToken == "" {
		t.Fatal("no session token from register")
	}

	// Logout
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.AddCookie(&http.Cookie{Name: "session", Value: sessionToken})
	logoutRec := httptest.NewRecorder()
	h.handleLogout(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", logoutRec.Code, logoutRec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, logoutRec, &resp)
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
}

func TestHandleLogout_ClearsCookie(t *testing.T) {
	h := setupHandler(t)

	// Register
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)

	var sessionToken string
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionToken = c.Value
			break
		}
	}

	// Logout
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.AddCookie(&http.Cookie{Name: "session", Value: sessionToken})
	logoutRec := httptest.NewRecorder()
	h.handleLogout(logoutRec, logoutReq)

	// Check cookie is cleared
	cookies := logoutRec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie in response")
	}
	if sessionCookie.MaxAge != -1 {
		t.Errorf("session cookie MaxAge should be -1 to clear, got %d", sessionCookie.MaxAge)
	}
}

func TestHandleLogout_DeletesSessionFromDB(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	// Register
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)

	var sessionToken string
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionToken = c.Value
			break
		}
	}

	// Logout
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.AddCookie(&http.Cookie{Name: "session", Value: sessionToken})
	logoutRec := httptest.NewRecorder()
	h.handleLogout(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", logoutRec.Code)
	}

	// Verify session is gone from DB. Sessions are stored hashed, so look up
	// by the hash of the plaintext cookie value.
	_, err := q.GetSession(context.Background(), auth.HashSessionToken(sessionToken))
	if err != sql.ErrNoRows {
		t.Errorf("expected session to be deleted from DB, got err: %v", err)
	}
}

func TestHandleLogout_NoCookie_Returns200(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleLogout(rec, req)

	// Should still return 200 gracefully even without a cookie
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// --- session-token hashing at rest ---

// TestSetSessionCookie_StoresHashNotPlaintext verifies the cookie value handed
// to the browser is never the value stored in sessions.token: the DB holds only
// the SHA-256 hash, so a database/backup leak cannot yield replayable cookies.
func TestSetSessionCookie_StoresHashNotPlaintext(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d body=%s", regRec.Code, regRec.Body.String())
	}

	var cookieValue string
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			cookieValue = c.Value
			break
		}
	}
	if cookieValue == "" {
		t.Fatal("no session cookie set on register")
	}

	// The raw cookie value must NOT be the stored token.
	var rawCount int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions WHERE token = ?`, cookieValue).Scan(&rawCount); err != nil {
		t.Fatalf("count raw token: %v", err)
	}
	if rawCount != 0 {
		t.Error("sessions.token stored the raw cookie value — a DB leak would be directly replayable")
	}

	// The stored token must be HashSessionToken(cookieValue).
	var hashCount int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions WHERE token = ?`, auth.HashSessionToken(cookieValue)).Scan(&hashCount); err != nil {
		t.Fatalf("count hashed token: %v", err)
	}
	if hashCount != 1 {
		t.Errorf("expected exactly one session row keyed by the token hash, got %d", hashCount)
	}
}

// TestSessionLookup_AcceptsPlaintextCookie_RejectsRawDBValue proves that a
// request bearing the plaintext cookie authenticates, while a request bearing
// the raw DB-stored value (the hash) does not — i.e. a leaked DB row is not a
// directly-replayable cookie.
func TestSessionLookup_AcceptsPlaintextCookie_RejectsRawDBValue(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d body=%s", regRec.Code, regRec.Body.String())
	}

	var cookieValue string
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			cookieValue = c.Value
			break
		}
	}
	if cookieValue == "" {
		t.Fatal("no session cookie set on register")
	}
	storedHash := auth.HashSessionToken(cookieValue)

	protected := auth.RequireAuth(q)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Plaintext cookie authenticates.
	okReq := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	okReq.AddCookie(&http.Cookie{Name: "session", Value: cookieValue})
	okRec := httptest.NewRecorder()
	protected.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Errorf("plaintext cookie should authenticate, got %d", okRec.Code)
	}

	// The raw DB-stored value (the hash) must NOT authenticate. It is 64 hex
	// chars so it passes the length check, exercising the lookup path itself.
	if len(storedHash) != 64 {
		t.Fatalf("expected 64-char hash, got %d", len(storedHash))
	}
	badReq := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	badReq.AddCookie(&http.Cookie{Name: "session", Value: storedHash})
	badRec := httptest.NewRecorder()
	protected.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Errorf("raw DB-stored hash must not be a replayable cookie, got %d", badRec.Code)
	}
}

// --- handleMe ---

func TestHandleMe_WithAuthenticatedUser_ReturnsUserJSON(t *testing.T) {
	h := setupHandler(t)

	user := database.User{
		ID:          1,
		Username:    "alice",
		DisplayName: "Alice",
		Role:        "admin",
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	ctx := context.WithValue(context.Background(), auth.UserContextKey, user)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.handleMe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp userResponse
	decodeResponse(t, rec, &resp)
	if resp.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", resp.Username)
	}
	if resp.DisplayName != "Alice" {
		t.Errorf("expected display_name 'Alice', got %q", resp.DisplayName)
	}
	if resp.Role != "admin" {
		t.Errorf("expected role 'admin', got %q", resp.Role)
	}
	if resp.ID != 1 {
		t.Errorf("expected ID 1, got %d", resp.ID)
	}
}

func TestHandleMe_WithoutUser_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	rec := httptest.NewRecorder()

	h.handleMe(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleMe_ResponseExcludesPasswordHash(t *testing.T) {
	h := setupHandler(t)

	user := database.User{
		ID:           1,
		Username:     "alice",
		PasswordHash: "$2a$12$secret",
		DisplayName:  "Alice",
		Role:         "admin",
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	ctx := context.WithValue(context.Background(), auth.UserContextKey, user)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.handleMe(rec, req)

	var raw map[string]any
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, exists := raw["password_hash"]; exists {
		t.Error("response should not include password_hash")
	}
}

// --- toUserResponse helper ---

func TestToUserResponse_MapsFieldsCorrectly(t *testing.T) {
	now := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	u := database.User{
		ID:           42,
		Username:     "bob",
		PasswordHash: "should-not-appear",
		DisplayName:  "Bob",
		Role:         "member",
		CreatedAt:    now,
	}

	resp := toUserResponse(u)

	if resp.ID != 42 {
		t.Errorf("expected ID 42, got %d", resp.ID)
	}
	if resp.Username != "bob" {
		t.Errorf("expected username 'bob', got %q", resp.Username)
	}
	if resp.DisplayName != "Bob" {
		t.Errorf("expected display_name 'Bob', got %q", resp.DisplayName)
	}
	if resp.Role != "member" {
		t.Errorf("expected role 'member', got %q", resp.Role)
	}
	if !resp.CreatedAt.Equal(now) {
		t.Errorf("expected created_at %v, got %v", now, resp.CreatedAt)
	}
}
