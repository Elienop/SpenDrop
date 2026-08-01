package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// handleListUsers returns all users without password hashes.
func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	users, err := h.queries.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	resp := make([]userResponse, len(users))
	for i, u := range users {
		resp[i] = toUserResponse(u)
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleCreateUser creates a new user (admin only).
func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
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
	// CHARSET GATE FIRST, and the order is load-bearing rather than stylistic.
	// isValidUsername admits only [a-zA-Z0-9_-], every one of which is one byte
	// in UTF-8, so once a value is past this line its byte count and its
	// character count are the same number — which is what lets the bound below
	// stay len() while its message says characters.
	//
	// Running the length check first made that equivalence something a reader
	// had to establish by looking ahead, and it also mislabelled the refusal: 20
	// Arabic letters are 40 bytes, so a non-ASCII username was answered with
	// "must be between 3 and 32 characters" — a count it did not violate — when
	// the charset message is the apt one. Both are fixed by asking the question
	// in this order. The empty case is already answered above, so this gate
	// never sees "".
	if !isValidUsername(req.Username) {
		writeError(w, http.StatusBadRequest, "username may only contain letters, numbers, hyphens, and underscores")
		return
	}
	// BYTES, which is the same number as characters by construction — see the
	// charset gate immediately above. Converting to charLen would buy nothing
	// and add a unit a reader has to re-derive.
	if len(req.Username) < MinUsernameLength || len(req.Username) > MaxUsernameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("username must be between %d and %d characters", MinUsernameLength, MaxUsernameLength))
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	// BYTES, and this one must stay bytes: bcrypt hashes at most the first 72
	// bytes of its input and silently discards the rest, so a rune-based bound
	// would accept a password whose tail never reaches the hasher — and two
	// different passwords agreeing on their first 72 bytes would then
	// authenticate the same account. len() is the quantity bcrypt actually
	// consumes. See getPasswordBounds in runtime.go, and the identical bounds in
	// handleRegister and validateNewPassword.
	minLen, maxLen := getPasswordBounds()
	if len(req.Password) < minLen {
		writeError(w, http.StatusBadRequest, passwordTooShortMessage(minLen))
		return
	}
	if len(req.Password) > maxLen {
		writeError(w, http.StatusBadRequest, passwordTooLongMessage(maxLen))
		return
	}
	if req.Role != RoleMember && req.Role != RoleAdmin {
		writeError(w, http.StatusBadRequest, "role must be 'member' or 'admin'")
		return
	}

	if charLen(req.DisplayName) > MaxDisplayNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("display name must be %d characters or less", MaxDisplayNameLength))
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user, err := h.queries.CreateUser(r.Context(), database.CreateUserParams{
		Username:     req.Username,
		PasswordHash: hash,
		DisplayName:  req.DisplayName,
		Role:         req.Role,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

// handleUpdateUser updates user display_name and role (admin only).
func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	var req struct {
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" && req.Role == "" {
		writeError(w, http.StatusBadRequest, "display_name or role is required")
		return
	}

	if req.DisplayName != "" && charLen(req.DisplayName) > MaxDisplayNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("display name must be %d characters or less", MaxDisplayNameLength))
		return
	}

	if req.Role != "" && req.Role != RoleMember && req.Role != RoleAdmin {
		writeError(w, http.StatusBadRequest, "role must be 'member' or 'admin'")
		return
	}

	// Prevent admin from demoting themselves (could lock out all admins)
	if req.Role == RoleMember && id == currentUser.ID {
		writeError(w, http.StatusBadRequest, "cannot demote yourself")
		return
	}

	// Fetch existing user to merge unspecified fields
	existing, fetchErr := h.queries.GetUserByID(r.Context(), id)
	if fetchErr != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = existing.DisplayName
	}
	role := req.Role
	if role == "" {
		role = existing.Role
	}

	err = h.queries.UpdateUser(r.Context(), database.UpdateUserParams{
		DisplayName: displayName,
		Role:        role,
		ID:          id,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Invalidate sessions if role changed (prevents privilege persistence)
	if role != existing.Role {
		_ = h.queries.DeleteSessionsByUserID(r.Context(), id)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleDeleteUser deletes a user (admin only). Cannot delete self.
func (h *Handler) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	currentUser, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	if id == currentUser.ID {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}

	// Refuse to delete a user who still owns ledger rows.
	//
	// transactions.user_id is declared ON DELETE CASCADE
	// (migrations/002_cascade_deletes.sql, re-declared in 010), and production
	// runs with _foreign_keys=on, so a bare DELETE here permanently destroys
	// every transaction the user ever created: no tombstone, no Trash entry,
	// no transaction_audit row, no restore path. That bypasses the entire
	// soft-delete contract the rest of the app is built on, and it silently
	// rewrites every historical report.
	//
	// The count deliberately includes tombstoned rows — a row in the Trash is
	// still recoverable history, and the cascade destroys it just as
	// permanently. Mirrors handleDeleteCategory's 409 on FK conflict; the
	// difference is that categories have no CASCADE so SQLite raises the
	// error itself, whereas here the cascade would succeed silently.
	txnCount, err := h.queries.CountTransactionsByUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check user transactions")
		return
	}
	if txnCount > 0 {
		writeError(w, http.StatusConflict,
			"cannot delete a user who has transactions — reassign or purge their transactions first")
		return
	}

	// balance_checkpoints.user_id is the second ON DELETE CASCADE edge off
	// users (migrations/007_balance_checkpoints.sql:69). A member with zero
	// transactions but a reconciliation checkpoint passed the guard above and
	// had their bank-statement anchors destroyed. Checkpoints are hand-entered
	// assertions with no tombstone, no Trash and no restore path, so the loss
	// is total — same 409 shape as the transactions guard.
	//
	// Deliberately NOT guarded: transaction_audit.actor_user_id is ON DELETE
	// SET NULL (migration 009), so deleting a user destroys no history. It
	// does anonymise it: a member who only ever edited other people's rows can
	// be deleted, and every audit row they authored loses its actor. That is
	// accepted — blocking a delete on audit provenance would make members
	// permanently undeletable — but it is not obvious from the schema, so it
	// is recorded here and in the README's user-deletion note.
	checkpointCount, err := h.queries.CountCheckpointsByUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check user checkpoints")
		return
	}
	if checkpointCount > 0 {
		writeError(w, http.StatusConflict,
			"cannot delete a user who has balance checkpoints — delete their checkpoints first")
		return
	}

	// Clean up sessions before deleting the user
	_ = h.queries.DeleteSessionsByUserID(r.Context(), id)

	result, err := h.queries.DeleteUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
