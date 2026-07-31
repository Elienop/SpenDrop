package api

import (
	"net/http"

	"github.com/elienop/spendrop/internal/auth"
)

// reportYearFloorResponse is the wire contract for
// GET /api/settings/report-year-floor.
//
// FloorYear is the only field the picker strictly needs; the two booleans
// exist so the UI can tell the three states apart without guessing:
//
//	has_transactions=false            -> empty ledger, FloorYear is the
//	                                     current-year fallback
//	has_transactions=true,  clamped=false -> FloorYear is real ledger data
//	has_transactions=true,  clamped=true  -> live rows exist BELOW FloorYear
//	                                     but below PlanningMinYear they are not
//	                                     reportable (see handler comment)
type reportYearFloorResponse struct {
	// FloorYear is the oldest year the Reports year picker may offer. It is
	// always a year the year-param endpoints accept, i.e. >= PlanningMinYear.
	FloorYear int `json:"floor_year"`
	// HasTransactions is false when the ledger holds no live rows at all.
	HasTransactions bool `json:"has_transactions"`
	// Clamped is true when the household has live transactions dated before
	// PlanningMinYear. Those rows are aggregated wherever a date RANGE covers them,
	// but no year picker can select their year.
	Clamped bool `json:"clamped"`
}

// handleReportYearFloor reports the oldest year the Reports year picker should
// offer, derived from the ledger instead of a hard-coded constant.
//
// Why this endpoint exists: the picker's floor used to be
// HISTORICAL_YEAR_START = 2024 in web/src/lib/dates.ts, while the importer
// deliberately accepts 1900-2100 so historic bank statements can be loaded. A
// user could import a 2019 statement, have every aggregate include it, and
// never be able to select 2019 in any Reports tab.
//
// Two deliberate decisions:
//
//   - Household-wide, not per-user. The transactions list is visible to every
//     authenticated user (see handleListTransactions), so scoping the picker
//     to the caller's own rows would hide a year whose amounts are already on
//     screen in every aggregate.
//
//   - Clamped to PlanningMinYear (2000). This clamp is now PURELY a wire
//     compatibility constant, not a live constraint. When this endpoint was
//     written, every year-param endpoint validated against a single
//     [2000, 2100] window, so returning a raw floor of 1995 would have made
//     the picker offer 1995 and every request the tab then issued would 400.
//     The report and dashboard endpoints have since moved to the wider DATA
//     window [MinDataYear, MaxDataYear] and would accept 1995 fine — but this
//     response shape is frozen: an installed PWA keeps serving its cached
//     bundle until the service worker updates, and that bundle reads
//     `floor_year` under these exact semantics. Changing the number here would
//     change what an already-shipped client renders. New clients read
//     GET /api/reports/years instead. `clamped` is still reported so the UI can
//     say a year is unreachable rather than silently dropping it.
//
// Empty ledger falls back to the current year (h.clock.Now()), so the picker
// offers exactly this year. Falling back to PlanningMinYear instead would open a fresh
// install with 27 selectable years that all render empty.
//
// The floor is also held at or below the current year. The picker counts DOWN
// from the current year, so a ledger holding only future-dated rows (planned
// or scheduled entries) would otherwise produce a floor above the ceiling and
// an empty dropdown. Clock skew between server and browser can still straddle
// a New Year boundary, so the client should apply the same
// `min(floor_year, its own current year)` guard.
func (h *Handler) handleReportYearFloor(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	oldest, err := h.queries.GetOldestTransactionYear(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get report year floor")
		return
	}

	currentYear := h.clock.Now().Year()

	// Empty-ledger fallback.
	floor := currentYear
	clamped := false

	if oldest.Valid {
		floor = int(oldest.Int64)
		// Reported off the RAW oldest year, before any clamping: the claim is
		// "the household has live rows the picker cannot reach", and that is
		// true regardless of what the floor is subsequently pulled up to.
		clamped = floor < PlanningMinYear
		if floor > currentYear {
			floor = currentYear
		}
	}

	// Applied last so the contract's invariant — floor_year is always a year
	// the year-param endpoints accept — holds on every path, including the
	// fallback under a misconfigured server clock.
	if floor < PlanningMinYear {
		floor = PlanningMinYear
	}

	writeJSON(w, http.StatusOK, reportYearFloorResponse{
		FloorYear:       floor,
		HasTransactions: oldest.Valid,
		Clamped:         clamped,
	})
}
