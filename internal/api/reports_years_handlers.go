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
	// OutOfRangeYears is every dropped year that falls OUTSIDE
	// [MinDataYear, MaxDataYear]. Newest first, deduplicated, and `[]` — never
	// null — for the ordinary household.
	//
	// This is the DEFECT bucket: legacy or corrupt rows from before
	// validateDate was bounded. Every year-param endpoint 400s on these
	// (measured: budget-vs-actual, dashboard/summary and year-over-year all
	// return 400 for 1850 and 3021), and no passage of time changes that. The
	// user may want to go fix them.
	//
	// This field is the whole point of returning a structure rather than a
	// list. Dropping a year silently is the exact bug class this work exists
	// to kill: a legacy 3021 row would simply cease to appear anywhere in the
	// UI's view of the ledger while still sitting in the transactions list.
	OutOfRangeYears []int `json:"out_of_range_years"`
	// FutureYears is every dropped year that is later than CurrentYear but
	// still INSIDE [MinDataYear, MaxDataYear]. Newest first, deduplicated, and
	// `[]` — never null — for the ordinary household.
	//
	// This is the FEATURE bucket, and it exists because these two causes used
	// to share one field. A deliberately planned 2027 bill is a normal
	// workflow, not a data problem: POST /api/transactions accepts the date
	// (measured: 201), and every year-param endpoint answers for 2027 with the
	// row's amount present. Only this handler's own cap keeps 2027 out of the
	// picker, and that cap lifts by itself on 1 January 2027.
	//
	// Split so the UI can say something TRUE and DIFFERENT for each: an
	// out-of-range year is a limitation the user may want to act on, a future
	// year is information about the reports' scope. Naming both in one
	// "these years cannot be selected" sentence told a user their deliberate
	// plan was corrupt data.
	FutureYears []int `json:"future_years"`
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
// Everything either filter drops is REPORTED back, never dropped silently —
// but under TWO separate keys, because the two filters have nothing to do with
// each other:
//
//   - out_of_range_years is a DEFECT. The year is outside the window, every
//     year-param endpoint 400s on it, and only editing the row will change
//     that. Measured against a live server with sentinel rows at 1850 and
//     3021: budget-vs-actual, dashboard/summary and year-over-year all 400.
//
//   - future_years is a FEATURE in progress. The year is perfectly valid —
//     POST /api/transactions accepts the date, and every year-param endpoint
//     answers for it with the row's amounts present (measured: with a 2027
//     row of $999, budget-vs-actual?year=2027 returns actual=999 in month 3
//     and dashboard/summary?year=2027 returns savings_ytd=-999). It is
//     withheld ONLY by this handler's cap, which lifts on its own when the
//     year arrives.
//
// PRECEDENCE: a year that is both — 3021 — belongs to out_of_range_years and
// appears in NOTHING else. The window violation is the more actionable fact,
// and the future framing ("this will appear when the year arrives") is a
// promise that would be false for a year the endpoints will never accept.
//
// Household-wide, matching handleListTransactions and handleReportYearFloor:
// the transactions list is visible to every authenticated user, so a per-user
// picker would hide a year whose amounts are already inside every aggregate on
// the page.
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

	years, outOfRange, future := partitionReportYears(ledgerYears, currentYear)

	writeJSON(w, http.StatusOK, reportYearsResponse{
		Years:       years,
		CurrentYear: currentYear,
		// Live rows, not offered years — see the field comment.
		HasTransactions: len(ledgerYears) > 0,
		OutOfRangeYears: outOfRange,
		FutureYears:     future,
	})
}

// partitionReportYears splits the ledger's years three ways — offerable,
// out-of-window, and in-window-but-future — unioning in currentYear so the
// offerable list is never empty. All three are newest-first and free of
// duplicates, and each ledger year lands in exactly ONE of them.
//
// THE ORDER OF THE TWO REJECTION TESTS IS THE PRECEDENCE RULE, not style. The
// window test runs first, so 3021 — out of window AND in the future — is
// out-of-range and nothing else. Swapping them would move it to future_years
// and make the UI promise it becomes reportable in 3021, which it never does:
// every year-param endpoint 400s outside [MinDataYear, MaxDataYear].
//
// All three slices are initialised non-nil so they marshal to `[]` rather than
// `null`. The consumer reads `.length` off both reject lists, and a `null`
// there unmounts the Reports page.
//
// ledgerYears arrives DESC and DISTINCT from ListTransactionYears, so a single
// pass preserves ordering without a sort.
//
// currentYear can simply LEAD the offerable list rather than being inserted at
// an ordered position, and that is a consequence of the filters, not a
// coincidence: any ledger year greater than currentYear is diverted to future
// or out-of-range, so every year that survives is <= currentYear. Change the
// future filter and this ordering stops holding.
//
// Split out from the handler so the ordering, dedup and precedence rules are
// reviewable on their own.
func partitionReportYears(ledgerYears []int64, currentYear int) (offerable, outOfRange, future []int) {
	offerable = append(make([]int, 0, len(ledgerYears)+1), currentYear)
	outOfRange = []int{}
	future = []int{}

	for _, y64 := range ledgerYears {
		year := int(y64)
		if year == currentYear {
			continue // already leading the list; do not duplicate
		}
		// Window first: see the precedence note above.
		if year < MinDataYear || year > MaxDataYear {
			outOfRange = append(outOfRange, year)
			continue
		}
		if year > currentYear {
			future = append(future, year)
			continue
		}
		offerable = append(offerable, year)
	}

	return offerable, outOfRange, future
}
