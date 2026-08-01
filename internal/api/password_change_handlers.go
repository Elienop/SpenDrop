package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// passwordTooShortMessage / passwordTooLongMessage are the single source of the
// wording for the password bounds, shared by handleRegister, handleCreateUser
// and validateNewPassword so all three password-entry paths say the same thing.
//
// They say BYTES, not characters, and that is the honest unit rather than a
// stylistic choice. bcrypt hashes at most the first 72 bytes of its input and
// silently discards the rest, so this bound cannot be moved to characters
// without accepting a password whose tail never reaches the hasher — two
// distinct passwords agreeing on their first 72 bytes would authenticate the
// same account. config.Validate refuses a PASSWORD_MAX_LENGTH above 72 for the
// same reason.
//
// Every other user-facing text cap in this app is enforced in characters (see
// charLen in limits.go). This one cannot be, so it stops claiming to be: a
// 40-character Arabic password is 80 bytes, and telling that user "72
// characters or less" would name a limit the server is not applying. The
// clarifier is on the maximum only, because that is the bound that can refuse
// text a character count would have admitted.
func passwordTooShortMessage(minLen int) string {
	return fmt.Sprintf("password must be at least %d bytes", minLen)
}

func passwordTooLongMessage(maxLen int) string {
	return fmt.Sprintf(
		"password must be %d bytes or less (accented and non-Latin characters use more than one byte each)",
		maxLen)
}

// validateNewPassword enforces the runtime-configured min/max byte length on a
// new password. Returns an error message (empty == valid) so callers can map
// it straight to a 400 body. Mirrors the bounds checks in handleRegister /
// handleCreateUser so all three password-entry paths agree.
func validateNewPassword(pw string) string {
	if pw == "" {
		return "password is required"
	}
	minLen, maxLen := getPasswordBounds()
	if len(pw) < minLen {
		return passwordTooShortMessage(minLen)
	}
	if len(pw) > maxLen {
		return passwordTooLongMessage(maxLen)
	}
	return ""
}

// handleChangePassword is the self-service password-change endpoint
// (POST /api/auth/password). Any authenticated user may rotate their own
// password. It implements the cascade locked in by
// internal/database/store_api_token_password_cascade_test.go and the 9-step
// TODO in auth_handlers.go: verify current password, validate + hash the new
// one, then in ONE serializable tx update the hash, revoke every live API
// token (audited as revoked_by_password_change), and delete every session for
// the caller. The caller's own session dies with the rest, so the frontend
// must redirect to /login on the next 401.
func (h *Handler) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Step 1: rate-limit the current-password check BEFORE the bcrypt compare.
	// A stolen session cookie otherwise allows unbounded current-password
	// guessing, and the unthrottled bcrypt is a CPU-DoS lever. The bucket is
	// per-user (keyed by the authenticated user's ID); checking Exhausted
	// before CheckPassword means an exhausted bucket short-circuits bcrypt
	// entirely. Mirrors the createTokenLimiter 429/Retry-After shape.
	failKey := strconv.FormatInt(user.ID, 10)
	if h.passwordChangeFailLimiter.Exhausted(failKey) {
		w.Header().Set("Retry-After", h.passwordChangeFailLimiter.RetryAfter(failKey))
		writeError(w, http.StatusTooManyRequests, "too many password-change attempts, try again later")
		return
	}

	// Step 2: verify the caller's current password BEFORE any mutation. A
	// wrong current password is an authentication failure, not a validation
	// error, so it maps to 401 with the same generic message as login. Only a
	// WRONG password records a failure hit; a correct one leaves the bucket
	// untouched so legitimate use after a few typos is not stranded.
	if !auth.CheckPassword(user.PasswordHash, req.CurrentPassword) {
		h.passwordChangeFailLimiter.Consume(failKey)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Step 3: validate the new password against the runtime bounds.
	if msg := validateNewPassword(req.NewPassword); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	// Step 4: hash the new password.
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	tokensRevoked, err := h.runPasswordResetCascade(r, user.ID, newHash, newActorContext(r, user))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to change password")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "password_changed",
		"tokens_revoked": tokensRevoked,
	})
}

// handleAdminResetPassword lets an admin reset another user's password
// (POST /api/users/{id}/reset-password). Mounted under the /api/users
// RequireAdmin group. There is NO current-password check — the admin's
// authority is the authorization. The cascade runs against the TARGET user:
// update hash, revoke the target's tokens (audited as
// revoked_by_password_change), delete the target's sessions, all in one
// serializable tx. The acting admin is captured in the audit row's actor_*
// columns via newActorContext; the audit row's user_id is the TARGET (the
// token owner) because api_token_audit.user_id FKs the token's owner.
func (h *Handler) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	admin, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	targetID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || targetID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	// An admin must not reset their OWN password through this endpoint: it
	// skips the current-password check that self-service requires, so allowing
	// self-target would let an admin bypass that safeguard. They must use the
	// Account page (POST /api/auth/password) instead.
	if targetID == admin.ID {
		writeError(w, http.StatusBadRequest, "use the Account page to change your own password")
		return
	}

	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if msg := validateNewPassword(req.NewPassword); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	// 404 if the target user does not exist — checked before any mutation so
	// the response is a clean not-found rather than a silent no-op.
	if _, err := h.queries.GetUserByID(r.Context(), targetID); err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	// Actor context: actor_* columns (ip / user-agent / session hash) capture
	// the acting ADMIN, but ActorContext.UserID is the TARGET because the
	// audit row's user_id is the token owner. newActorContext derives actor_*
	// from the request (the admin's), so we override UserID to the target.
	actor := newActorContext(r, admin)
	actor.UserID = targetID

	tokensRevoked, err := h.runPasswordResetCascade(r, targetID, newHash, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset password")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "password_reset",
		"tokens_revoked": tokensRevoked,
	})
}

// runPasswordResetCascade runs the atomic password-rotation cascade for the
// given user inside one sql.LevelSerializable transaction:
//
//  1. UpdateUserPassword(userID) -> the new hash.
//  2. RevokeAllForUserTx(actor, revoked_by_password_change) -> revoke every
//     live API token, one audit row each, in the SAME tx.
//  3. DeleteSessionsByUserID(userID) -> sign out every device.
//
// It returns the number of tokens revoked. Either everything commits or
// nothing does. RevokeAllForUserTx (not the non-Tx RevokeAllForUser) is used
// deliberately so the revoke shares this tx with the UPDATE and the session
// delete — see the contract on store_api_token.go.
//
// actor.UserID controls which user's tokens are revoked and which user_id the
// audit rows are attributed to; callers set it (self for change, target for
// admin reset) before calling.
func (h *Handler) runPasswordResetCascade(r *http.Request, userID int64, newHash string, actor database.ActorContext) (int64, error) {
	tx, err := h.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	qtx := h.queries.WithTx(tx)

	if err := qtx.UpdateUserPassword(r.Context(), database.UpdateUserPasswordParams{
		PasswordHash: newHash,
		ID:           userID,
	}); err != nil {
		return 0, fmt.Errorf("update password: %w", err)
	}

	tokensRevoked, err := h.apiTokenStore.RevokeAllForUserTx(r.Context(), tx, actor,
		database.APITokenAuditRevokedByPasswordChange)
	if err != nil {
		return 0, fmt.Errorf("revoke tokens: %w", err)
	}

	if err := qtx.DeleteSessionsByUserID(r.Context(), userID); err != nil {
		return 0, fmt.Errorf("delete sessions: %w", err)
	}

	// Push subscriptions are a standing capability to receive household
	// activity: whoever holds the endpoint keeps getting notifications
	// regardless of sessions or tokens. Revoking sessions and API tokens but
	// leaving these alive meant a compromised or lost device carried on
	// receiving every transaction after the password was rotated — precisely
	// the thing the rotation was meant to stop. Revoked in the SAME
	// transaction as the password write so the cascade cannot half-apply.
	if _, err := qtx.DeletePushSubscriptionsByUser(r.Context(), userID); err != nil {
		return 0, fmt.Errorf("revoke push subscriptions: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return tokensRevoked, nil
}
