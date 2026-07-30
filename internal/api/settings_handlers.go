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
//	                                     but below MinYear they are not
//	                                     reportable (see handler comment)
type reportYearFloorResponse struct {
	// FloorYear is the oldest year the Reports year picker may offer. It is
	// always a year the year-param endpoints accept, i.e. >= MinYear.
	FloorYear int `json:"floor_year"`
	// HasTransactions is false when the ledger holds no live rows at all.
	HasTransactions bool `json:"has_transactions"`
	// Clamped is true when the household has live transactions dated before
	// MinYear. Those rows are aggregated wherever a date RANGE covers them,
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
//   - Clamped to MinYear. Year-param endpoints validate against
//     MinYear/MaxYear (internal/api/limits.go). Returning a raw floor of 1995
//     would make the picker offer 1995, and every request the tab then issued
//     would 400. Widening MinYear instead would touch budget, savings and
//     checkpoint validation and is a separate decision. The residual: rows
//     dated 1900-1999 stay importable and stay inside date-range aggregates,
//     but their year cannot be selected. `clamped` is reported so the UI can
//     say so rather than silently dropping them.
//
// Empty ledger falls back to the current year (h.clock.Now()), so the picker
// offers exactly this year. Falling back to MinYear instead would open a fresh
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
		clamped = floor < MinYear
		if floor > currentYear {
			floor = currentYear
		}
	}

	// Applied last so the contract's invariant — floor_year is always a year
	// the year-param endpoints accept — holds on every path, including the
	// fallback under a misconfigured server clock.
	if floor < MinYear {
		floor = MinYear
	}

	writeJSON(w, http.StatusOK, reportYearFloorResponse{
		FloorYear:       floor,
		HasTransactions: oldest.Valid,
		Clamped:         clamped,
	})
}
