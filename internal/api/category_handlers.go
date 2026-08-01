package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// categoryResponse is the wire shape for a category. Icon is a plain
// nullable string (JSON null when unset), NOT the Go sql.NullString object
// {"String":...,"Valid":...} that leaks if a database.Category is serialized
// directly. Rendering that object as a React child crashes the client
// (React error #31 — "objects are not valid as a React child"); this DTO
// mirrors the frontend Category type (icon: string | null).
type categoryResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Icon      *string   `json:"icon"`
	SortOrder int64     `json:"sort_order"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

func toCategoryResponse(c database.Category) categoryResponse {
	var icon *string
	if c.Icon.Valid {
		s := c.Icon.String
		icon = &s
	}
	return categoryResponse{
		ID:        c.ID,
		Name:      c.Name,
		Type:      c.Type,
		Icon:      icon,
		SortOrder: c.SortOrder,
		IsActive:  c.IsActive,
		CreatedAt: c.CreatedAt,
	}
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

	out := make([]categoryResponse, 0, len(categories))
	for _, c := range categories {
		out = append(out, toCategoryResponse(c))
	}
	writeJSON(w, http.StatusOK, out)
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
	if charLen(req.Name) > MaxCategoryNameLength {
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

	// Live-updates: a new category changes the category list and the
	// dashboard/reports category breakdowns on every open device.
	// Post-commit, best-effort, nil-safe — never affects the response.
	h.publishInvalidate("categories", "dashboard", "reports")

	writeJSON(w, http.StatusCreated, toCategoryResponse(cat))
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
	if charLen(req.Name) > MaxCategoryNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or less", MaxCategoryNameLength))
		return
	}
	if charLen(req.Icon) > MaxIconNameLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("icon must be %d characters or less", MaxIconNameLength))
		return
	}

	// The rename and the content_hash clear it triggers MUST share one SQL
	// transaction. If the rename committed and the clear did not, every
	// transaction in the category would be left holding a digest computed from
	// the OLD name — the exact permanently-stale state this pairing exists to
	// prevent, and one the startup backfill cannot heal (it only ever
	// processes rows WHERE content_hash IS NULL).
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update category")
		return
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds
	qtx := h.queries.WithTx(tx)

	// Read the prior row inside the tx: it supplies both the 404 signal and
	// the OLD name the change detector needs. Reading it outside would race
	// with a concurrent rename.
	before, err := qtx.GetCategoryByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update category")
		return
	}

	if _, err := qtx.UpdateCategory(r.Context(), database.UpdateCategoryParams{
		Name: req.Name,
		Icon: toNullString(req.Icon),
		ID:   id,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update category")
		return
	}

	// database.ComputeContentHash mixes in the category NAME, so a rename
	// moves the dedupe identity of every transaction filed under it. Clear
	// those rows rather than recompute: recomputation would have to resolve
	// collisions against the partial unique index (legitimate same-hash rows
	// exist in real household data), which is exactly what the boot backfill
	// already implements with earliest-id-wins. A cleared row simply leaves
	// dedupe until the next boot re-anchors it under the new name.
	//
	// Gated on an ACTUAL name change, judged with the hash function's own
	// normalization — this endpoint is a full replace of {name, icon}, so the
	// settings UI resends the current name on every icon edit, and clearing
	// there would de-anchor the whole category for nothing.
	if database.CategoryRenameChangesHashes(before.Name, req.Name) {
		if err := qtx.ClearContentHashForCategory(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update category")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit category update")
		return
	}

	// Live-updates: a renamed/re-iconed category changes the category list
	// and dashboard/reports labels. Post-commit, best-effort, nil-safe.
	h.publishInvalidate("categories", "dashboard", "reports")

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

	// Live-updates: toggling is_active adds/removes the category from the
	// active list and the dashboard/reports breakdowns. Post-commit,
	// best-effort, nil-safe.
	h.publishInvalidate("categories", "dashboard", "reports")

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

	// Live-updates: a hard-deleted category drops out of the list and the
	// dashboard/reports breakdowns. Post-commit, best-effort, nil-safe.
	h.publishInvalidate("categories", "dashboard", "reports")

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

	// Live-updates: reordering changes the category sort order shown in the
	// list and dashboard/reports. One signal for the whole batch.
	// Post-commit, best-effort, nil-safe.
	h.publishInvalidate("categories", "dashboard", "reports")

	writeJSON(w, http.StatusOK, map[string]string{"status": "reordered"})
}
