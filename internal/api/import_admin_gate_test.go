package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// --- router-level admin gating for the xlsx import surface ---
//
// The handler tests in import_handlers_test.go set auth.UserContextKey
// themselves and so never meet auth.RequireAdmin; several of them still seed a
// "member" user, which no longer describes a reachable production request.
// These tests round-trip through the router instead, so the gate has to sign
// off.

// registerViaRouter registers a user through the real /api/auth/register route
// and returns its session cookie. The first user the router ever sees becomes
// the admin; every later one is a member and needs registration_enabled first.
func registerViaRouter(t *testing.T, router http.Handler, username string) *http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %q: status=%d body=%s", username, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	t.Fatalf("register %q: no session cookie", username)
	return nil
}

// importRouteCases enumerates the whole import surface. Each non-GET case
// carries a JSON Content-Type so requireJSONContentType does not 415 ahead of
// the status under test. The importID is 32 characters because
// loadImportEntryForUser rejects any other length before it looks anything up
// — a shorter one would 400 for the wrong reason on the admin pass.
var importRouteCases = []struct {
	method string
	path   string
	body   string
}{
	{http.MethodPost, "/api/import/upload", `{}`},
	{http.MethodPost, "/api/import/confirm", `{"import_id":"00000000000000000000000000000000"}`},
	{http.MethodDelete, "/api/import/00000000000000000000000000000000", `{}`},
	{http.MethodPatch, "/api/import/00000000000000000000000000000000/rows/0", `{"field":"skip","value":true}`},
	{http.MethodGet, "/api/import/00000000000000000000000000000000", ""},
}

func newImportRouteRequest(method, path, body string, cookie *http.Cookie) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func TestNewRouter_ImportEndpoints_WithoutAuth_Return401(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	for _, c := range importRouteCases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, newImportRouteRequest(c.method, c.path, c.body, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status=%d, want 401; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNewRouter_ImportEndpoints_AsMember_Return403(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	registerViaRouter(t, router, "admin")
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   "registration_enabled",
		Value: "true",
	}); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}
	memberCookie := registerViaRouter(t, router, "member")

	for _, c := range importRouteCases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, newImportRouteRequest(c.method, c.path, c.body, memberCookie))
			if rec.Code != http.StatusForbidden {
				t.Errorf("status=%d, want 403; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestNewRouter_ImportEndpoints_AsAdmin_ReachTheHandler is the other half of
// the gate: the routes must still exist and still run for an admin.
//
// A status code alone cannot show that here. Deleting the route registrations
// entirely would leave chi's NotFound answering these paths, and three of the
// five cases expect a 404 from the handler itself — so a bare status assertion
// would pass against a router with no import routes at all. Each case
// therefore asserts the handler's own JSON error text, which chi's plain-text
// "404 page not found" cannot produce.
func TestNewRouter_ImportEndpoints_AsAdmin_ReachTheHandler(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)
	adminCookie := registerViaRouter(t, router, "admin")

	cases := []struct {
		method   string
		path     string
		body     string
		wantCode int
		wantErr  string
	}{
		// No multipart body, so the upload handler rejects the form read.
		{http.MethodPost, "/api/import/upload", `{}`,
			http.StatusBadRequest, "missing or invalid file upload"},
		{http.MethodPost, "/api/import/confirm", `{"import_id":"00000000000000000000000000000000"}`,
			http.StatusNotFound, "import not found or expired"},
		{http.MethodDelete, "/api/import/00000000000000000000000000000000", `{}`,
			http.StatusNotFound, "import not found or expired"},
		{http.MethodPatch, "/api/import/00000000000000000000000000000000/rows/0", `{"field":"skip","value":true}`,
			http.StatusNotFound, "import not found or expired"},
		{http.MethodGet, "/api/import/00000000000000000000000000000000", "",
			http.StatusNotFound, "import not found or expired"},
	}

	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, newImportRouteRequest(c.method, c.path, c.body, adminCookie))
			if rec.Code != c.wantCode {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, c.wantCode, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not the handler's JSON (chi NotFound?): %q", rec.Body.String())
			}
			if got, _ := body["error"].(string); got != c.wantErr {
				t.Errorf("error=%q, want %q", got, c.wantErr)
			}
		})
	}
}

// TestNewRouter_ImportUpload_AsAdmin_Succeeds is the end-to-end pass: a real
// xlsx through the real router, asserting a populated preview rather than just
// a 2xx. This is what proves the gate lets the actual feature through.
func TestNewRouter_ImportUpload_AsAdmin_Succeeds(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)
	adminCookie := registerViaRouter(t, router, "admin")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food"},
		{"2026-01-16", "Coffee", "5.75", "Food"},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode preview: %v; body=%s", err, rec.Body.String())
	}
	if id, _ := resp["import_id"].(string); id == "" {
		t.Errorf("import_id empty; body=%s", rec.Body.String())
	}
	if n, _ := resp["row_count"].(float64); int(n) != 2 {
		t.Errorf("row_count=%v, want 2; body=%s", resp["row_count"], rec.Body.String())
	}
}

// TestNewRouter_ImportLifecycle_AsAdmin_EveryRouteSucceeds drives a real
// session end to end so that every one of the five routes returns its success
// status for an admin, not merely "not 403".
//
// The 403 test alone cannot show this. A gate applied to four of five
// endpoints, or applied so broadly that it also rejects admins, both look
// correct in a member-only test — the failure that matters is the one where
// the feature stops working for the people who are supposed to have it. Two
// sessions are used because cancel destroys the session it acts on, so it
// cannot share one with confirm.
func TestNewRouter_ImportLifecycle_AsAdmin_EveryRouteSucceeds(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)
	adminCookie := registerViaRouter(t, router, "admin")

	// Confirm needs a real category to land the rows in.
	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(cats) == 0 {
		t.Fatal("expected the migrations to seed at least one category")
	}
	defaultCategoryID := cats[0].ID

	do := func(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, newImportRouteRequest(method, path, body, adminCookie))
		return rec
	}

	upload := func(t *testing.T, descriptions ...string) string {
		t.Helper()
		rows := make([][]string, 0, len(descriptions))
		for i, d := range descriptions {
			rows = append(rows, []string{
				fmt.Sprintf("2026-01-%02d", i+1), d, "42.50", cats[0].Name,
			})
		}
		xlsxData := createTestXLSX(t, "Transactions",
			[]string{"Date", "Description", "Amount", "Category"}, rows)
		req := postMultipartFile(t, "/api/import/upload", xlsxData)
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/import/upload: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode preview: %v", err)
		}
		id, _ := resp["import_id"].(string)
		if id == "" {
			t.Fatalf("no import_id in preview; body=%s", rec.Body.String())
		}
		return id
	}

	// Session 1: upload -> get -> patch -> confirm.
	// Descriptions are distinct so the confirm-time collision gate does not
	// 409 the batch for reasons unrelated to routing.
	id := upload(t, "Groceries", "Coffee")

	if rec := do(t, http.MethodGet, "/api/import/"+id, ""); rec.Code != http.StatusOK {
		t.Errorf("GET /api/import/{id}: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	if rec := do(t, http.MethodPatch, "/api/import/"+id+"/rows/0",
		`{"field":"description","value":"Groceries edited"}`); rec.Code != http.StatusOK {
		t.Errorf("PATCH /api/import/{id}/rows/{row}: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	confirmBody := fmt.Sprintf(`{"import_id":%q,"default_category_id":%d}`, id, defaultCategoryID)
	rec := do(t, http.MethodPost, "/api/import/confirm", confirmBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/import/confirm: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var confirmed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode confirm: %v", err)
	}
	if n, _ := confirmed["imported"].(float64); int(n) != 2 {
		t.Errorf("imported=%v, want 2; body=%s", confirmed["imported"], rec.Body.String())
	}

	// Session 2: upload -> cancel. Descriptions differ from session 1 so the
	// rows just committed above cannot collide with this preview.
	cancelID := upload(t, "Hardware store", "Pharmacy")
	if rec := do(t, http.MethodDelete, "/api/import/"+cancelID, `{}`); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE /api/import/{id}: status=%d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// mintAPITokenFor issues a live API token for an already-seeded user and
// returns the plaintext bearer string. Deliberately separate from
// seedUserWithLiveToken in homepage_handlers_test.go, which creates its own
// user and hardcodes the member role — the gate has to be exercised from both
// sides, so the role has to be the caller's choice.
func mintAPITokenFor(t *testing.T, q *database.Queries, user database.User) string {
	t.Helper()
	plaintext, tokenHash, prefix, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate api token for %s: %v", user.Username, err)
	}
	if _, err := q.CreateAPIToken(context.Background(), database.CreateAPITokenParams{
		UserID:      user.ID,
		TokenHash:   tokenHash,
		TokenPrefix: prefix,
		Name:        "import-gate-test-" + user.Username,
	}); err != nil {
		t.Fatalf("create api token for %s: %v", user.Username, err)
	}
	return plaintext
}

// TestNewRouter_ImportUpload_BearerToken_RespectsAdminGate pins the gate on the
// OTHER way in. Every test above authenticates with a session cookie, but these
// routes sit under RequireAuthOrAPIToken, so a Bearer API token reaches them
// too — and nothing else in the suite exercises the gate on that path.
//
// The code is correct today: RequireAPIToken resolves the whole row via
// GetUserByID and stores a real database.User under the same context key the
// session path uses, so RequireAdmin reads a genuine Role either way. What is
// unpinned is that this keeps being true. Caching user_id on the token to skip
// the lookup is an obvious performance change, and depending on how the
// synthesised user was built it would either lock admins out of import
// automation or hand the decision to a zero-valued Role. Neither outcome would
// fail any other test.
//
// One route is enough. The gate is a single r.Use and the five-route matrix is
// already covered on the cookie path; what is being pinned here is the auth
// mechanism, not the route list. Both cases send the byte-identical upload so
// the only variable is which token authenticates it.
func TestNewRouter_ImportUpload_BearerToken_RespectsAdminGate(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	admin := seedTestUser(t, q, "bearer-admin", "admin")
	member := seedTestUser(t, q, "bearer-member", "member")
	adminToken := mintAPITokenFor(t, q, admin)
	memberToken := mintAPITokenFor(t, q, member)

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{{"2026-01-15", "Groceries", "42.50", "Food"}})

	upload := func(t *testing.T, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := postMultipartFile(t, "/api/import/upload", xlsxData)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	t.Run("member token is refused", func(t *testing.T) {
		rec := upload(t, memberToken)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d, want 403; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("admin token reaches the handler", func(t *testing.T) {
		rec := upload(t, adminToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200 — an admin's token must still drive import; body=%s",
				rec.Code, rec.Body.String())
		}
		// A preview, not merely a 2xx: this is what shows the token carried a
		// real identity through the gate rather than something zero-valued
		// that happened not to be rejected.
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode preview: %v; body=%s", err, rec.Body.String())
		}
		if id, _ := resp["import_id"].(string); id == "" {
			t.Errorf("import_id empty; body=%s", rec.Body.String())
		}
	})
}
