package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

func seedSavingsGoal(t *testing.T, h *Handler, year int64, cents int64) {
	t.Helper()
	if err := h.queries.UpsertSavingsGoal(t.Context(), database.UpsertSavingsGoalParams{
		Year:              year,
		TargetAmountCents: cents,
	}); err != nil {
		t.Fatalf("seed savings goal: %v", err)
	}
}

func deleteSavingsGoalReq(t *testing.T, h *Handler, user database.User, year int64) *httptest.ResponseRecorder {
	t.Helper()
	y := strconv.FormatInt(year, 10)
	req := withUserAndURLParam(
		httptest.NewRequest(http.MethodDelete, "/api/savings-goals/"+y, nil),
		user, "year", y,
	)
	rec := httptest.NewRecorder()
	h.handleDeleteSavingsGoal(rec, req)
	return rec
}

// TestHandleDeleteSavingsGoal_RemovesTheRow is the regression test for the
// no-op delete. Removal was previously expressed as PUT {target_amount: 0},
// which upserted a zero row that persisted and still rendered as a goal card
// while the UI claimed it had been removed.
func TestHandleDeleteSavingsGoal_RemovesTheRow(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", "admin")
	seedSavingsGoal(t, h, 2026, 500000)

	rec := deleteSavingsGoalReq(t, h, admin, 2026)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	goals, err := h.queries.ListSavingsGoals(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, g := range goals {
		if g.Year == 2026 {
			t.Fatalf("goal for 2026 survived the delete with target_amount_cents=%d",
				g.TargetAmountCents)
		}
	}
}

// setSavingsGoalReq drives the real PUT handler, which is the only way to
// exercise what the route actually does with a target of zero.
func setSavingsGoalReq(t *testing.T, h *Handler, user database.User, year int64, dollars string) *httptest.ResponseRecorder {
	t.Helper()
	y := strconv.FormatInt(year, 10)
	req := withUserAndURLParam(
		httptest.NewRequest(http.MethodPut, "/api/savings-goals/"+y,
			strings.NewReader(`{"target_amount":`+dollars+`}`)),
		user, "year", y,
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleSetSavingsGoal(rec, req)
	return rec
}

// TestHandleSetSavingsGoal_ZeroTargetIsNotADelete pins the semantics the new
// route buys us: zero is a legitimate "no target this year" value and must
// persist, rather than being overloaded to mean removal.
//
// This previously seeded a zero row with UpsertSavingsGoal and asserted the row
// was still there, never calling handleSetSavingsGoal at all — so it proved only
// that SQLite can store a 0, and the semantic it is named for was untested. It
// now PUTs zero over an existing non-zero goal, which is the sequence a user
// performs and the one that would regress if zero were ever treated as removal.
func TestHandleSetSavingsGoal_ZeroTargetIsNotADelete(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", "admin")

	// Start from a real target, so a delete-on-zero regression has something to
	// destroy. Seeding zero directly could not tell the two behaviours apart.
	seedSavingsGoal(t, h, 2026, 500000)

	rec := setSavingsGoalReq(t, h, admin, 2026, "0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	goals, err := h.queries.ListSavingsGoals(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, g := range goals {
		if g.Year == 2026 {
			found = true
			if g.TargetAmountCents != 0 {
				t.Errorf("target_amount_cents = %d, want 0", g.TargetAmountCents)
			}
		}
	}
	if !found {
		t.Error("PUT target_amount=0 removed the row — zero is a legitimate target, " +
			"not a request to delete; DELETE /savings-goals/{year} is the removal route")
	}

	// The PUT deliberately answers with a status envelope rather than the row —
	// clients refetch via the invalidate it publishes — so there is no money
	// field here to check, and equally no *_cents to leak. The row assertion
	// above is the real one.
	var body map[string]any
	decodeResponse(t, rec, &body)
	if body["status"] != "updated" {
		t.Errorf("body = %v, want status=updated", body)
	}
	for k := range body {
		if strings.HasSuffix(k, "_cents") {
			t.Errorf("response leaked %q; the wire contract for money is dollars", k)
		}
	}
}

func TestHandleDeleteSavingsGoal_UnknownYearReturns404(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", "admin")

	rec := deleteSavingsGoalReq(t, h, admin, 2031)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteSavingsGoal_NonAdminForbidden(t *testing.T) {
	h := setupHandler(t)
	member := seedTestUser(t, h.queries, "member", "member")
	seedSavingsGoal(t, h, 2026, 500000)

	rec := deleteSavingsGoalReq(t, h, member, 2026)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	goals, _ := h.queries.ListSavingsGoals(t.Context())
	if len(goals) != 1 {
		t.Errorf("a forbidden delete still removed the row: %d goals remain", len(goals))
	}
}

func TestHandleDeleteSavingsGoal_InvalidYearReturns400(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", "admin")

	req := withUserAndURLParam(
		httptest.NewRequest(http.MethodDelete, "/api/savings-goals/nope", nil),
		admin, "year", "nope",
	)
	rec := httptest.NewRecorder()
	h.handleDeleteSavingsGoal(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
