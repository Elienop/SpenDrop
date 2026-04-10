package api

import (
	"context"
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
