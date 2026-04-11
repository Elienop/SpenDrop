package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// savingsGoalRequest is the JSON input for upserting a savings goal.
type savingsGoalRequest struct {
	TargetAmount float64 `json:"target_amount"`
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

	writeJSON(w, http.StatusOK, goals)
}

// handleSetSavingsGoal upserts a savings goal for a given year. Admin only.
func (h *Handler) handleSetSavingsGoal(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	yearStr := chi.URLParam(r, "year")
	year, err := strconv.ParseInt(yearStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid year")
		return
	}
	if year < 2000 || year > 2100 {
		writeError(w, http.StatusBadRequest, "year must be between 2000 and 2100")
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
	if req.TargetAmount > 1_000_000_000 {
		writeError(w, http.StatusBadRequest, "target_amount exceeds maximum allowed value")
		return
	}

	err = h.queries.UpsertSavingsGoal(r.Context(), database.UpsertSavingsGoalParams{
		Year:         year,
		TargetAmount: req.TargetAmount,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set savings goal")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
