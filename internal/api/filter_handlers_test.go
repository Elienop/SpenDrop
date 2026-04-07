package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// --- handleListSavedFilters ---

func TestHandleListSavedFilters_ReturnsUserFilters(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Seed filters directly in DB
	_, err := q.CreateSavedFilter(context.Background(), database.CreateSavedFilterParams{
		UserID:     user.ID,
		Name:       "Last 30 days",
		FilterJson: `{"days":30}`,
	})
	if err != nil {
		t.Fatalf("seed filter: %v", err)
	}
	_, err = q.CreateSavedFilter(context.Background(), database.CreateSavedFilterParams{
		UserID:     user.ID,
		Name:       "Expenses only",
		FilterJson: `{"type":"expense"}`,
	})
	if err != nil {
		t.Fatalf("seed filter: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/filters", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListSavedFilters(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []database.SavedFilter
	decodeResponse(t, rec, &resp)
	if len(resp) != 2 {
		t.Errorf("expected 2 filters, got %d", len(resp))
	}
	// Should be ordered by name
	if len(resp) >= 2 && resp[0].Name != "Expenses only" {
		t.Errorf("expected first filter 'Expenses only', got %q", resp[0].Name)
	}
}

func TestHandleListSavedFilters_EmptyList(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/filters", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListSavedFilters(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []database.SavedFilter
	decodeResponse(t, rec, &resp)
	if len(resp) != 0 {
		t.Errorf("expected 0 filters for new user, got %d", len(resp))
	}
}

func TestHandleListSavedFilters_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/filters", nil)
	rec := httptest.NewRecorder()

	h.handleListSavedFilters(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleListSavedFilters_UserIsolation(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user1 := seedTestUser(t, q, "alice", "member")
	user2 := seedTestUser(t, q, "bob", "member")

	_, err := q.CreateSavedFilter(context.Background(), database.CreateSavedFilterParams{
		UserID:     user1.ID,
		Name:       "Alice filter",
		FilterJson: `{"owner":"alice"}`,
	})
	if err != nil {
		t.Fatalf("seed filter: %v", err)
	}

	// User2 should see no filters
	req := httptest.NewRequest(http.MethodGet, "/api/filters", nil)
	req = withUser(req, user2)
	rec := httptest.NewRecorder()

	h.handleListSavedFilters(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp []database.SavedFilter
	decodeResponse(t, rec, &resp)
	if len(resp) != 0 {
		t.Errorf("user2 should see 0 filters, got %d", len(resp))
	}
}

// --- handleCreateSavedFilter ---

func TestHandleCreateSavedFilter_Success(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"name":"My Filter","filter_json":"{\"days\":7}"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/filters", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateSavedFilter(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp database.SavedFilter
	decodeResponse(t, rec, &resp)
	if resp.Name != "My Filter" {
		t.Errorf("expected name 'My Filter', got %q", resp.Name)
	}
	if resp.FilterJson != `{"days":7}` {
		t.Errorf("expected filter_json '{\"days\":7}', got %q", resp.FilterJson)
	}
	if resp.UserID != user.ID {
		t.Errorf("expected user_id %d, got %d", user.ID, resp.UserID)
	}
	if resp.ID == 0 {
		t.Error("expected non-zero ID")
	}
}

func TestHandleCreateSavedFilter_MissingName_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"filter_json":"{\"days\":7}"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/filters", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateSavedFilter(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if resp["error"] == "" {
		t.Error("expected error message")
	}
}

func TestHandleCreateSavedFilter_MissingFilterJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"name":"My Filter"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/filters", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateSavedFilter(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateSavedFilter_InvalidJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/filters", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateSavedFilter(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleCreateSavedFilter_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"name":"My Filter","filter_json":"{}"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/filters", body)
	rec := httptest.NewRecorder()

	h.handleCreateSavedFilter(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleCreateSavedFilter_PersistedInDB(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"name":"Persisted","filter_json":"{\"check\":true}"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/filters", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateSavedFilter(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	// Verify via direct DB query
	filters, err := q.ListSavedFilters(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("list filters: %v", err)
	}
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter in DB, got %d", len(filters))
	}
	if filters[0].Name != "Persisted" {
		t.Errorf("expected name 'Persisted', got %q", filters[0].Name)
	}
}

// --- handleUpdateSavedFilter ---

func TestHandleUpdateSavedFilter_Success(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	filter, err := q.CreateSavedFilter(context.Background(), database.CreateSavedFilterParams{
		UserID:     user.ID,
		Name:       "Old Name",
		FilterJson: `{"old":true}`,
	})
	if err != nil {
		t.Fatalf("seed filter: %v", err)
	}

	body := strings.NewReader(`{"name":"New Name","filter_json":"{\"new\":true}"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/filters/1", body)
	req = withUserAndURLParam(req, user, "id", idStr(filter.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateSavedFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if resp["status"] != "updated" {
		t.Errorf("expected status 'updated', got %q", resp["status"])
	}

	// Verify in DB
	filters, err := q.ListSavedFilters(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("list filters: %v", err)
	}
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(filters))
	}
	if filters[0].Name != "New Name" {
		t.Errorf("expected name 'New Name', got %q", filters[0].Name)
	}
	if filters[0].FilterJson != `{"new":true}` {
		t.Errorf("expected updated filter_json, got %q", filters[0].FilterJson)
	}
}

func TestHandleUpdateSavedFilter_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"name":"Updated","filter_json":"{}"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/filters/abc", body)
	req = withUserAndURLParam(req, user, "id", "abc")
	rec := httptest.NewRecorder()

	h.handleUpdateSavedFilter(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleUpdateSavedFilter_MissingName_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	filter, err := q.CreateSavedFilter(context.Background(), database.CreateSavedFilterParams{
		UserID:     user.ID,
		Name:       "Original",
		FilterJson: `{}`,
	})
	if err != nil {
		t.Fatalf("seed filter: %v", err)
	}

	body := strings.NewReader(`{"filter_json":"{}"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/filters/1", body)
	req = withUserAndURLParam(req, user, "id", idStr(filter.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateSavedFilter(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateSavedFilter_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"name":"Updated","filter_json":"{}"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/filters/1", body)
	req = withURLParam(req, "id", "1")
	rec := httptest.NewRecorder()

	h.handleUpdateSavedFilter(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleUpdateSavedFilter_CrossUser_NoEffect(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user1 := seedTestUser(t, q, "alice", "member")
	user2 := seedTestUser(t, q, "bob", "member")

	filter, err := q.CreateSavedFilter(context.Background(), database.CreateSavedFilterParams{
		UserID:     user1.ID,
		Name:       "Alice filter",
		FilterJson: `{"owner":"alice"}`,
	})
	if err != nil {
		t.Fatalf("seed filter: %v", err)
	}

	// User2 tries to update user1's filter
	body := strings.NewReader(`{"name":"Hacked","filter_json":"{}"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/filters/1", body)
	req = withUserAndURLParam(req, user2, "id", idStr(filter.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateSavedFilter(rec, req)

	// The handler returns 200 (exec doesn't report "not found") but the DB is unchanged
	filters, err := q.ListSavedFilters(context.Background(), user1.ID)
	if err != nil {
		t.Fatalf("list filters: %v", err)
	}
	if len(filters) != 1 || filters[0].Name != "Alice filter" {
		t.Errorf("cross-user update should have no effect, got name %q", filters[0].Name)
	}
}

// --- handleDeleteSavedFilter ---

func TestHandleDeleteSavedFilter_Success(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	filter, err := q.CreateSavedFilter(context.Background(), database.CreateSavedFilterParams{
		UserID:     user.ID,
		Name:       "To Delete",
		FilterJson: `{}`,
	})
	if err != nil {
		t.Fatalf("seed filter: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/filters/1", nil)
	req = withUserAndURLParam(req, user, "id", idStr(filter.ID))
	rec := httptest.NewRecorder()

	h.handleDeleteSavedFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if resp["status"] != "deleted" {
		t.Errorf("expected status 'deleted', got %q", resp["status"])
	}

	// Verify deleted from DB
	filters, err := q.ListSavedFilters(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("list filters: %v", err)
	}
	if len(filters) != 0 {
		t.Errorf("expected 0 filters after delete, got %d", len(filters))
	}
}

func TestHandleDeleteSavedFilter_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodDelete, "/api/filters/abc", nil)
	req = withUserAndURLParam(req, user, "id", "abc")
	rec := httptest.NewRecorder()

	h.handleDeleteSavedFilter(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDeleteSavedFilter_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/filters/1", nil)
	req = withURLParam(req, "id", "1")
	rec := httptest.NewRecorder()

	h.handleDeleteSavedFilter(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleDeleteSavedFilter_CrossUser_NoEffect(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user1 := seedTestUser(t, q, "alice", "member")
	user2 := seedTestUser(t, q, "bob", "member")

	filter, err := q.CreateSavedFilter(context.Background(), database.CreateSavedFilterParams{
		UserID:     user1.ID,
		Name:       "Alice filter",
		FilterJson: `{}`,
	})
	if err != nil {
		t.Fatalf("seed filter: %v", err)
	}

	// User2 tries to delete user1's filter
	req := httptest.NewRequest(http.MethodDelete, "/api/filters/1", nil)
	req = withUserAndURLParam(req, user2, "id", idStr(filter.ID))
	rec := httptest.NewRecorder()

	h.handleDeleteSavedFilter(rec, req)

	// Filter should still exist
	filters, err := q.ListSavedFilters(context.Background(), user1.ID)
	if err != nil {
		t.Fatalf("list filters: %v", err)
	}
	if len(filters) != 1 {
		t.Errorf("cross-user delete should have no effect, got %d filters", len(filters))
	}
}

// idStr converts an int64 to its string representation for URL params.
func idStr(id int64) string {
	return strconv.FormatInt(id, 10)
}
