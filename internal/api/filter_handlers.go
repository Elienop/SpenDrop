package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

type savedFilterRequest struct {
	Name       string `json:"name"`
	FilterJSON string `json:"filter_json"`
}

func (h *Handler) handleListSavedFilters(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	filters, err := h.queries.ListSavedFilters(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list filters")
		return
	}

	writeJSON(w, http.StatusOK, filters)
}

func (h *Handler) handleCreateSavedFilter(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Limit filters per user
	existingFilters, err := h.queries.ListSavedFilters(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check existing filters")
		return
	}
	if len(existingFilters) >= MaxSavedFilters {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("too many saved filters (max %d)", MaxSavedFilters))
		return
	}

	var req savedFilterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if charLen(req.Name) > MaxFilterNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or less", MaxFilterNameLength))
		return
	}
	if req.FilterJSON == "" {
		writeError(w, http.StatusBadRequest, "filter_json is required")
		return
	}
	// BYTES, deliberately, and the message says so. Unlike the name above,
	// filter_json is not prose a human typed and counted — it is a serialized
	// payload the client assembled, so what needs bounding is the transport and
	// storage cost, which is a byte quantity. Every other text cap in this app
	// is in characters (see charLen in limits.go); this one is the exception,
	// and stating the unit in the message is what keeps it from looking like the
	// bug that pattern used to be.
	if len(req.FilterJSON) > MaxFilterJSONLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("filter_json must be %d bytes or less", MaxFilterJSONLength))
		return
	}
	if !json.Valid([]byte(req.FilterJSON)) {
		writeError(w, http.StatusBadRequest, "filter_json must be valid JSON")
		return
	}

	filter, err := h.queries.CreateSavedFilter(r.Context(), database.CreateSavedFilterParams{
		UserID:     user.ID,
		Name:       req.Name,
		FilterJson: req.FilterJSON,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create filter")
		return
	}

	writeJSON(w, http.StatusCreated, filter)
}

func (h *Handler) handleUpdateSavedFilter(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid filter ID")
		return
	}

	var req savedFilterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if charLen(req.Name) > MaxFilterNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or less", MaxFilterNameLength))
		return
	}
	if req.FilterJSON == "" {
		writeError(w, http.StatusBadRequest, "filter_json is required")
		return
	}
	// Bytes, for the same reason as the create path above.
	if len(req.FilterJSON) > MaxFilterJSONLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("filter_json must be %d bytes or less", MaxFilterJSONLength))
		return
	}
	if !json.Valid([]byte(req.FilterJSON)) {
		writeError(w, http.StatusBadRequest, "filter_json must be valid JSON")
		return
	}

	result, err := h.queries.UpdateSavedFilter(r.Context(), database.UpdateSavedFilterParams{
		Name:       req.Name,
		FilterJson: req.FilterJSON,
		ID:         id,
		UserID:     user.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update filter")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		writeError(w, http.StatusNotFound, "filter not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) handleDeleteSavedFilter(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid filter ID")
		return
	}

	result, err := h.queries.DeleteSavedFilter(r.Context(), database.DeleteSavedFilterParams{
		ID:     id,
		UserID: user.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete filter")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		writeError(w, http.StatusNotFound, "filter not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
