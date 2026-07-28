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
	if req.Role != RoleMember && req.Role != RoleAdmin {
		writeError(w, http.StatusBadRequest, "role must be 'member' or 'admin'")
		return
	}

	if len(req.DisplayName) > MaxDisplayNameLength {
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

	if req.DisplayName != "" && len(req.DisplayName) > MaxDisplayNameLength {
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
