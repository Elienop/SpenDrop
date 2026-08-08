// App-wide UI / business-logic constants. These are invariants of the
// product (not operator-tunable knobs), centralised so that the same
// value does not need to be copy-pasted across files.
//
// Values that originate on the backend (year bounds, batch caps, string
// lengths, etc.) should match the matching constants in
// `internal/api/limits.go`; if the backend changes, update this file in
// the same PR.

// --- Pagination ---

/**
 * Selectable page sizes on the transactions table.
 * Matches `MaxPerPage = 100` on the backend — any value added here must
 * be <= that cap, otherwise the server will clamp it silently.
 */
export const TRANSACTION_PAGE_SIZES = [10, 20, 50, 100] as const;

/** Default per-page value when the user has no saved preference. */
export const DEFAULT_TRANSACTIONS_PER_PAGE = 20;

// --- Dashboard / Overview ---

/** Number of recent transactions shown on the dashboard. */
export const DASHBOARD_RECENT_TX_LIMIT = 6;

/** Number of categories shown before "Show more" collapses the rest. */
export const DASHBOARD_CATEGORY_COLLAPSED_LIMIT = 6;

// --- Reports ---

/**
 * Default number of entries on the "Top Merchants" report. Matches
 * `DefaultTopMerchantsLimit` on the backend.
 */
export const TOP_MERCHANTS_DEFAULT_LIMIT = 10;

/** Default number of months shown on trend-style reports. */
export const DEFAULT_TREND_MONTHS = 12;

// --- Text field lengths ---
//
// All of these are CHARACTER counts, matching the constants of the same name in
// `internal/api/limits.go`, and they must be validated with `charCount()` from
// `lib/text.ts` rather than `String.length` or Zod's `.max()`. Both of those
// count UTF-16 code units, which disagree with the server on any character
// outside the Basic Multilingual Plane — an emoji is 2 to JavaScript and 1 to
// Go. See the note in `lib/text.ts`.

/** Transaction description. Backend: `MaxDescriptionLength`. */
export const MAX_DESCRIPTION_LENGTH = 500;

/** API token name. Backend: `apiTokenNameMax`, and migration 011's
 *  `CHECK(length(name) BETWEEN 1 AND 100)` — SQLite counts characters. */
export const MAX_API_TOKEN_NAME_LENGTH = 100;

/** Currency symbol. Backend: `MaxCurrencySymbolLength`. Wide enough for the
 *  multi-character symbols real currencies use ("ل.ل." is four). */
export const MAX_CURRENCY_SYMBOL_LENGTH = 10;

/** Household member display name. Backend: `MaxDisplayNameLength`, applied
 *  through `charLen` (a rune count) in both the register and the admin-edit
 *  handlers. */
export const MAX_DISPLAY_NAME_LENGTH = 64;
