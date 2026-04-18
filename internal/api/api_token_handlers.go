package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

const (
	apiTokenNameMin        = 1
	apiTokenNameMax        = 100
	apiTokenMaxExpiryYears = 10
)

type apiTokenCreateRequest struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at"`
	Password  string     `json:"password"`
}

type apiTokenCreateResponse struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	Token       string     `json:"token"` // plaintext — only present on this response
}

type apiTokenListItem struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	LastUsedIP  *string    `json:"last_used_ip"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

type apiTokenListResponse struct {
	Tokens []apiTokenListItem `json:"tokens"`
}

// newActorContext builds the audit-capture bundle. Called from every
// mutation handler. Session cookie value is hashed, never stored raw.
func newActorContext(r *http.Request, user database.User) database.ActorContext {
	ip := extractIP(r.RemoteAddr)
	ua := r.Header.Get("User-Agent")
	if len(ua) > 500 {
		ua = ua[:500]
	}
	sessionHash := ""
	if cookie, err := r.Cookie("session"); err == nil {
		sessionHash = auth.HashSessionToken(cookie.Value)
	}
	return database.ActorContext{
		UserID:      user.ID,
		IP:          ip,
		UserAgent:   ua,
		SessionHash: sessionHash,
	}
}

// handleCreateAPIToken mints a new API token. Requires session auth (from
// RequireAuth in router.go) plus an in-body password reconfirm. Failed
// reconfirms consume the shared login-failure bucket (same bucket
// handleLogin uses). Successful creates consume the 5/hr createTokenLimiter.
func (h *Handler) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Create-limit gate runs FIRST so an already-exhausted user doesn't
	// burn a bcrypt compare on every poll.
	userKey := strconv.FormatInt(user.ID, 10)
	if h.createTokenLimiter.Exhausted(userKey) {
		w.Header().Set("Retry-After", h.createTokenLimiter.RetryAfter(userKey))
		writeError(w, http.StatusTooManyRequests, "token creation rate limit exceeded")
		return
	}

	var req apiTokenCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if len(name) < apiTokenNameMin || len(name) > apiTokenNameMax {
		writeError(w, http.StatusBadRequest, "invalid name")
		return
	}

	var expiresAt sql.NullTime
	if req.ExpiresAt != nil {
		if !req.ExpiresAt.After(time.Now()) {
			writeError(w, http.StatusBadRequest, "invalid expires_at")
			return
		}
		maxFuture := time.Now().AddDate(apiTokenMaxExpiryYears, 0, 0)
		if req.ExpiresAt.After(maxFuture) {
			writeError(w, http.StatusBadRequest, "invalid expires_at")
			return
		}
		expiresAt = sql.NullTime{Time: req.ExpiresAt.UTC(), Valid: true}
	}

	if req.Password == "" || !auth.CheckPassword(user.PasswordHash, req.Password) {
		// Failed reconfirm — consume the LOGIN bucket, not the create bucket.
		// Spec §3.7: prevents slow-probing passwords on the create path.
		h.loginFailureLimiter.Consume(extractIP(r.RemoteAddr))
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	plaintext, hash, prefix, err := auth.GenerateAPIToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	actor := newActorContext(r, user)
	created, err := h.apiTokenStore.Create(r.Context(), actor, database.CreateAPITokenParams{
		UserID:      user.ID,
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: prefix,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	// Only consume the create bucket on success (spec §3.7 — failed reconfirms
	// do not burn the create quota).
	h.createTokenLimiter.Consume(userKey)

	writeJSON(w, http.StatusCreated, apiTokenCreateResponse{
		ID:          created.ID,
		Name:        created.Name,
		TokenPrefix: created.TokenPrefix,
		ExpiresAt:   nullTimeToPtr(created.ExpiresAt),
		CreatedAt:   created.CreatedAt,
		Token:       plaintext,
	})
}

func nullTimeToPtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}

func nullStringToPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

// handleListAPITokens returns every live (not-revoked, not-expired) token
// for the caller. No hashes, no plaintexts — only the 15-char prefix so
// the UI can show which row they're looking at.
func (h *Handler) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rows, err := h.queries.ListLiveAPITokensForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}

	items := make([]apiTokenListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, apiTokenListItem{
			ID:          row.ID,
			Name:        row.Name,
			TokenPrefix: row.TokenPrefix,
			CreatedAt:   row.CreatedAt,
			LastUsedAt:  nullTimeToPtr(row.LastUsedAt),
			LastUsedIP:  nullStringToPtr(row.LastUsedIp),
			ExpiresAt:   nullTimeToPtr(row.ExpiresAt),
		})
	}
	writeJSON(w, http.StatusOK, apiTokenListResponse{Tokens: items})
}

// handleRevokeAPIToken soft-deletes one token owned by the caller.
// Idempotent: revoking an already-revoked token returns 200 without
// emitting a second audit row. Cross-user ids return 404 (same body as
// "id does not exist" — no enumeration oracle).
func (h *Handler) handleRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}

	actor := newActorContext(r, user)
	err = h.apiTokenStore.Revoke(r.Context(), actor, id)
	if errors.Is(err, database.ErrTokenNotFound) {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleRevokeAllAPITokens bulk-revokes every live token for the caller and
// returns the number of tokens revoked. Emits one audit row per token
// (action = revoked_by_mass_revoke). A user with zero live tokens gets
// {"revoked": 0} and zero audit rows.
func (h *Handler) handleRevokeAllAPITokens(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	actor := newActorContext(r, user)
	n, err := h.apiTokenStore.RevokeAllForUser(r.Context(), actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke tokens")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}
