package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// --- handleListCategories ---

func TestHandleListCategories_ReturnsActiveCategories(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/categories", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	// Seed data has 19 categories, all active
	if len(resp) < 1 {
		t.Error("expected at least 1 category")
	}
	// All returned should be active
	for _, cat := range resp {
		if cat["is_active"] != true {
			t.Errorf("expected is_active=true for category %v", cat["name"])
		}
	}
}

// Regression: icon must serialize as a plain JSON string (or null), never the
// Go sql.NullString object {"String":...,"Valid":...}. Rendering that object as
// a React child crashes the client with React error #31 — which is exactly
// what the new CategoryLimitsSection (the first UI to render cat.icon) hit.
func TestHandleListCategories_IconIsPlainStringNotNullStringObject(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Seed categories have NULL icons; set one so the test also exercises the
	// Valid→string path. (A NULL NullString still serializes to the
	// {"String":"","Valid":false} object, so the null-icon rows alone already
	// prove the regression — but a real icon pins the happy path too.)
	if _, err := db.Exec("UPDATE categories SET icon = '🛒' WHERE id = 1"); err != nil {
		t.Fatalf("set icon on category 1: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/categories", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleListCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	if len(resp) == 0 {
		t.Fatal("expected seeded categories")
	}

	sawSetIcon := false
	for _, cat := range resp {
		icon, ok := cat["icon"]
		if !ok {
			t.Fatalf("category %v missing icon field", cat["name"])
		}
		if icon == nil {
			continue // null = no icon set, acceptable
		}
		if _, isObject := icon.(map[string]any); isObject {
			t.Fatalf("icon leaked as a sql.NullString object for %v: %v", cat["name"], icon)
		}
		s, isString := icon.(string)
		if !isString {
			t.Fatalf("icon should be a string, got %T (%v) for %v", icon, icon, cat["name"])
		}
		if s == "🛒" {
			sawSetIcon = true
		}
	}
	if !sawSetIcon {
		t.Error("expected category id=1 to return its icon as the string \"🛒\"")
	}
}

func TestHandleListCategories_IncludeInactive(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	// Deactivate a category
	_, err := q.UpdateCategoryActive(context.Background(), database.UpdateCategoryActiveParams{
		IsActive: false,
		ID:       1,
	})
	if err != nil {
		t.Fatalf("deactivate category: %v", err)
	}

	// Without include_inactive: should exclude the deactivated one
	req := httptest.NewRequest(http.MethodGet, "/api/categories", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleListCategories(rec, req)

	var active []map[string]any
	decodeResponse(t, rec, &active)

	// With include_inactive=true: should include all
	req2 := httptest.NewRequest(http.MethodGet, "/api/categories?include_inactive=true", nil)
	req2 = withUser(req2, user)
	rec2 := httptest.NewRecorder()
	h.handleListCategories(rec2, req2)

	var all []map[string]any
	decodeResponse(t, rec2, &all)

	if len(all) <= len(active) {
		t.Errorf("expected include_inactive to return more categories: all=%d, active=%d", len(all), len(active))
	}
}

func TestHandleListCategories_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/categories", nil)
	rec := httptest.NewRecorder()

	h.handleListCategories(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleCreateCategory ---

func TestHandleCreateCategory_AdminCanCreate(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"name":"TestCat","type":"expense","sort_order":99}`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateCategory(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["name"] != "TestCat" {
		t.Errorf("expected name 'TestCat', got %v", resp["name"])
	}
	if resp["type"] != "expense" {
		t.Errorf("expected type 'expense', got %v", resp["type"])
	}
}

func TestHandleCreateCategory_MemberForbidden(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"name":"TestCat","type":"expense","sort_order":99}`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories", body)
	req = withUser(req, member)
	rec := httptest.NewRecorder()

	h.handleCreateCategory(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleCreateCategory_MissingName_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"type":"expense","sort_order":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateCategory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateCategory_InvalidType_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"name":"TestCat","type":"invalid","sort_order":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateCategory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateCategory_InvalidJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateCategory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleUpdateCategory ---

func TestHandleUpdateCategory_AdminCanUpdate(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"name":"Updated Food"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/categories/1", body)
	req = withUserAndURLParam(req, admin, "id", "1")
	rec := httptest.NewRecorder()

	h.handleUpdateCategory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateCategory_MemberForbidden(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"name":"Hacked"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/categories/1", body)
	req = withUserAndURLParam(req, member, "id", "1")
	rec := httptest.NewRecorder()

	h.handleUpdateCategory(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleUpdateCategory_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"name":"X"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/categories/abc", body)
	req = withUserAndURLParam(req, admin, "id", "abc")
	rec := httptest.NewRecorder()

	h.handleUpdateCategory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleUpdateCategory_MissingName_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPut, "/api/categories/1", body)
	req = withUserAndURLParam(req, admin, "id", "1")
	rec := httptest.NewRecorder()

	h.handleUpdateCategory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// --- handlePatchCategory ---

func TestHandlePatchCategory_AdminCanDeactivate(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"is_active":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/categories/1", body)
	req = withUserAndURLParam(req, admin, "id", "1")
	rec := httptest.NewRecorder()

	h.handlePatchCategory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify it's actually deactivated
	cat, err := q.GetCategoryByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("get category: %v", err)
	}
	if cat.IsActive {
		t.Error("expected category to be inactive")
	}
}

func TestHandlePatchCategory_AdminCanReactivate(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// First deactivate
	_, err := q.UpdateCategoryActive(context.Background(), database.UpdateCategoryActiveParams{
		IsActive: false,
		ID:       1,
	})
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// Then reactivate via handler
	body := strings.NewReader(`{"is_active":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/categories/1", body)
	req = withUserAndURLParam(req, admin, "id", "1")
	rec := httptest.NewRecorder()

	h.handlePatchCategory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	cat, err := q.GetCategoryByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("get category: %v", err)
	}
	if !cat.IsActive {
		t.Error("expected category to be active")
	}
}

func TestHandlePatchCategory_MemberForbidden(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"is_active":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/categories/1", body)
	req = withUserAndURLParam(req, member, "id", "1")
	rec := httptest.NewRecorder()

	h.handlePatchCategory(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandlePatchCategory_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"is_active":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/categories/abc", body)
	req = withUserAndURLParam(req, admin, "id", "abc")
	rec := httptest.NewRecorder()

	h.handlePatchCategory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleReorderCategories ---

func TestHandleReorderCategories_AdminCanReorder(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`[{"id":1,"sort_order":10},{"id":2,"sort_order":20}]`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories/reorder", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleReorderCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify sort orders are updated
	cat1, _ := q.GetCategoryByID(context.Background(), 1)
	cat2, _ := q.GetCategoryByID(context.Background(), 2)
	if cat1.SortOrder != 10 {
		t.Errorf("expected cat1 sort_order=10, got %d", cat1.SortOrder)
	}
	if cat2.SortOrder != 20 {
		t.Errorf("expected cat2 sort_order=20, got %d", cat2.SortOrder)
	}
}

func TestHandleReorderCategories_MemberForbidden(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`[{"id":1,"sort_order":10}]`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories/reorder", body)
	req = withUser(req, member)
	rec := httptest.NewRecorder()

	h.handleReorderCategories(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleReorderCategories_EmptyArray_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`[]`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories/reorder", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleReorderCategories(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleReorderCategories_InvalidJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/categories/reorder", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleReorderCategories(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleDeleteCategory ---

func TestHandleDeleteCategory_AdminCanDelete(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Create a category to delete
	cat, err := q.CreateCategory(context.Background(), database.CreateCategoryParams{
		Name:      "ToDelete",
		Type:      "expense",
		SortOrder: 99,
	})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	idStr := fmt.Sprintf("%d", cat.ID)
	req := httptest.NewRequest(http.MethodDelete, "/api/categories/"+idStr, nil)
	req = withUserAndURLParam(req, admin, "id", idStr)
	rec := httptest.NewRecorder()

	h.handleDeleteCategory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if resp["status"] != "deleted" {
		t.Errorf("expected status 'deleted', got %q", resp["status"])
	}

	// Verify category is actually gone
	_, err = q.GetCategoryByID(context.Background(), cat.ID)
	if err == nil {
		t.Error("expected category to be deleted, but it still exists")
	}
}

func TestHandleDeleteCategory_MemberForbidden(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodDelete, "/api/categories/1", nil)
	req = withUserAndURLParam(req, member, "id", "1")
	rec := httptest.NewRecorder()

	h.handleDeleteCategory(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleDeleteCategory_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	req := httptest.NewRequest(http.MethodDelete, "/api/categories/abc", nil)
	req = withUserAndURLParam(req, admin, "id", "abc")
	rec := httptest.NewRecorder()

	h.handleDeleteCategory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDeleteCategory_NotFound_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	req := httptest.NewRequest(http.MethodDelete, "/api/categories/99999", nil)
	req = withUserAndURLParam(req, admin, "id", "99999")
	rec := httptest.NewRecorder()

	h.handleDeleteCategory(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteCategory_WithTransactions_Returns409(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Enable foreign keys (test DSN doesn't include _foreign_keys=on)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	// Create a category and a transaction referencing it
	cat, err := q.CreateCategory(context.Background(), database.CreateCategoryParams{
		Name:      "HasTxns",
		Type:      "expense",
		SortOrder: 50,
	})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	seedTestTransaction(t, q, admin.ID, cat.ID, "2025-01-15", 42.0, "test txn")

	idStr := fmt.Sprintf("%d", cat.ID)
	req := httptest.NewRequest(http.MethodDelete, "/api/categories/"+idStr, nil)
	req = withUserAndURLParam(req, admin, "id", idStr)
	rec := httptest.NewRecorder()

	h.handleDeleteCategory(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if !strings.Contains(resp["error"], "deactivate") {
		t.Errorf("expected helpful message mentioning deactivation, got %q", resp["error"])
	}
}
