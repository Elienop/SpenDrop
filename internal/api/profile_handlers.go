package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// handleUpdateMe is the self-service profile endpoint (PATCH /api/auth/me).
// Any authenticated user — member or admin — may change THEIR OWN display name,
// and nothing else. It exists because a display name labels every transaction
// its owner enters, household-wide, and the only other write path for that
// column is PUT /api/users/{id}, which sits behind RequireAdmin.
//
// THE TARGET COMES FROM THE SESSION AND ONLY FROM THE SESSION. The route
// carries no {id} and the decoded request has no field naming a user, so there
// is no input a caller could point at somebody else's row. That is the reason
// this is the write half of GET /api/auth/me rather than a loosened
// PUT /api/users/{id}: on a path with a target in the URL, "the caller may only
// name themselves" is a check that has to be written and can be dropped, and
// here it is the shape of the route.
//
// IT DELIBERATELY REUSES NOTHING FROM handleUpdateUser. That handler decodes
// role alongside display_name, merges both against the stored row, writes them
// through UpdateUser (whose SET list includes role), and destroys every session
// belonging to the target when the effective role differs from the stored one.
// Every one of those behaviours is wrong here: a rename must not be able to
// move a privilege, and it must not sign anyone out. Sharing any of that
// machinery would mean this path carries a role value it has no business
// holding, one merge bug away from a silent promotion. UpdateUserDisplayName
// writes exactly one column, so the narrow field set is enforced by the SQL.
//
// NOTHING IS REVOKED. Contrast runPasswordResetCascade, which deletes sessions,
// revokes API tokens and drops push subscriptions: a rename is not a credential
// change, so no session, token or subscription may be touched by it. Pinned by
// TestUpdateMe_DoesNotRunThePasswordCascade.
func (h *Handler) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// The decoded shape is the field allowlist. json.Decode leaves unknown keys
	// alone (decodeJSON does not DisallowUnknownFields), so a body carrying
	// "role", "username" or "password" is parsed and those keys go nowhere —
	// there is no variable for them to land in. TestUpdateMe_IgnoresOtherFields
	// asserts that from the outside, so adding a field here fails a test rather
	// than quietly widening what a member may write.
	var req struct {
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Trim before validating, as handleRegister, handleCreateUser and
	// handleUpdateUser all do, so "   " is the same input as "" on every path
	// that writes this column. Unlike those three an empty value has nowhere to
	// fall back to — there is no username to default to and no stored name to
	// keep, because the whole request is the new name — so it is refused.
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}
	// LENGTH AND CONTENT, through the one validator the other three write paths
	// also call — see display_name.go for the per-codepoint-class reasoning.
	//
	// The length half is CHARACTERS via charLen, with the same wording as the
	// three paths above. len() would refuse an Arabic name at about half the cap
	// it advertises, and this household writes Arabic alongside English — see
	// charLen's comment in limits.go. Pinned at the boundary by
	// TestHandleUpdateMe_DisplayNameLimitIsCharacters.
	//
	// The content half matters most HERE of the four. This is the only path a
	// member can reach unaided, and it is the path a name arrives on by paste —
	// so it is where a crafted name would be introduced, and also where a
	// pre-existing one gets replaced, since the whole request is the new name.
	if err := validateDisplayName(req.DisplayName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// One statement, so no transaction: there is nothing to keep in step with
	// it. The paired-write paths in this package (the password cascade, the
	// transaction store) open one because a second row has to commit or roll
	// back with the first; a single UPDATE is already atomic, and users
	// mutations are not audited anywhere in this codebase — transaction_audit
	// and api_token_audit are the only audit tables, and neither has a users
	// counterpart. Nothing is invented here to fill that gap.
	updated, err := h.queries.UpdateUserDisplayName(r.Context(), database.UpdateUserDisplayNameParams{
		DisplayName: req.DisplayName,
		ID:          user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		// The account was deleted between authenticating and this write. The
		// RETURNING clause is what surfaces it; a bare UPDATE would have
		// reported a rename that touched nothing.
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update display name")
		return
	}

	// The same body GET /api/auth/me returns, so a caller can put the response
	// straight back into whatever it cached from that endpoint. toUserResponse
	// strips the password hash.
	writeJSON(w, http.StatusOK, toUserResponse(updated))
}
