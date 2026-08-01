package api

// Numeric business-logic limits that are invariants of the app, not
// operator-tunable knobs. Knobs that an operator might reasonably want
// to adjust at runtime (body caps, rate limits, password bounds, session
// TTLs, etc.) live in internal/config and are read through runtime.go.
//
// These values, by contrast, are part of the product's data-model
// contract — the SQL schema, the API wire format, and the UI all assume
// the same caps, and changing one without the other is a bug. Centralising
// them makes that contract explicit.

// Year bounds. There are two windows because there are two jobs, and
// conflating them is what produced the hole this pair replaces: a single
// [2000, 2100] constant guarded every year PARAM while nothing at all
// guarded a transaction DATE, so the app could store rows in years no
// report would accept.
//
// MinDataYear / MaxDataYear bound the window a transaction date may
// occupy. Everything that reads ledger data by year — reports, dashboard,
// export, and validateDate on the write side — uses this pair, so every
// year the ledger can hold is a year the reports can display.
//
// PlanningMinYear / PlanningMaxYear bound the year params of budgets and
// savings goals. Those rows are configuration the household writes forward,
// not history it imports backward: nothing plans a 1990 budget, and the
// Budgets page's year Select bottoms out at MIN_YEAR (web/src/lib/dates.ts).
// Widening them would add a century of empty years to that Select for no
// gain.
//
// NEVER lower MinDataYear below 1000. Two verified hazards live under four
// digits:
//
//   - web/src/components/reports/SpendingHeatmap.tsx (lines 24 and 31)
//     builds `new Date(Date.UTC(year, 0, 1))`, and JS maps years 0-99 onto
//     1900-1999 — a year-84 row would silently render as 1984.
//   - The report handlers build ranges with fmt.Sprintf("%d-01-01", year),
//     which emits "5-01-01" for year 5 and breaks the lexical string
//     comparison SumByMonthRange does against the stored dates.
//
// 1900 specifically because parseImportDate has accepted [1900, 2100] since
// it was written (minImportYear / maxImportYear in import_handlers.go, now
// aliases of these). One product-wide window, not two that disagree.
const (
	MinDataYear = 1900
	MaxDataYear = 2100

	PlanningMinYear = 2000
	PlanningMaxYear = 2100
)

// Pagination defaults and caps for list endpoints.
const (
	DefaultPerPage = 25
	MaxPerPage     = 100
	// MaxPage is the upper bound on the page number to keep pathological
	// `?page=999999999` requests from producing a huge OFFSET scan.
	MaxPage = 1_000_000
)

// Batch-operation caps. These bound how many items a single request can
// mutate in one database transaction.
const (
	MaxBatchTransactions = 500
	MaxBatchDeleteIDs    = 500
	// MaxBatchRestoreIDs is pinned to MaxBatchDeleteIDs so "undo the
	// last batch delete" is always possible in a single request — an
	// operator who just nuked 500 rows by accident cannot have the
	// restore cap be smaller than the delete cap or the undo becomes
	// a multi-step dance. Declared as a named alias rather than a
	// literal reference at the call site so the symmetry is visible
	// in code review and a future split (if the two ever diverge) is
	// a one-line change here.
	MaxBatchRestoreIDs = MaxBatchDeleteIDs
	// MaxBatchUpdateIDs caps the size of the ID list accepted by
	// /api/transactions/batch-update. Pinned to MaxBatchDeleteIDs because
	// every bulk mutation should share the same per-request blast radius.
	MaxBatchUpdateIDs      = MaxBatchDeleteIDs
	MaxCategoryReorder     = 200
	MaxImportRows          = 10000
	MaxSavedFilters        = 50
	MaxMultiCategoryFilter = 100
)

// MaxTransactionAmount is the largest positive value accepted for a
// transaction (or budget). Anything above this is almost certainly a
// currency-entry mistake (e.g. pasting an entire account balance into
// the amount field) and would overflow some downstream chart axes.
const MaxTransactionAmount = 1_000_000_000

// String length caps for user-supplied text fields. These match the
// SQLite column definitions in migrations/001 and the Zod schemas on
// the frontend.
const (
	MaxDescriptionLength    = 500
	MaxTagsLength           = 500
	MaxNotesLength          = 2000
	MaxDisplayNameLength    = 64
	MinUsernameLength       = 3
	MaxUsernameLength       = 32
	MaxCategoryNameLength   = 100
	MaxIconNameLength       = 100
	MaxCurrencyNameLength   = 100
	MaxCurrencySymbolLength = 10
	MaxFilterNameLength     = 100
	MaxFilterJSONLength     = 10000
)

// Bounds on the client-supplied idempotency key for POST /api/transactions.
// Clients mint crypto.randomUUID(), which is 36 characters; 64 leaves room for
// a prefixed or otherwise-structured key from the API-token surface without
// letting an unbounded string reach an indexed column.
//
// The 16-character floor exists to keep degenerate values out of the key
// namespace. A key is a claim, and a short one is a claim somebody else's
// script will make too: "0", "1", "undefined" and "null" are what a caller
// emits when its key generator silently failed, and every submission after
// that would replay against the first one and be discarded as a retry. The
// floor makes an accidental key too short to be accepted at all, so the caller
// gets a 400 instead of a ledger that quietly stops recording. It cannot break
// a real client — a UUID is 36 characters and any deliberate scheme clears 16.
//
// The bounds are in bytes, which equals characters here because the charset
// (see clientKeyPattern) is ASCII-only.
const (
	MinClientKeyLength = 16
	MaxClientKeyLength = 64
)

// Query result caps for read endpoints that don't expose pagination.
const (
	DefaultTopMerchantsLimit   = 10
	MaxTopMerchantsLimit       = 50
	DescriptionSuggestionLimit = 500
	TagSuggestionLimit         = 1000
)

// MaxExportRows is the upper bound on rows included in a single
// spreadsheet export. Higher values would blow up memory in excelize's
// in-memory sheet buffer before the download could finish streaming.
const MaxExportRows = 50_000

// MaxSubscriptionsPerUser caps how many browser push subscriptions one user
// may register. A user with phone + laptop + tablet needs a handful; 20 is
// generous headroom while still bounding a misbehaving client that re-POSTs a
// fresh endpoint on every page load.
const MaxSubscriptionsPerUser = 20

// Month-window caps for trend endpoints. Both income/expense trends and the
// dashboard trend resolve their whole window from ONE SumByMonthRange query
// and then walk it in memory, so the cost is linear in the response size, not
// in queries. Category trends are capped far lower because that query fans out
// per category.
//
// MaxTrendMonths is DERIVED, not chosen. The Savings tab sizes its window from
// the year the user picked (web/src/components/reports/utils.ts,
// MAX_REPORT_MONTHS, which must match this value) and the year picker is
// driven by the ledger, so the widest window the UI can legitimately ask for
// is January of the oldest year the ledger may hold through December of the
// newest — i.e. the whole DATA window. Past MaxDataYear every year-param
// endpoint 400s, so nothing wider is reachable.
//
// There is now ZERO slack: at [1900, 2100] this is exactly the widest
// reachable window, not a value comfortably above it. It used to be sized
// against the narrower planning window and could be described as "far larger
// than any window yearOptions can ask for" — that is no longer true, and any
// future widening of the data window MUST come here (and to the frontend
// literal) in the same change or the oldest years start truncating silently.
//
// Two earlier values were literals sized against a then-hard-coded frontend
// floor of 2024, and each one was already scheduled to start silently
// truncating the oldest selectable years: 120 would have bound from 2034, and
// 600 from 2050. Deriving the constant removes that whole class of expiry —
// the only way to shrink this window now is to narrow the data bounds, which
// is exactly when it should shrink.
const (
	MaxTrendMonths         = (MaxDataYear - MinDataYear + 1) * 12
	MaxCategoryTrendMonths = 60
)
