package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// savingsGoalRequest is the JSON input for upserting a savings goal.
type savingsGoalRequest struct {
	TargetAmount float64 `json:"target_amount"`
}

// savingsGoalDTO is the wire shape for GET /api/savings-goals: `target_amount`
// in DOLLARS, never the raw target_amount_cents column. The legacy REAL
// target_amount column was dropped in migration 010, so returning the
// database.SavingsGoal row directly leaked target_amount_cents under the wrong
// key — the frontend expects `target_amount` (dollars) like every other money
// endpoint, so goal.target_amount came back undefined and fmt(undefined)
// crashed the Reports → Savings tab and the Settings savings-goals panel.
// Mirrors budgetDTO / categoryBudgetDTO.
type savingsGoalDTO struct {
	ID           int64     `json:"id"`
	Year         int64     `json:"year"`
	TargetAmount float64   `json:"target_amount"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// handleGetSavingsGoals returns all savings goals.
func (h *Handler) handleGetSavingsGoals(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	goals, err := h.queries.ListSavingsGoals(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list savings goals")
		return
	}

	out := make([]savingsGoalDTO, 0, len(goals))
	for _, g := range goals {
		out = append(out, savingsGoalDTO{
			ID:           g.ID,
			Year:         g.Year,
			TargetAmount: centsToDollars(g.TargetAmountCents),
			UpdatedAt:    g.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// handleSetSavingsGoal upserts a savings goal for a given year. Admin only.
func (h *Handler) handleSetSavingsGoal(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	yearStr := chi.URLParam(r, "year")
	year, err := strconv.ParseInt(yearStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid year")
		return
	}
	if year < MinYear || year > MaxYear {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("year must be between %d and %d", MinYear, MaxYear))
		return
	}

	var req savingsGoalRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TargetAmount < 0 {
		writeError(w, http.StatusBadRequest, "target_amount must not be negative")
		return
	}
	if req.TargetAmount > MaxTransactionAmount {
		writeError(w, http.StatusBadRequest, "target_amount exceeds maximum allowed value")
		return
	}

	// Phase 3.1b: the legacy REAL target_amount column was dropped in
	// migration 010; only target_amount_cents is written. The cents value is
	// derived once from the client-supplied float at the wire edge.
	err = h.queries.UpsertSavingsGoal(r.Context(), database.UpsertSavingsGoalParams{
		Year:              year,
		TargetAmountCents: dollarsToCents(req.TargetAmount),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set savings goal")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
