package api

import (
	"math"
	"net/http"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// notificationSettingsDTO is the wire shape for /api/push/preferences. The
// large-transaction threshold crosses the edge in DOLLARS
// (large_txn_threshold_dollars), converted via centsToDollars on read /
// dollarsToCents on write — the raw large_txn_threshold_cents column is NEVER
// exposed (Money Wire-Edge DTO discipline; see savingsGoalDTO). The five
// toggles are plain bools the frontend reads directly.
type notificationSettingsDTO struct {
	OverBudget               bool      `json:"over_budget"`
	TxnAdded                 bool      `json:"txn_added"`
	TxnDeleted               bool      `json:"txn_deleted"`
	TxnEdited                bool      `json:"txn_edited"`
	LargeTxn                 bool      `json:"large_txn"`
	LargeTxnThresholdDollars float64   `json:"large_txn_threshold_dollars"`
	DigestMode               string    `json:"digest_mode"`
	QuietStart               string    `json:"quiet_start"`
	QuietEnd                 string    `json:"quiet_end"`
	QuietTz                  string    `json:"quiet_tz"`
	QuietAllowOverBudget     bool      `json:"quiet_allow_over_budget"`
	UpdatedAt                time.Time `json:"updated_at"`
}

func notificationSettingsToDTO(s database.NotificationSettings) notificationSettingsDTO {
	return notificationSettingsDTO{
		OverBudget:               s.OverBudget,
		TxnAdded:                 s.TxnAdded,
		TxnDeleted:               s.TxnDeleted,
		TxnEdited:                s.TxnEdited,
		LargeTxn:                 s.LargeTxn,
		LargeTxnThresholdDollars: centsToDollars(s.LargeTxnThresholdCents),
		DigestMode:               s.DigestMode,
		QuietStart:               s.QuietStart,
		QuietEnd:                 s.QuietEnd,
		QuietTz:                  s.QuietTz,
		QuietAllowOverBudget:     s.QuietAllowOverBudget,
		UpdatedAt:                s.UpdatedAt,
	}
}

// updateNotificationSettingsRequest is the PUT body. The threshold is a dollar
// float; an absent toggle decodes as false, so the frontend always sends the
// full set (the hook in useNotificationPrefs merges partials before PUT).
type updateNotificationSettingsRequest struct {
	OverBudget               bool    `json:"over_budget"`
	TxnAdded                 bool    `json:"txn_added"`
	TxnDeleted               bool    `json:"txn_deleted"`
	TxnEdited                bool    `json:"txn_edited"`
	LargeTxn                 bool    `json:"large_txn"`
	LargeTxnThresholdDollars float64 `json:"large_txn_threshold_dollars"`
	DigestMode               string  `json:"digest_mode"`
	QuietStart               string  `json:"quiet_start"`
	QuietEnd                 string  `json:"quiet_end"`
	QuietTz                  string  `json:"quiet_tz"`
	QuietAllowOverBudget     bool    `json:"quiet_allow_over_budget"`
}

// handleGetNotificationSettings returns the household notification preferences.
// ANY authenticated user may READ (so non-admins can see what is enabled and
// why their device buzzes); only admins may write (see PUT).
func (h *Handler) handleGetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s, err := h.queries.GetNotificationSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read notification settings")
		return
	}
	writeJSON(w, http.StatusOK, notificationSettingsToDTO(s))
}

// handleUpdateNotificationSettings rewrites the household preferences. ADMIN
// ONLY (403 otherwise) — these are household-wide, so a member must not be able
// to mute everyone's alerts. The dollar threshold is validated >= 0 and
// converted to cents before storage; the response echoes the saved DTO.
func (h *Handler) handleUpdateNotificationSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var req updateNotificationSettingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Validate the dollar threshold the same way every other money input is
	// validated (see transaction_handlers.go / checkpoint_handlers.go): reject
	// NaN/Inf and bound it by MaxTransactionAmount. A lone `>= 0` check is
	// insufficient — a huge but finite value (e.g. 1e308) passes it yet
	// overflows int64 in dollarsToCents and stores a negative threshold.
	if math.IsNaN(req.LargeTxnThresholdDollars) || math.IsInf(req.LargeTxnThresholdDollars, 0) ||
		req.LargeTxnThresholdDollars < 0 || req.LargeTxnThresholdDollars > MaxTransactionAmount {
		writeError(w, http.StatusBadRequest, "large_txn_threshold_dollars must be between 0 and the maximum transaction amount")
		return
	}

	if req.DigestMode != "off" && req.DigestMode != "daily" {
		writeError(w, http.StatusBadRequest, "digest_mode must be 'off' or 'daily'")
		return
	}

	// Quiet-hours bounds: each is EITHER empty ("no boundary") OR a valid 24h
	// HH:MM. parseHHMM already enforces the HH:MM range, so an empty string is
	// the only extra case to allow through.
	if !validQuietBound(req.QuietStart) {
		writeError(w, http.StatusBadRequest, "quiet_start must be empty or a 24h HH:MM time")
		return
	}
	if !validQuietBound(req.QuietEnd) {
		writeError(w, http.StatusBadRequest, "quiet_end must be empty or a 24h HH:MM time")
		return
	}
	// quiet_tz: empty ("no zone") or a loadable IANA zone.
	if req.QuietTz != "" {
		if _, err := time.LoadLocation(req.QuietTz); err != nil {
			writeError(w, http.StatusBadRequest, "quiet_tz must be empty or a valid IANA time zone")
			return
		}
	}

	if err := h.queries.UpdateNotificationSettings(r.Context(), database.UpdateNotificationSettingsParams{
		OverBudget:             req.OverBudget,
		TxnAdded:               req.TxnAdded,
		TxnDeleted:             req.TxnDeleted,
		TxnEdited:              req.TxnEdited,
		LargeTxn:               req.LargeTxn,
		LargeTxnThresholdCents: dollarsToCents(req.LargeTxnThresholdDollars),
		DigestMode:             req.DigestMode,
		QuietStart:             req.QuietStart,
		QuietEnd:               req.QuietEnd,
		QuietTz:                req.QuietTz,
		QuietAllowOverBudget:   req.QuietAllowOverBudget,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update notification settings")
		return
	}

	s, err := h.queries.GetNotificationSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read notification settings")
		return
	}
	writeJSON(w, http.StatusOK, notificationSettingsToDTO(s))
}
