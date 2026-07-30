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

// Year bounds for budget, savings, dashboard, and export endpoints.
// Keep narrow enough that the UI's year picker does not need to paginate
// and wide enough that operators who import historic data from 20-year-old
// spreadsheets do not hit a wall.
const (
	MinYear = 2000
	MaxYear = 2100
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
// MAX_REPORT_MONTHS, which must match this value), and the year picker's floor
// now comes from the ledger via GET /api/settings/report-year-floor. That
// handler clamps the floor to MinYear, so the widest window the UI can ever
// legitimately ask for is January of MinYear through December of MaxYear —
// past MaxYear every year-param endpoint 400s, so nothing wider is reachable.
//
// Two earlier values were literals sized against the then-hard-coded frontend
// floor of 2024, and each one was already scheduled to start silently
// truncating the oldest selectable years: 120 would have bound from 2034, and
// 600 binds from 2050 once the floor can reach MinYear. Deriving the constant
// removes that whole class of expiry — the only way to shrink this window now
// is to narrow MinYear/MaxYear, which is exactly when it should shrink.
const (
	MaxTrendMonths         = (MaxYear - MinYear + 1) * 12
	MaxCategoryTrendMonths = 60
)
