package api

import (
	"database/sql"
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

const sessionCookieMaxAge = 30 * 24 * 3600 // 30 days in seconds

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
// cookie on the response.
func (h *Handler) setSessionCookie(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := auth.GenerateSessionToken()
	if err != nil {
		return err
	}

	expiresAt := time.Now().Add(time.Duration(sessionCookieMaxAge) * time.Second)
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
		MaxAge:   sessionCookieMaxAge,
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
	if attempts >= 10 {
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
	if len(req.Username) < 3 || len(req.Username) > 32 {
		writeError(w, http.StatusBadRequest, "username must be between 3 and 32 characters")
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
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if len(req.Password) > 72 {
		writeError(w, http.StatusBadRequest, "password must be 72 characters or less")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	// Default display name to username if not provided
	displayName := req.DisplayName
	if len(displayName) > 64 {
		writeError(w, http.StatusBadRequest, "display name must be 64 characters or less")
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
	role := "member"
	users, err := qtx.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check existing users")
		return
	}
	if len(users) == 0 {
		role = "admin"
	} else {
		// Check if registration is enabled via app setting
		setting, err := qtx.GetSetting(r.Context(), "registration_enabled")
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
	loginAttempts      = make(map[string]int)
	registerAttempts   = make(map[string]int)
	rateLimitMu        sync.Mutex
	rateLimitWindow    = time.Minute
)

func init() {
	// Reset rate limit counters every minute
	go func() {
		ticker := time.NewTicker(rateLimitWindow)
		defer ticker.Stop()
		for range ticker.C {
			rateLimitMu.Lock()
			loginAttempts = make(map[string]int)
			registerAttempts = make(map[string]int)
			rateLimitMu.Unlock()
		}
	}()
}

// handleLogin authenticates a user by username and password.
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := extractIP(r.RemoteAddr)
	rateLimitMu.Lock()
	attempts := loginAttempts[clientIP]
	rateLimitMu.Unlock()
	if attempts >= 10 {
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
		rateLimitMu.Lock()
		loginAttempts[clientIP]++
		rateLimitMu.Unlock()
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		rateLimitMu.Lock()
		loginAttempts[clientIP]++
		rateLimitMu.Unlock()
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := h.setSessionCookie(w, r, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	// Clear rate limit counter on successful login so shared-IP household
	// users aren't penalised by earlier failed attempts.
	rateLimitMu.Lock()
	delete(loginAttempts, clientIP)
	rateLimitMu.Unlock()

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
