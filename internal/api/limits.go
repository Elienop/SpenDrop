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

// Month-window caps for trend endpoints. 10 years is the upper bound for
// simple income/expense trend walks; category trends are capped lower
// because the query fans out per category.
const (
	MaxTrendMonths         = 120
	MaxCategoryTrendMonths = 60
)
