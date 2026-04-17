import type { Role } from '@/lib/roles';
import type { TransactionType } from '@/lib/transaction-types';

export interface User {
  id: number;
  username: string;
  display_name: string;
  role: Role;
  created_at: string;
}

export interface Transaction {
  id: number;
  user_id: number;
  date: string;
  amount: number;
  original_amount: number | null;
  original_currency: string | null;
  description: string;
  category_id: number;
  category_name: string;
  category_type: TransactionType;
  tags: string | null;
  notes: string | null;
  created_at: string;
  updated_at: string;
}

/**
 * A soft-deleted (tombstoned) transaction, as returned by the admin-only
 * Trash view endpoints (`GET /api/transactions/deleted`,
 * `POST /api/transactions/restore-batch`, etc.).
 *
 * Same shape as `Transaction` plus a guaranteed `deleted_at` — the
 * backend's `deletedTransactionResponse` deliberately does NOT use
 * `omitempty` on this field so the recovery surface always shows the
 * exact moment each row was tombstoned, even for the zero-value case.
 */
export interface DeletedTransaction extends Transaction {
  deleted_at: string;
}

/** Paginated response shape for the admin Trash view list endpoint. */
export interface DeletedTransactionList {
  transactions: DeletedTransaction[];
  total: number;
  page: number;
  per_page: number;
}

export interface Category {
  id: number;
  name: string;
  type: TransactionType;
  icon: string | null;
  sort_order: number;
  is_active: boolean;
  created_at: string;
}

export interface Currency {
  code: string;
  name: string;
  symbol: string;
  rate_to_base: number;
  is_base: boolean;
  /** Backend may omit this field on older payloads; callers MUST treat
   *  `undefined` as active so historical rows keep rendering. Only an
   *  explicit `false` hides the currency from entry-row pickers. */
  is_active?: boolean;
  updated_at: string;
}

export interface Budget {
  id: number;
  year: number;
  month: number;
  amount: number;
  updated_at: string;
}

export interface SavingsGoal {
  id: number;
  year: number;
  target_amount: number;
  updated_at: string;
}

export interface DashboardSummary {
  year: number;
  month: number;
  budget: number;
  total_spent: number;
  total_income: number;
  remaining: number;
  savings_this_month: number;
  savings_goal: number;
  savings_ytd: number;
  savings_goal_progress: number;
}

export interface DashboardTrendItem {
  year: number;
  month: number;
  total_spent: number;
  total_income: number;
}

export interface CategoryBreakdownItem {
  id: number;
  name: string;
  total: number;
}

export interface SavedFilter {
  id: number;
  user_id: number;
  name: string;
  filter_json: string;
  created_at: string;
  updated_at: string;
}

export interface PaginatedResponse<T> {
  transactions: T[];
  total: number;
  page: number;
  per_page: number;
}

export interface ImportRow {
  /**
   * Stable per-session index assigned by the backend at upload time.
   * The frontend uses this as the React key and as the {rowID} path
   * parameter for PATCH requests. IDs are 0-indexed and dense: row_id
   * always equals the row's position in the initial upload response's
   * rows array, and never changes for the lifetime of the session.
   */
  row_id: number;
  /**
   * True if the user has marked this row as excluded from the import
   * via the Skip checkbox. Skipped rows still render in the preview
   * (with strikethrough styling — Chunk 5) but are NOT sent to the DB
   * on /confirm and do NOT count toward the unresolved-collisions gate.
   * Skip is sticky: once set, only an explicit un-check clears it. No
   * other edit (including editing a collision row into uniqueness)
   * ever mutates skip client-side — the server is the source of truth.
   */
  skip: boolean;
  /**
   * SHA-256 content hash of (date, amount_cents, description, category)
   * computed by the backend. Used by the collision detector. The
   * frontend treats this as opaque — it is shown in the UI only as a
   * debug tooltip (if at all) and is never hashed client-side.
   *
   * Optional because the backend does not currently emit it on the
   * importRow wire payload; marking required would lie to consumers
   * that the field is always present. When the debug-tooltip feature
   * needs it, the Go side must add a json tag before flipping the
   * type to required.
   */
  content_hash?: string;
  date: string;
  description: string;
  amount: number;
  original_amount?: number;
  original_currency?: string;
  category: string;
  tags?: string;
  notes?: string;
}

/**
 * Why a set of rows share a content hash.
 * - `intra_file`: two or more rows in the current upload session
 *   produce the same hash. The user must edit or skip some of them.
 * - `db_match`: at least one row in the group also matches a live
 *   (non-tombstoned) transaction already in the database. The
 *   `db_match` preview is attached so the UI can show what the
 *   colliding DB row looks like.
 *
 * If a group qualifies as both (two file rows collide AND match a DB
 * row), the backend reports it as `db_match` — the more actionable
 * label (the user can see the DB conflict and decide whether to skip
 * the whole import or edit the rows).
 */
export type CollisionReason = 'intra_file' | 'db_match';

/**
 * A lightweight snapshot of a live DB transaction that collides with
 * an uploaded row. Sent inline on the `CollisionGroup` so the UI can
 * render "this matches: Starbucks, $5.00, Jan 7 2025, Coffee" without
 * a second round-trip.
 */
export interface DbMatchPreview {
  id: number;
  date: string; // ISO date (YYYY-MM-DD)
  description: string;
  amount_cents: number;
  category_name: string;
}

/**
 * A set of rows that share a content hash. The `member_row_ids` array
 * lists the 2+ row_ids in the group (groups of size 1 are not emitted
 * — they are not collisions). The UI renders a header row above each
 * group showing "⚠ N rows collide" and a "Skip all in group" button.
 */
export interface CollisionGroup {
  group_id: string;
  reason: CollisionReason;
  member_row_ids: number[];
  /** Present only when reason === 'db_match'. */
  db_match?: DbMatchPreview;
}

export interface ImportPreview {
  import_id: string;
  row_count: number;
  rows: ImportRow[];
  columns: string[];
  unique_categories: string[];
  /**
   * Every detected collision group from the current session state.
   * Empty on a clean file. Recomputed by the backend on every PATCH
   * and every GET — never derived on the client.
   */
  collision_groups: CollisionGroup[];
  /**
   * ISO-8601 timestamp (UTC) at which the backend will evict this
   * session from the in-memory importStore. The frontend reads this
   * only to show a countdown in the footer (Chunk 5) — it does NOT
   * attempt to refresh the session or warn before expiry.
   *
   * Optional because the backend does not currently emit it on the
   * upload/PATCH/GET wire payload; marking required would lie to
   * consumers that the field is always present. When the countdown
   * feature lands, the Go side must add `"expires_at"` to all three
   * response maps before flipping the type to required.
   */
  expires_at?: string;
}

/**
 * Body for `PATCH /api/import/{importID}/rows/{rowID}`. The backend's
 * per-field validator splits on `field` — all four variants hit
 * different code paths server-side (date/amount go through the
 * existing parsers; description runs length + trim; skip is a raw
 * boolean with no validation).
 */
export interface PatchRowRequest {
  field: 'date' | 'description' | 'amount' | 'skip';
  value: string | boolean;
}

/**
 * Response shape for a successful PATCH. The backend returns the
 * FULL session snapshot (not just the patched row) so the frontend
 * can re-render collision state without a separate GET round-trip.
 * The shape matches `ImportPreview` exactly — we alias it rather
 * than redeclaring it to keep the contract in one place.
 */
export type PatchRowResponse = ImportPreview;

export interface ImportResult {
  imported: number;
  skipped: number;
  total: number;
}

// Reports
export interface YoYMonthEntry {
  month: number;
  expenses: number;
  income: number;
}

export interface YoYResponse {
  current_year: number;
  previous_year: number;
  current: YoYMonthEntry[];
  previous: YoYMonthEntry[];
}

export interface CategoryTrendEntry {
  id: number;
  name: string;
  type: TransactionType;
  data: { year: number; month: number; total: number }[];
}

export interface IncomeExpenseEntry {
  year: number;
  month: number;
  income: number;
  expenses: number;
  net: number;
}

export interface TopMerchantEntry {
  description: string;
  tx_count: number;
  total: number;
}

// --- New Report Types ---

export interface BudgetVsActualEntry {
  month: number;       // 1-indexed, map via MONTH_NAMES_SHORT[month - 1]
  budget: number;
  actual: number;
}

export interface ExpenseVelocityData {
  days_in_month: number;
  budget: number;      // 0 if no budget set
  current: { day: number; daily_total: number }[];
  previous: { day: number; daily_total: number }[];
}

export interface HeatmapEntry {
  date: string;        // ISO date "YYYY-MM-DD"
  total: number;
}

export interface RecurringEntry {
  description: string;
  monthly_avg: number;
  month_count: number;
  annual_total: number;
}

export interface TagBreakdownEntry {
  tag: string;
  total: number;
  count: number;
}

export interface TransactionSuggestions {
  descriptions: string[];
  tags: string[];
}
