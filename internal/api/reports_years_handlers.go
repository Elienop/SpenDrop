package api

import (
	"net/http"

	"github.com/elienop/spendrop/internal/auth"
)

// reportYearsResponse is the wire contract for GET /api/reports/years.
//
// It replaces GET /api/settings/report-year-floor, which could only express a
// contiguous range from a single floor. A ledger is not contiguous — a
// household with rows in 2019 and 2026 and nothing between should not be
// offered six empty years — and a single integer had nowhere to put the years
// it had to drop.
type reportYearsResponse struct {
	// Years is every year the picker may offer, newest first. Guaranteed:
	// every element is inside [MinDataYear, MaxDataYear], no element is
	// greater than CurrentYear, and CurrentYear itself is always present, so
	// the list is never empty.
	Years []int `json:"years"`
	// CurrentYear is the server's current year. Sent so the client does not
	// have to trust its own clock to work out which entry is "this year";
	// server and browser can straddle a New Year boundary.
	CurrentYear int `json:"current_year"`
	// HasTransactions is false only when the ledger holds no LIVE rows at all.
	// It tracks rows, not offered years: a household whose only row is dated
	// 3021 still HAS transactions, and the UI needs to distinguish "you have
	// no data" from "you have data none of it reportable".
	HasTransactions bool `json:"has_transactions"`
	// OutOfRangeYears is every year the ledger holds that Years had to drop —
	// either outside the reportable window or in the future. Newest first,
	// deduplicated. Empty for the ordinary household.
	//
	// This field is the whole point of returning a structure rather than a
	// list. Dropping a year silently is the exact bug class this work exists
	// to kill: a legacy 3021 row would simply cease to appear anywhere in the
	// UI's view of the ledger while still sitting in the transactions list.
	OutOfRangeYears []int `json:"out_of_range_years"`
}

// handleReportYears reports which years the Reports year pickers should offer,
// derived from the ledger rather than a constant.
//
// Three filters run over the raw ledger years, and each one is load-bearing:
//
//   - Outside [MinDataYear, MaxDataYear]. validateDate had no year bound at
//     all until the data window shipped, so any deployment may hold rows dated
//     1850 or 3021. Every year-param endpoint 400s on those, so offering one
//     would break every tab at the same moment.
//
//   - Later than the current year. This is the most dangerous one to get
//     wrong, and it is not merely cosmetic. SavingsTab sizes its window with
//     monthsToCoverYear(year, currentYear) = (currentYear - year + 1) * 12,
//     which goes <= 0 for a future year and floors at 24. The tab then renders
//     a trailing 24-month window as though it were that year: $0 and "0% of
//     goal" for a year that holds real data. A confidently wrong number is
//     worse than an empty state. (Planned / future-dated rows are a real
//     feature, so this case is not hypothetical.)
//
//   - Nothing at all, when the ledger is empty. The current year is unioned
//     in unconditionally, so a fresh install and a past-only ledger both yield
//     a non-empty list. An empty dropdown is not a state the UI can render.
//
// Everything either filter drops lands in out_of_range_years so the UI can say
// so. Household-wide, matching handleListTransactions and
// handleReportYearFloor: the transactions list is visible to every
// authenticated user, so a per-user picker would hide a year whose amounts are
// already inside every aggregate on the page.
func (h *Handler) handleReportYears(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	ledgerYears, err := h.queries.ListTransactionYears(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list report years")
		return
	}

	// Held inside the data window before anything else uses it, so the
	// contract's invariant — every year in `years` is a year the year-param
	// endpoints accept — holds even under a misconfigured server clock. A
	// clock outside [MinDataYear, MaxDataYear] is past the range this
	// application models at all; reporting the clamped value keeps
	// `current_year` pointing at an entry that actually exists in `years`.
	currentYear := h.clock.Now().Year()
	if currentYear < MinDataYear {
		currentYear = MinDataYear
	}
	if currentYear > MaxDataYear {
		currentYear = MaxDataYear
	}

	years, outOfRange := partitionReportYears(ledgerYears, currentYear)

	writeJSON(w, http.StatusOK, reportYearsResponse{
		Years:       years,
		CurrentYear: currentYear,
		// Live rows, not offered years — see the field comment.
		HasTransactions: len(ledgerYears) > 0,
		OutOfRangeYears: outOfRange,
	})
}

// partitionReportYears splits the ledger's years into the offerable list and
// the dropped list, unioning in currentYear so the offerable list is never
// empty. Both are newest-first and free of duplicates.
//
// ledgerYears arrives DESC and DISTINCT from ListTransactionYears, so a single
// pass preserves ordering without a sort.
//
// currentYear can simply LEAD the list rather than being inserted at an
// ordered position, and that is a consequence of the filters, not a
// coincidence: any ledger year greater than currentYear is dropped as future,
// so every year that survives is <= currentYear. Change the future filter and
// this ordering stops holding.
//
// Split out from the handler so the ordering and dedup rules are reviewable on
// their own.
func partitionReportYears(ledgerYears []int64, currentYear int) (offerable, outOfRange []int) {
	offerable = append(make([]int, 0, len(ledgerYears)+1), currentYear)
	outOfRange = []int{}

	for _, y64 := range ledgerYears {
		year := int(y64)
		if year == currentYear {
			continue // already leading the list; do not duplicate
		}
		if year < MinDataYear || year > MaxDataYear || year > currentYear {
			outOfRange = append(outOfRange, year)
			continue
		}
		offerable = append(offerable, year)
	}

	return offerable, outOfRange
}
