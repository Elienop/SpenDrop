package api

import (
	"database/sql"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

var currencyCodeRegexp = regexp.MustCompile(`^[A-Z]{3}$`)

// currencyCreateRequest is the JSON input for creating a currency.
type currencyCreateRequest struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Symbol     string  `json:"symbol"`
	RateToBase float64 `json:"rate_to_base"`
}

// currencyUpdateRequest is the JSON input for updating a currency rate.
type currencyUpdateRequest struct {
	RateToBase float64 `json:"rate_to_base"`
}

// handleListCurrencies returns all currencies.
func (h *Handler) handleListCurrencies(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	currencies, err := h.queries.ListCurrencies(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list currencies")
		return
	}

	writeJSON(w, http.StatusOK, currencies)
}

// handleCreateCurrency creates (or upserts) a new currency. Admin only.
func (h *Handler) handleCreateCurrency(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var req currencyCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}
	if !currencyCodeRegexp.MatchString(req.Code) {
		writeError(w, http.StatusBadRequest, "code must be exactly 3 uppercase letters (e.g., USD)")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 100 {
		writeError(w, http.StatusBadRequest, "name must be 100 characters or less")
		return
	}
	if req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol is required")
		return
	}
	if len(req.Symbol) > 10 {
		writeError(w, http.StatusBadRequest, "symbol must be 10 characters or less")
		return
	}
	if req.RateToBase <= 0 {
		writeError(w, http.StatusBadRequest, "rate_to_base must be positive")
		return
	}

	// Prevent overwriting the base currency (would demote it to non-base)
	existing, existErr := h.queries.GetCurrency(r.Context(), req.Code)
	if existErr == nil && existing.IsBase {
		writeError(w, http.StatusConflict, "cannot overwrite the base currency")
		return
	}

	err := h.queries.UpsertCurrency(r.Context(), database.UpsertCurrencyParams{
		Code:       req.Code,
		Name:       req.Name,
		Symbol:     req.Symbol,
		RateToBase: req.RateToBase,
		IsBase:     false,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create currency")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"code":         req.Code,
		"name":         req.Name,
		"symbol":       req.Symbol,
		"rate_to_base": req.RateToBase,
		"is_base":      false,
	})
}

// handleUpdateCurrency updates the exchange rate for an existing currency.
// Admin only.
func (h *Handler) handleUpdateCurrency(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	code := chi.URLParam(r, "code")

	// Look up existing currency
	existing, err := h.queries.GetCurrency(r.Context(), code)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "currency not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get currency")
		return
	}

	if existing.IsBase {
		writeError(w, http.StatusBadRequest, "cannot modify the base currency rate")
		return
	}

	var req currencyUpdateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RateToBase <= 0 {
		writeError(w, http.StatusBadRequest, "rate_to_base must be positive")
		return
	}

	// Upsert with updated rate, keeping other fields the same
	err = h.queries.UpsertCurrency(r.Context(), database.UpsertCurrencyParams{
		Code:       existing.Code,
		Name:       existing.Name,
		Symbol:     existing.Symbol,
		RateToBase: req.RateToBase,
		IsBase:     existing.IsBase,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update currency")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
