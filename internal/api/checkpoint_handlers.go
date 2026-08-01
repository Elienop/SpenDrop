package api

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// Phase 3.3 — Balance checkpoints (Beancount-style).
//
// A checkpoint is a user-asserted claim of the form "on date D, the sum of
// <scope> equals Y cents", where <scope> is one of:
//
//   * total     — the whole-ledger sum up to date D
//   * category  — the per-category sum up to date D
//   * tag       — the per-tag sum (CSV token match) up to date D
//
// The handler stores the assertion in cents (Phase 3.1a discipline), runs a
// single SUM over t.amount_cents filtered on t.deleted_at IS NULL (Phase 2.1
// discipline), and writes the outcome back to last_verification_status
// ('ok' or 'mismatch'). Mismatches are a user-visible *signal*, not a
// failure: the endpoint always returns 200/201 so the user can fix the
// stored value, the underlying transactions, or both.
//
// The wire shape keeps amounts as dollar float64 (matching every other
// endpoint in this package) and converts cents<->dollars exactly once at
// the edge via cents.go helpers. All internal arithmetic is int64.
//
// This file also exposes verifyAffectedCheckpoints, the helper used by the
// post-mutation and post-import hooks in Phase 3.3 step 2. See the comment
// on that function for the contract around error handling (log, never
// abort the enclosing mutation).

// checkpointRequest is the JSON input for POST /api/checkpoints.
//
// expected_amount is a dollar float64 on the wire; the handler converts it
// to int64 cents before inserting so every on-disk comparison is exact.
// scope_id and scope_label are set to the zero value when scope_type does
// not use them, and scope_type is validated before either field is read so
// a malformed request never leaks a stale field into the insert.
type checkpointRequest struct {
	ScopeType      string  `json:"scope_type"`
	ScopeID        int64   `json:"scope_id"`
	ScopeLabel     string  `json:"scope_label"`
	Date           string  `json:"date"`
	ExpectedAmount float64 `json:"expected_amount"`
	Note           string  `json:"note"`
}

// checkpointResponse is the JSON output for a single checkpoint. Every
// nullable column from the DB is unwrapped to an omitempty field so the
// client sees a flat shape. actual_amount is populated by the endpoints
// that run a verification inline (create / verify); it is omitted from
// list responses because ListCheckpoints does not re-run the SUM per row.
type checkpointResponse struct {
	ID                     int64    `json:"id"`
	UserID                 int64    `json:"user_id"`
	ScopeType              string   `json:"scope_type"`
	ScopeID                *int64   `json:"scope_id,omitempty"`
	ScopeLabel             string   `json:"scope_label,omitempty"`
	Date                   string   `json:"date"`
	ExpectedAmount         float64  `json:"expected_amount"`
	ActualAmount           *float64 `json:"actual_amount,omitempty"`
	Note                   string   `json:"note,omitempty"`
	CreatedAt              string   `json:"created_at"`
	LastVerifiedAt         string   `json:"last_verified_at,omitempty"`
	LastVerificationStatus string   `json:"last_verification_status,omitempty"`
}

// toCheckpointResponse converts a database.BalanceCheckpoint row into the
// JSON wire shape. The ActualAmount field is left nil — call sites that
// just finished running a verification overwrite it via
// withActualCents below.
func toCheckpointResponse(c database.BalanceCheckpoint) checkpointResponse {
	resp := checkpointResponse{
		ID:             c.ID,
		UserID:         c.UserID,
		ScopeType:      c.ScopeType,
		Date:           c.Date.Format("2006-01-02"),
		ExpectedAmount: centsToDollars(c.ExpectedAmountCents),
		CreatedAt:      c.CreatedAt.Format(time.RFC3339),
	}
	if c.ScopeID.Valid {
		id := c.ScopeID.Int64
		resp.ScopeID = &id
	}
	if c.ScopeLabel.Valid {
		resp.ScopeLabel = c.ScopeLabel.String
	}
	if c.Note.Valid {
		resp.Note = c.Note.String
	}
	if c.LastVerifiedAt.Valid {
		resp.LastVerifiedAt = c.LastVerifiedAt.Time.Format(time.RFC3339)
	}
	if c.LastVerificationStatus.Valid {
		resp.LastVerificationStatus = c.LastVerificationStatus.String
	}
	return resp
}

// withActualCents copies the verified actual sum onto the response as a
// dollar float64, so POST /create and POST /verify can surface it.
// Returned by value so callers chain it inline with toCheckpointResponse.
func withActualCents(resp checkpointResponse, actualCents int64) checkpointResponse {
	amt := centsToDollars(actualCents)
	resp.ActualAmount = &amt
	return resp
}

// validateCheckpointRequest runs the shape checks that do not require a DB
// round-trip: scope enum, date format, amount range, note length. Scope
// cross-field rules (category needs scope_id, tag needs scope_label) are
// enforced here too so the handler never constructs a half-filled
// CreateCheckpointParams.
func validateCheckpointRequest(req checkpointRequest) error {
	switch req.ScopeType {
	case "total", "category", "tag":
	default:
		return fmt.Errorf("scope_type must be one of total, category, tag")
	}

	if req.Date == "" {
		return fmt.Errorf("date is required")
	}
	if _, err := time.Parse("2006-01-02", req.Date); err != nil {
		return fmt.Errorf("date must be in YYYY-MM-DD format")
	}

	if math.IsNaN(req.ExpectedAmount) || math.IsInf(req.ExpectedAmount, 0) {
		return fmt.Errorf("expected_amount must be a finite number")
	}
	// Checkpoints can legitimately be negative (net worth after expenses,
	// or a category with refunds exceeding spends), so we only cap on the
	// magnitude. MaxTransactionAmount is the same cap every other money
	// field in this package uses.
	if math.Abs(req.ExpectedAmount) > MaxTransactionAmount {
		return fmt.Errorf("expected_amount exceeds maximum allowed value")
	}

	if req.ScopeType == "category" && req.ScopeID <= 0 {
		return fmt.Errorf("scope_id is required for category scope")
	}
	if req.ScopeType == "tag" {
		label := strings.TrimSpace(req.ScopeLabel)
		if label == "" {
			return fmt.Errorf("scope_label is required for tag scope")
		}
		// Reject embedded commas — tag match uses ',' as the delimiter
		// of the CSV-encoded transactions.tags column, so a tag that
		// contains a comma would silently match substrings of other
		// tags. See the migration 007 header comment for the full
		// rationale on the CSV-token contract.
		if strings.ContainsRune(label, ',') {
			return fmt.Errorf("scope_label must not contain a comma")
		}
	}

	if charLen(req.Note) > MaxNotesLength {
		return fmt.Errorf("note must be %d characters or less", MaxNotesLength)
	}
	return nil
}

// verifyCheckpointOnce re-runs the verification SUM for a single checkpoint
// against live transactions, writes the resulting status back to the row,
// and returns (actual_cents, status). Called by handleCreateCheckpoint
// (post-insert), handleVerifyCheckpoint (manual re-verify), and
// verifyAffectedCheckpoints (the post-mutation hook loop).
//
// The scope fields are pulled from the checkpoint row itself so the caller
// never has to re-marshal them from the HTTP request — this makes the
// post-mutation hook a straight "load rows, call this per row" loop with
// no request context plumbing.
func (h *Handler) verifyCheckpointOnce(ctx context.Context, cp database.BalanceCheckpoint) (actualCents int64, status string, err error) {
	params := database.VerifyCheckpointTotalParams{
		Date:      cp.Date.Format("2006-01-02"),
		ScopeType: cp.ScopeType,
	}
	if cp.ScopeID.Valid {
		params.ScopeID = cp.ScopeID.Int64
	}
	if cp.ScopeLabel.Valid {
		params.ScopeLabel = cp.ScopeLabel.String
	}

	actual, err := h.queries.VerifyCheckpointTotal(ctx, params)
	if err != nil {
		return 0, "", fmt.Errorf("verify checkpoint %d: %w", cp.ID, err)
	}

	// Exact int-vs-int comparison. No tolerance. Phase 3.1a promise.
	status = "mismatch"
	if actual == cp.ExpectedAmountCents {
		status = "ok"
	}

	if err := h.queries.UpdateCheckpointVerification(ctx, database.UpdateCheckpointVerificationParams{
		LastVerificationStatus: sql.NullString{String: status, Valid: true},
		ID:                     cp.ID,
	}); err != nil {
		return actual, status, fmt.Errorf("update verification id=%d: %w", cp.ID, err)
	}
	return actual, status, nil
}

// earliestDate returns the earlier of the two times, treating a zero
// time.Time as "unset" so a loop accumulator starts empty and the first
// real date seen initializes it. Used by batch hooks
// (handleBatchCreateTransactions, handleBatchDeleteTransactions,
// handleImportConfirm) to compute the minimum transaction date across a
// successful run without carrying a separate "found any" bool flag.
func earliestDate(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.Before(b) {
		return a
	}
	return b
}

// verifyAffectedCheckpoints is the Phase 3.3 post-mutation hook. Callers
// pass the earliest affected date (the transaction date for create/delete,
// or the MIN(old_date, new_date) for an update) and this helper re-verifies
// every checkpoint on or after that date. Checkpoints earlier than fromDate
// cannot be affected by a transaction whose date is on or after fromDate,
// so the index on balance_checkpoints.date makes this a cheap lookup even
// on large checkpoint tables.
//
// Passing a zero time.Time is legal and means "re-verify every checkpoint"
// — handleDeleteTransactionsByFilter uses this because its bulk UPDATE does
// not expose individual row dates, so the conservative "reverify all"
// fallback is the only correct choice.
//
// Error contract: THIS FUNCTION NEVER RETURNS AN ERROR. The caller's
// mutation is already committed at the point of the call — a mismatch is
// the intended outcome, not a failure, and a transient DB error reading or
// updating a checkpoint row must not roll back (or retroactively error out
// of) a committed transaction mutation. All errors are logged and the loop
// continues so one bad row cannot block the rest. Callers invoke this as a
// best-effort hook after the mutation has already succeeded.
func (h *Handler) verifyAffectedCheckpoints(ctx context.Context, fromDate time.Time) {
	affected, err := h.queries.ListCheckpointsAffectedByDate(ctx, fromDate.Format("2006-01-02"))
	if err != nil {
		log.Printf("checkpoint hook: list affected from=%s: %v", fromDate.Format("2006-01-02"), err)
		return
	}
	for _, cp := range affected {
		if _, _, err := h.verifyCheckpointOnce(ctx, cp); err != nil {
			log.Printf("checkpoint hook: %v", err)
			// keep going — one transient failure must not skip the rest
		}
	}
}

// handleCreateCheckpoint accepts a POST body describing a new checkpoint,
// inserts it with status='pending', runs the verification inline, and
// returns 201 with the resulting row (including the computed
// actual_amount and the freshly-written status).
//
// Verification runs SYNCHRONOUSLY on create because the UX expectation is
// that the user sees the outcome immediately — a "pending" forever row
// would just be a worse version of a manual verify button. If the
// verification step itself errors out (transient DB issue), the
// checkpoint row still exists with status='pending' and the user's
// next interaction with the /verify endpoint will re-run it.
func (h *Handler) handleCreateCheckpoint(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req checkpointRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateCheckpointRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// For category scope, short-circuit the FK violation into a clean 400
	// so the user gets "unknown category" instead of "failed to create".
	if req.ScopeType == "category" {
		if _, err := h.queries.GetCategoryByID(r.Context(), req.ScopeID); err != nil {
			if err == sql.ErrNoRows {
				writeError(w, http.StatusBadRequest, "unknown category")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to look up category")
			return
		}
	}

	date, _ := time.Parse("2006-01-02", req.Date) // already validated

	// safeDollarsToCents, not dollarsToCents. validateCheckpointRequest already
	// rejects NaN/Inf and bounds the magnitude, so this is defence in depth —
	// but it is the conversion, not the validator, that turns an unrepresentable
	// float into int64 minimum, and the guard belongs where the damage is done.
	expectedCents, ok := safeDollarsToCents(req.ExpectedAmount)
	if !ok {
		writeError(w, http.StatusBadRequest, "expected_amount is not a representable money value")
		return
	}

	params := database.CreateCheckpointParams{
		UserID:              user.ID,
		ScopeType:           req.ScopeType,
		Date:                date,
		ExpectedAmountCents: expectedCents,
		Note:                toNullString(req.Note),
	}
	if req.ScopeType == "category" {
		params.ScopeID = sql.NullInt64{Int64: req.ScopeID, Valid: true}
	}
	if req.ScopeType == "tag" {
		params.ScopeLabel = sql.NullString{String: strings.TrimSpace(req.ScopeLabel), Valid: true}
	}

	cp, err := h.queries.CreateCheckpoint(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create checkpoint")
		return
	}

	// Inline verification. Any error here just means the row stays in
	// its default 'pending' state; the user can hit POST /verify later.
	// We still log it because a recurring failure would point at a
	// broader DB problem the operator needs to see.
	actual, status, verr := h.verifyCheckpointOnce(r.Context(), cp)
	if verr != nil {
		log.Printf("checkpoint create: inline verify id=%d: %v", cp.ID, verr)
		// Return the row as-inserted (pending) — the UX still works,
		// the user just sees "pending" and a re-verify button.
		writeJSON(w, http.StatusCreated, toCheckpointResponse(cp))
		return
	}

	// Patch the in-memory row so the response reflects the fresh status
	// without a second round-trip to re-read. last_verified_at is set
	// by the UPDATE's CURRENT_TIMESTAMP, which we approximate here with
	// time.Now(); the next GET will read the exact DB value.
	cp.LastVerificationStatus = sql.NullString{String: status, Valid: true}
	cp.LastVerifiedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}

	writeJSON(w, http.StatusCreated, withActualCents(toCheckpointResponse(cp), actual))
}

// handleListCheckpoints returns every checkpoint in the household (shared
// visibility, same model as transactions and categories). Checkpoints are
// user-attributed via user_id for delete ownership, but the list itself
// is household-wide so anyone can see what other members have asserted.
// No per-row verification is run here — callers that need a fresh status
// hit POST /api/checkpoints/{id}/verify instead, and the hook loop runs
// after every mutation so the stored status should already be current.
func (h *Handler) handleListCheckpoints(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rows, err := h.queries.ListCheckpoints(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list checkpoints")
		return
	}

	items := make([]checkpointResponse, 0, len(rows))
	for _, cp := range rows {
		items = append(items, toCheckpointResponse(cp))
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkpoints": items})
}

// handleDeleteCheckpoint deletes a checkpoint by ID. Ownership: the
// creator may delete their own; admins may delete any. Returns 404 for
// an unknown ID (same behaviour as the transaction endpoints, so clients
// that stale-cache an ID get a consistent signal).
func (h *Handler) handleDeleteCheckpoint(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid checkpoint ID")
		return
	}

	existing, err := h.queries.GetCheckpoint(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "checkpoint not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to look up checkpoint")
		return
	}
	if user.Role != RoleAdmin && existing.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if _, err := h.queries.DeleteCheckpoint(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete checkpoint")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleVerifyCheckpoint runs a fresh verification for a single checkpoint
// on demand, updates the stored status, and returns the row with the new
// actual_amount and status. Any authenticated household member can call
// this — verification is a read-heavy no-side-effect operation apart from
// the small UPDATE to the checkpoint's status columns.
//
// This is the manual re-verify button. Contrast with the post-mutation
// hook (verifyAffectedCheckpoints) which fires automatically after every
// transaction CRUD and import confirm.
func (h *Handler) handleVerifyCheckpoint(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid checkpoint ID")
		return
	}

	cp, err := h.queries.GetCheckpoint(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "checkpoint not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to look up checkpoint")
		return
	}

	actual, status, verr := h.verifyCheckpointOnce(r.Context(), cp)
	if verr != nil {
		log.Printf("checkpoint manual verify id=%d: %v", cp.ID, verr)
		writeError(w, http.StatusInternalServerError, "failed to verify checkpoint")
		return
	}
	cp.LastVerificationStatus = sql.NullString{String: status, Valid: true}
	cp.LastVerifiedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}

	writeJSON(w, http.StatusOK, withActualCents(toCheckpointResponse(cp), actual))
}
