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

// categoryCreateRequest is the JSON input for creating a category.
type categoryCreateRequest struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	SortOrder int64  `json:"sort_order"`
}

// categoryUpdateRequest is the JSON input for updating a category.
type categoryUpdateRequest struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
}

// categoryPatchRequest is the JSON input for patching category active status.
type categoryPatchRequest struct {
	IsActive *bool `json:"is_active"`
}

// reorderItem represents a single category reorder entry.
type reorderItem struct {
	ID        int64 `json:"id"`
	SortOrder int64 `json:"sort_order"`
}

// handleListCategories returns all active categories, or all categories if
// include_inactive=true is passed as a query parameter.
func (h *Handler) handleListCategories(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var categories []database.Category
	var err error

	if r.URL.Query().Get("include_inactive") == "true" {
		categories, err = h.queries.ListAllCategories(r.Context())
	} else {
		categories, err = h.queries.ListActiveCategories(r.Context())
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list categories")
		return
	}

	writeJSON(w, http.StatusOK, categories)
}

// handleCreateCategory creates a new category. Admin only.
func (h *Handler) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var req categoryCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > MaxCategoryNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or less", MaxCategoryNameLength))
		return
	}
	if req.Type != CategoryTypeExpense && req.Type != CategoryTypeIncome {
		writeError(w, http.StatusBadRequest, "type must be 'expense' or 'income'")
		return
	}

	cat, err := h.queries.CreateCategory(r.Context(), database.CreateCategoryParams{
		Name:      req.Name,
		Type:      req.Type,
		SortOrder: req.SortOrder,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create category")
		return
	}

	writeJSON(w, http.StatusCreated, cat)
}

// handleUpdateCategory updates an existing category's name and icon.
// Admin only.
func (h *Handler) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid category ID")
		return
	}

	var req categoryUpdateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > MaxCategoryNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or less", MaxCategoryNameLength))
		return
	}
	if len(req.Icon) > MaxIconNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("icon must be %d characters or less", MaxIconNameLength))
		return
	}

	result, err := h.queries.UpdateCategory(r.Context(), database.UpdateCategoryParams{
		Name: req.Name,
		Icon: toNullString(req.Icon),
		ID:   id,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update category")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handlePatchCategory updates the is_active status of a category (soft delete).
// Admin only.
func (h *Handler) handlePatchCategory(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid category ID")
		return
	}

	var req categoryPatchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.IsActive == nil {
		writeError(w, http.StatusBadRequest, "is_active is required")
		return
	}

	result, err := h.queries.UpdateCategoryActive(r.Context(), database.UpdateCategoryActiveParams{
		IsActive: *req.IsActive,
		ID:       id,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update category")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleDeleteCategory permanently deletes a category. Admin only.
// Returns 409 Conflict if transactions reference the category (FK constraint).
func (h *Handler) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid category ID")
		return
	}

	result, err := h.queries.DeleteCategory(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			writeError(w, http.StatusConflict, "cannot delete category that has transactions — deactivate it instead, or reassign its transactions first")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete category")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleReorderCategories updates sort_order for multiple categories in a
// single transaction. Admin only.
func (h *Handler) handleReorderCategories(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var items []reorderItem
	if err := decodeJSON(w, r, &items); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "at least one item is required")
		return
	}
	if len(items) > MaxCategoryReorder {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many items (max %d)", MaxCategoryReorder))
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	qtx := h.queries.WithTx(tx)
	for _, item := range items {
		err := qtx.UpdateCategorySortOrder(r.Context(), database.UpdateCategorySortOrderParams{
			SortOrder: item.SortOrder,
			ID:        item.ID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update sort order")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit reorder")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "reordered"})
}
