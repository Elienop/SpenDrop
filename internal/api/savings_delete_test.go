package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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

// TestHandleSetSavingsGoal_ZeroTargetIsNotADelete pins the semantics the new
// route buys us: zero is a legitimate "no target this year" value and must
// persist, rather than being overloaded to mean removal.
func TestHandleSetSavingsGoal_ZeroTargetIsNotADelete(t *testing.T) {
	h := setupHandler(t)
	seedSavingsGoal(t, h, 2026, 0)

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
		t.Error("a zero-target goal should persist as a real row")
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
