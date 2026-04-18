package api

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// extractIP strips the port from r.RemoteAddr to key rate limits by IP only.
func extractIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// userResponse is the JSON representation of a user, excluding password_hash.
type userResponse struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// toUserResponse converts a database.User to a userResponse, stripping the
// password hash.
func toUserResponse(u database.User) userResponse {
	return userResponse{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		CreatedAt:   u.CreatedAt,
	}
}

// setSessionCookie creates a new session in the database and sets the session
// cookie on the response. The cookie lifetime is read from the runtime config
// via getSessionTTL so operators can tune SESSION_TTL without a code change.
func (h *Handler) setSessionCookie(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := auth.GenerateSessionToken()
	if err != nil {
		return err
	}

	ttl := getSessionTTL()
	expiresAt := time.Now().Add(ttl)
	err = h.queries.CreateSession(r.Context(), database.CreateSessionParams{
		Token:     token,
		UserID:    userID,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   shouldMarkCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
	return nil
}

// handleRegister creates a new user account. The first registered user is
// assigned the "admin" role; subsequent users get "member".
func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	clientIP := extractIP(r.RemoteAddr)
	rateLimitMu.Lock()
	attempts := registerAttempts[clientIP]
	rateLimitMu.Unlock()
	if attempts >= getRateLimitMax() {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}

	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)

	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if len(req.Username) < MinUsernameLength || len(req.Username) > MaxUsernameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("username must be between %d and %d characters", MinUsernameLength, MaxUsernameLength))
		return
	}
	if !isValidUsername(req.Username) {
		writeError(w, http.StatusBadRequest, "username may only contain letters, numbers, hyphens, and underscores")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	minLen, maxLen := getPasswordBounds()
	if len(req.Password) < minLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", minLen))
		return
	}
	if len(req.Password) > maxLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("password must be %d characters or less", maxLen))
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	// Default display name to username if not provided
	displayName := req.DisplayName
	if len(displayName) > MaxDisplayNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("display name must be %d characters or less", MaxDisplayNameLength))
		return
	}
	if displayName == "" {
		displayName = req.Username
	}

	// Use a transaction to prevent TOCTOU race on first-user admin assignment.
	// BEGIN IMMEDIATE acquires SQLite's write lock, serializing concurrent registrations.
	tx, err := h.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	qtx := h.queries.WithTx(tx)

	// First user gets admin role
	role := RoleMember
	users, err := qtx.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check existing users")
		return
	}
	if len(users) == 0 {
		role = RoleAdmin
	} else {
		// Check if registration is enabled via app setting
		setting, err := qtx.GetSetting(r.Context(), SettingRegistrationEnabled)
		if err != nil || setting.Value != "true" {
			writeError(w, http.StatusForbidden, "registration is disabled")
			return
		}
	}

	user, err := qtx.CreateUser(r.Context(), database.CreateUserParams{
		Username:     req.Username,
		PasswordHash: hash,
		DisplayName:  displayName,
		Role:         role,
	})
	if err != nil {
		rateLimitMu.Lock()
		registerAttempts[clientIP]++
		rateLimitMu.Unlock()
		// SQLite UNIQUE constraint violation
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit registration")
		return
	}

	if err := h.setSessionCookie(w, r, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

var (
	registerAttempts = make(map[string]int)
	rateLimitMu      sync.Mutex

	// rateLimitTickerOnce makes startRateLimitReset idempotent so multiple
	// NewRouter calls (e.g. from test setups that spin up several routers in
	// a single process) don't leak background goroutines.
	rateLimitTickerOnce sync.Once
)

// startRateLimitReset launches the background goroutine that clears the
// rate-limit attempt counters every rateLimitTickerWindow. Called exactly
// once from NewRouter after ApplyConfig has set the window. Safe to call
// multiple times — subsequent calls are no-ops.
func startRateLimitReset() {
	rateLimitTickerOnce.Do(func() {
		go func() {
			runtimeMu.RLock()
			window := rateLimitTickerWindow
			runtimeMu.RUnlock()
			ticker := time.NewTicker(window)
			defer ticker.Stop()
			for range ticker.C {
				rateLimitMu.Lock()
				registerAttempts = make(map[string]int)
				rateLimitMu.Unlock()
			}
		}()
	})
}

// handleLogin authenticates a user by username and password.
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := extractIP(r.RemoteAddr)
	if h.loginFailureLimiter.Exhausted(clientIP) {
		w.Header().Set("Retry-After", h.loginFailureLimiter.RetryAfter(clientIP))
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.queries.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		h.loginFailureLimiter.Consume(clientIP)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		h.loginFailureLimiter.Consume(clientIP)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := h.setSessionCookie(w, r, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	// Clear rate limit counter on successful login so shared-IP household
	// users aren't penalised by earlier failed attempts.
	h.loginFailureLimiter.Reset(clientIP)

	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// handleLogout invalidates the current session and clears the cookie.
// Requires JSON Content-Type to prevent CSRF via form submission.
func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}

	cookie, err := r.Cookie("session")
	if err == nil {
		// Best-effort delete; ignore errors (token may already be gone)
		h.queries.DeleteSession(r.Context(), cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   shouldMarkCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleMe returns the currently authenticated user's profile.
func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// usernameRegexp matches valid usernames: letters, numbers, hyphens, underscores.
var usernameRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// isValidUsername checks that a username contains only allowed characters.
func isValidUsername(s string) bool {
	return usernameRegexp.MatchString(s)
}

// TODO(password-change-handler): When SpenDrop adds a self-service password
// change endpoint (POST /api/auth/password), implement it using the cascade
// sequence locked in by
// internal/database/store_api_token_password_cascade_test.go. The sequence
// MUST run inside one sql.Tx:
//
//  1. Verify the caller's current password (outside the tx — no mutation yet).
//     auth.CheckPassword(user.PasswordHash, req.CurrentPassword) must return
//     true; otherwise writeError 401 "invalid credentials".
//  2. Validate the new password against getPasswordBounds() (min/max bytes).
//  3. Hash the new password with auth.HashPassword.
//  4. tx, err := h.db.BeginTx(r.Context(), &sql.TxOptions{
//         Isolation: sql.LevelSerializable,
//     })
//     defer tx.Rollback()
//     qtx := h.queries.WithTx(tx)
//  5. qtx.UpdateUserPassword(r.Context(), database.UpdateUserPasswordParams{
//         PasswordHash: newHash,
//         ID:           user.ID,
//     })
//  6. h.apiTokenStore.RevokeAllForUserTx(r.Context(), tx,
//         database.ActorContext{
//             UserID:      user.ID,
//             IP:          extractIP(r.RemoteAddr),
//             UserAgent:   r.UserAgent(),
//             SessionHash: auth.HashSessionToken(sessionCookie),
//         },
//         database.APITokenAuditRevokedByPasswordChange)
//  7. qtx.DeleteSessionsByUserID(r.Context(), user.ID)
//  8. tx.Commit()
//  9. Respond 200 with {"status": "password_changed", "tokens_revoked": N}.
//     The frontend must redirect to /login on the next 401 from any
//     subsequent request (the caller's own session was killed in step 7).
//
// DO NOT skip any step. DO NOT call store.RevokeAllForUser (non-Tx) — it
// opens its own tx and breaks atomicity with the UPDATE and the session
// delete. DO NOT pass APITokenAuditRevokedByMassRevoke or any other action
// — that action label is reserved for the settings-UI "revoke all my
// tokens" flow and would misclassify the audit trail.
