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
