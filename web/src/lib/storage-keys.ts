// Single source of truth for every localStorage / sessionStorage key used
// by the app. Keeping them in one file avoids typo-driven bugs (where a
// setter and getter disagree on the key) and makes it trivial to audit
// what we persist in the browser.
//
// Convention: every key is prefixed with `spendrop-` so that dev tools
// like "Application → Storage → LocalStorage" can be filtered at a
// glance, and so that nothing we persist can collide with a third-party
// library that happens to share the origin.

export const STORAGE_KEYS = {
  /** Active color theme name (see color-themes.ts). */
  colorTheme: 'spendrop-color-theme',
  /** Dark / light / system preference for the UI. */
  theme: 'spendrop-theme',
  /** Sidebar expanded-vs-collapsed state. */
  sidebar: 'spendrop-sidebar',
  /** Last date used in the transaction entry row, for sticky default. */
  lastTransactionDate: 'spendrop-last-date',
  /** Last category used in the transaction entry row, for sticky default. */
  lastTransactionCategory: 'spendrop-last-category',
  /** Last currency used in the transaction entry row, for sticky default. */
  lastTransactionCurrency: 'spendrop-last-currency',
  /** Dashboard year filter persistence. */
  dashboardYear: 'spendrop-dash-year',
  /** Dashboard month filter persistence. */
  dashboardMonth: 'spendrop-dash-month',
  /** Rows-per-page selection on the transactions table. */
  transactionsPerPage: 'spendrop-tx-per-page',
  /**
   * The import_id of the currently-active import preview session, if
   * any. Set on successful upload, read on component mount to resume
   * a session after F5 / tab refresh, cleared on successful confirm
   * or on 404 from the resume GET.
   */
  importId: 'spendrop-import-id',
} as const;

export type StorageKey = (typeof STORAGE_KEYS)[keyof typeof STORAGE_KEYS];
