package api

import (
	"database/sql"
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

	// LENGTH AND CONTENT, the same call the other three display_name write paths
	// make — see display_name.go. The fallback below needs no check of its own:
	// req.Username is already past isValidUsername, which admits only
	// [a-zA-Z0-9_-].
	if err := validateDisplayName(req.DisplayName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	// LENGTH AND CONTENT — see display_name.go. The `!= ""` guard the length
	// check used to carry is gone rather than moved: validateDisplayName returns
	// nil for "", which is what this handler means by "not supplied" (it merges
	// the stored name in below). Keeping the guard would only restate that.
	//
	// It validates the REQUEST field, not the merged value. A role-only edit of
	// a user whose STORED name predates this rule must still succeed — refusing
	// it would leave an admin unable to change that user's role at all, which is
	// a worse failure than a legacy name keeping its contents.
	if err := validateDisplayName(req.DisplayName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	// THE MERGE READ, THE WRITE AND THE SESSION WIPE SHARE ONE TRANSACTION, so
	// that a 200 on this endpoint is a promise about BOTH: the role that is
	// stored, and the absence of any session predating it. Before B20 the wipe
	// was
	// `_ = h.queries.DeleteSessionsByUserID(...)` — a discarded error, so a
	// failed DELETE answered "updated" while every old session survived.
	//
	// Rolling the role change BACK on a failed wipe is the deliberate choice
	// over letting the demotion stand, for two reasons:
	//
	//   * The retry has to work. handleUpdateUser merges the stored role when
	//     the request omits one and only wipes `if role != existing.Role`, so
	//     a demotion that persisted WITHOUT its wipe is invisible to the next
	//     attempt: the retry sees role == existing.Role, skips the wipe
	//     entirely, and returns 200 over the sessions it was meant to clear.
	//     All-or-nothing makes the retry identical to the first attempt.
	//
	//   * Nothing urgent is lost by rolling back. Authorization is re-read per
	//     request — authenticateSession and RequireAPIToken both call
	//     GetUserByID and RequireAdmin reads that row's Role — so a surviving
	//     cookie carries no stale privilege in the first place. The wipe
	//     exists to force a fresh login (the client caches its own copy of the
	//     role), not to be the revocation itself. So the trade is "the whole
	//     edit lands, or none of it does, and the admin is told" against
	//     "half of it lands and the admin is told it all did".
	//
	// Same shape and same reasoning as runPasswordResetCascade in
	// password_change_handlers.go, which has wrapped its password write and
	// its session wipe in one transaction since the credential-cascade work.
	//
	// THE MERGE READ IS INSIDE THE TRANSACTION, and that placement is
	// load-bearing rather than tidy. This is a read-modify-write: the request
	// may omit display_name or role, and the omitted field is filled from the
	// stored row. Reading that row on h.queries before BeginTx released the
	// connection between the read and the write, so two concurrent PUTs on the
	// same user could interleave read, read, write, write. Both would see
	// role='admin'; a demotion could commit and wipe the sessions; a rename
	// arriving with no role of its own would then merge its stale snapshot,
	// write role='admin' back, and — because its own comparison `role !=
	// existing.Role` is false against that stale value — return 200 without
	// wiping anything. Net effect: an acknowledged demotion silently reverted,
	// with live sessions, and no error anywhere. Inside the transaction the
	// read and the write are one unit: SetMaxOpenConns(1) means the
	// transaction holds the only connection for its whole life, so no second
	// request can run a statement between them.
	//
	// The corollary is that NOTHING here may use h.queries: a query issued on
	// the pool while this transaction is open waits for a connection only the
	// transaction can release, which is a deadlock rather than a slow path.
	// Every read below goes through qtx.
	tx, err := h.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds
	qtx := h.queries.WithTx(tx)

	// Fetch existing user to merge unspecified fields. The 404 return here
	// leaves the transaction open on the stack; the deferred Rollback above is
	// what hands the single connection back, so this early exit must never be
	// rewritten to return before that defer is registered.
	existing, fetchErr := qtx.GetUserByID(r.Context(), id)
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

	if err := qtx.UpdateUser(r.Context(), database.UpdateUserParams{
		DisplayName: displayName,
		Role:        role,
		ID:          id,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Invalidate sessions if role changed (prevents privilege persistence).
	// The comparison is against the STORED role, so a request that re-sends
	// the role a user already has logs nobody out.
	if role != existing.Role {
		if err := qtx.DeleteSessionsByUserID(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to invalidate sessions after role change")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
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

	// Clean up sessions before deleting the user.
	//
	// KEPT DESPITE THE CASCADE, and error-checked rather than discarded.
	// sessions.user_id is ON DELETE CASCADE (migration 001, verified against a
	// fully migrated database with PRAGMA foreign_key_list), and production
	// runs with _foreign_keys=on — so on the default configuration DeleteUser
	// below would clear these rows by itself. The explicit delete covers the
	// configuration where it would not: Config.SQLite.ForeignKeys is a field,
	// and a handle opened with `_foreign_keys=off` fires no cascade at all.
	//
	// What it is NOT is a security control. An orphaned session is inert:
	// authenticateSession resolves the row, then calls GetUserByID, and a
	// missing user is a 401. So the cost of the FK-off case is stale rows, not
	// access — which is why this runs before the delete without a transaction
	// around the pair. The only interleaving that leaves work half done is
	// "sessions cleared, user delete then failed", i.e. an account that is
	// still present but logged out, which the owner resolves by logging in
	// again.
	//
	// The error is surfaced instead of discarded because a DELETE that cannot
	// run says the database is refusing writes; continuing on to a DELETE of
	// the user row is not a recovery, it is the same failure with a bigger
	// blast radius.
	if err := h.queries.DeleteSessionsByUserID(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up user sessions")
		return
	}

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
