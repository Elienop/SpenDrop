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
  /**
   * Who entered this row. It names `user_id`, which a member cannot resolve
   * themselves because `GET /users` is admin-only — before this field they
   * learned a row was somebody else's only when Save came back 403.
   *
   * Carries `users.display_name`, not `users.username`: display_name is what
   * this app shows wherever it names a person. Render it as a plain name —
   * NOT in the Sidebar's `@handle` idiom, which is reserved for the login
   * identifier.
   *
   * Always present on the wire (the backend emits it without `omitempty`),
   * so there is no absent case to distinguish. The empty string means the
   * creator's user row is gone — render a neutral fallback, never a blank.
   *
   * Display only: it changes no ownership or authorization semantics.
   */
  created_by: string;
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
 *
 * `created_by` is inherited and REQUIRED: `deletedTransactionResponse` is a
 * SEPARATE struct from `transactionResponse` (see the comment above it), and
 * it now emits the attribution field on both list paths — the admin-wide
 * `ListDeletedTransactions` and the member-scoped
 * `ListDeletedTransactionsByUser` — via a `LEFT JOIN users`, without
 * `omitempty`. Until it did, this interface `Omit`-ed the field so Trash code
 * could not read `tx.created_by`, type-check clean, and get `undefined` at
 * runtime — the same shape as the migration-010 money regression. Keep the
 * field required rather than optional: an optional field would restore
 * exactly that hole. As on `Transaction`, the empty string means the
 * creator's user row is gone (the LEFT JOIN found nothing) and must render a
 * neutral fallback, never a blank.
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

/**
 * A per-category spending limit for a single month, as returned by
 * `GET /api/category-budgets?year=&month=`. `amount` is in whole dollars
 * (the backend converts from its integer-cents storage on the wire), so
 * the panel can bind it directly to a `<Input type="number">` without a
 * /100 conversion. Categories with no limit set are simply absent from
 * the array — there is no zero-amount row — so a missing `category_id`
 * means "no limit", which the editor renders as a blank field.
 */
export interface CategoryBudget {
  category_id: number;
  amount: number;
}

/**
 * Body for `PUT /api/category-budgets/{year}/{month}/{categoryId}`.
 * `amount` is in whole dollars and must be strictly greater than 0 — the
 * backend rejects `<= 0`, and clearing a limit is expressed by a DELETE
 * rather than a PUT with `amount: 0`.
 */
export interface CategoryBudgetRequest {
  amount: number;
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
  limit: number | null;
  over: boolean;
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

/**
 * The fields an uploaded row can carry that are longer than SpenDrop
 * stores. Only these three have a length bound worth reporting: `date`
 * and `amount` are parsed, so an over-long value fails as unparseable
 * long before length matters.
 */
export type ImportFieldErrorField = 'description' | 'tags' | 'notes';

/**
 * One field on one row whose value exceeds the limit the rest of the
 * API enforces. Upload, PATCH and GET report these on the preview so
 * the UI can flag a row on load rather than only after a failed
 * confirm; confirm reports them again in its 409 body.
 *
 * Carries its own explanation. The remedies genuinely differ between
 * fields — a description is editable in the preview table ("shorten it
 * here") while tags and notes have no cell to point at and can only be
 * skipped or fixed in the source spreadsheet — but the backend writes
 * both, so this side never composes one. See `message` below.
 *
 * Where the error is SHOWN is still ours to decide: `description` goes
 * in its cell, the rest in a detail line under the row. That split is
 * `isEditableInPreview` in `lib/import-field-errors.ts`.
 */
export interface ImportFieldError {
  row_id: number;
  field: ImportFieldErrorField;
  /**
   * Server-authored explanation for this one field, rendered VERBATIM.
   * Do not reword it and do not compose an alternative: the same cell
   * can be filled from two directions — this list, and the 400 body of
   * a rejected PATCH — and the backend emits one string for both
   * (`importFieldLengthMessage`, reused by `validateImportField`). Any
   * wording written on this side would match only by coincidence, and
   * only until either copy was edited.
   *
   * It is also why this side holds no length constants: the sentence
   * arrives with the number already in it, correct by construction.
   *
   * Optional so a response that omits it still type-checks;
   * `fallbackFieldTooLongMessage` covers that case with a numberless
   * sentence rather than a guessed bound.
   */
  message?: string;
}

/**
 * Why a category value on an uploaded row has no decision behind it yet.
 * - `unmapped`: the cell names a category the household does not have.
 *   Remedy: pick a target for that name.
 * - `missing`: the cell is empty. Remedy: pick a default category.
 *
 * The two are separate because their remedies are separate controls, and a
 * single "unresolved" label would point the user at the wrong one.
 */
export type UnresolvedCategoryReason = 'unmapped' | 'missing';

/**
 * One distinct category value in the upload that nothing resolves. `name` is
 * `''` for the missing-cell case; `row_ids` is every non-skipped row carrying
 * it, which is what turns "one category needs mapping" into "…and it covers
 * 312 of your 400 rows".
 *
 * Emitted on every preview response computed as if the user had decided
 * NOTHING — so the list is stable and the client marks each entry resolved
 * against its own mapping state. Rows the backend would reject before ever
 * consulting their category (unparseable date, empty description, zero
 * amount) are excluded, which is what stops a trailing "TOTAL" row from
 * demanding a category it will never use.
 */
export interface UnresolvedCategory {
  name: string;
  reason: UnresolvedCategoryReason;
  row_ids: number[];
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
   * Every field on every row that is longer than SpenDrop stores.
   * Recomputed by the backend alongside `collision_groups`, so a row
   * edited back under the limit drops out of this array on the PATCH
   * response and its flag clears without any client-side bookkeeping.
   *
   * Optional for the same reason `expires_at` below is optional: the
   * field is only meaningful once the Go side emits it on all three
   * response maps (upload / PATCH / GET). Marking it required would
   * tell consumers it is always present, and a preview built before
   * that lands would type-check while being `undefined` at runtime.
   * Every read goes through `activeFieldErrorRowIDs`, which treats
   * `undefined` as "none".
   */
  field_errors?: ImportFieldError[];
  /**
   * Every category value in the upload that no decision covers. Recomputed
   * by the backend alongside `collision_groups`, so skipping the last row
   * carrying an undecided name drops the entry on the PATCH response and
   * the gate clears with no client-side bookkeeping.
   *
   * Optional for the same reason `field_errors` is: a preview built before
   * the Go side emitted it would type-check while being `undefined` at
   * runtime. Every read treats `undefined` as "nothing to decide".
   */
  unresolved_categories?: UnresolvedCategory[];
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
  /**
   * `skipped` split by cause, counts keyed by reason. A bare "488 skipped"
   * is a number the user cannot act on and cannot distinguish from a broken
   * import; this is what makes it explicable.
   *
   * Keys are the backend's own reason strings — `duplicate`,
   * `unparseable_date`, `empty_description`, `zero_amount`, `sign_mismatch`
   * (amount and original_amount carry opposite signs), `missing_category`,
   * `field_too_long` — plus `user_skipped` (rows the
   * user skipped in the preview) and `error`. Only non-zero causes appear,
   * so a consumer must render whatever keys arrive rather than a fixed list,
   * and the counts sum to `skipped`.
   */
  skipped_reasons?: Record<string, number>;
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
  /**
   * The day's NET expense total: signed, so a day whose refunds outweigh its
   * spending arrives as a negative number and a day that was fully refunded
   * arrives as an exact zero.
   */
  total: number;
  /**
   * How many live expense rows the day holds. NOT derivable from `total` once
   * amounts are signed — a zero total means "refunded to nothing" on a day
   * with rows and "nothing happened" on a day without, and the heatmap paints,
   * announces and fetches differently for the two. Days with no rows produce
   * no entry at all, so this is never 0 on the wire; the field exists so the
   * client never has to infer rows from money.
   */
  txn_count: number;
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

/**
 * A user-owned API token, as returned by `GET /api/api-tokens`. The list
 * endpoint deliberately omits the full plaintext (`token`) — that value
 * is only on the create response, returned once and never again. If you
 * find yourself reaching for a `token` field here, you're mixing the
 * list type with the create-response type; use `CreateTokenResponse`.
 */
export interface ApiToken {
  id: number;
  name: string;
  token_prefix: string;
  created_at: string;
  last_used_at: string | null;
  last_used_ip: string | null;
  expires_at: string | null;
}

export interface ListTokensResponse {
  tokens: ApiToken[];
}

/**
 * Body for `POST /api/api-tokens`. `expires_at` is either `null` (never
 * expires) or a pre-computed UTC ISO-8601 timestamp derived client-side
 * from the dropdown choice. The server rejects past or >10y-future
 * timestamps — the spec chose a closed enum on the client, so the only
 * way to pass validation is to emit one of the pre-computed offsets.
 */
export interface CreateTokenRequest {
  name: string;
  expires_at: string | null;
  password: string;
}

/**
 * Response body for `POST /api/api-tokens`. Carries the full plaintext
 * in `token` — present ONLY on the 201 create response, never on a list
 * fetch. The one-time-view contract is enforced structurally: the list
 * type (`ApiToken`) does not include this field, so TypeScript catches
 * any attempt to display a stored list row as if it had a plaintext
 * token attached.
 */
export interface CreateTokenResponse extends ApiToken {
  token: string;
}

/**
 * Response body for `DELETE /api/api-tokens/{id}`. Chunk 4 handler
 * emits 200 OK with `{"ok": true}` on success — we type the response
 * rather than using `api.del<void>` so a future handler change (e.g.
 * adding a `revoked_at` echo) surfaces as a TS error instead of a
 * silently dropped field.
 */
export interface RevokeOneResponse {
  ok: boolean;
}

/**
 * Response body for `DELETE /api/api-tokens` (mass revoke). Chunk 4
 * emits 200 OK with `{"revoked": <count>}` — the count is used in the
 * success toast so the user sees how many integrations they broke.
 */
export interface RevokeAllResponse {
  revoked: number;
}

// ---------- Password change / reset ----------

/**
 * Body for `POST /api/auth/password` (self-service change). The caller
 * proves possession of the account by supplying its current password;
 * the server verifies it before any mutation. A wrong current password
 * comes back as 401 (`invalid credentials`), a too-short / too-long new
 * password as 400 with the bound message in `{error}`.
 */
export interface ChangePasswordRequest {
  current_password: string;
  new_password: string;
}

/**
 * Body for `POST /api/users/{id}/reset-password` (admin reset). No
 * current-password check — the admin's authority is the authorization.
 */
export interface ResetPasswordRequest {
  new_password: string;
}

/**
 * Response body for `POST /api/auth/password`. The cascade revokes every
 * live API token and deletes every session for the caller (including the
 * one making this request), so the frontend must log out + redirect to
 * /login on success. `tokens_revoked` is surfaced in the success toast so
 * the user knows how many integrations they just broke.
 */
export interface ChangePasswordResponse {
  status: 'password_changed';
  tokens_revoked: number;
}

/**
 * Response body for `POST /api/users/{id}/reset-password`. Same cascade as
 * the self-service change, run against the target user: the target is
 * signed out everywhere and their API tokens revoked.
 */
export interface ResetPasswordResponse {
  status: 'password_reset';
  tokens_revoked: number;
}

// ---------- Bulk-edit ----------

// BulkUpdatePatch is the partial-update payload. Keys absent = no change.
// Mirrors the backend patchRequest in internal/api/transaction_handlers.go.
export interface BulkUpdatePatch {
  date?: string;
  description?: string;
  category_id?: number;
  tags?: string;
}

export type BulkUpdateTagsMode = 'add' | 'remove' | 'replace';

export interface BatchUpdateRequest {
  ids: number[];
  patch: BulkUpdatePatch;
  tagsMode?: BulkUpdateTagsMode;
}

export interface BulkUpdateByFilterRequest {
  patch: BulkUpdatePatch;
  tagsMode?: BulkUpdateTagsMode;
}

// BulkUpdateResponse is shared between batch-update and update-by-filter.
// `skipped` is present only on batch-update (filter-mode's WHERE clause
// already excluded skips, so the response omits the field).
export interface BulkUpdateResponse {
  updated: number;
  skipped?: number;
}

// ---------- Notification preferences (household-wide) ----------

/**
 * Household-wide push notification preferences, as returned by
 * `GET /api/push/preferences` and accepted by `PUT /api/push/preferences`.
 *
 * Each boolean is a per-type send gate keyed by the notification type id
 * shared with the backend fan-out (`over_budget`, `txn_added`, … — see
 * `internal/api/notifications.go`). There is exactly one household row, so
 * this is a flat object, not a list.
 *
 * `large_txn_threshold_dollars` crosses the wire in whole/fractional
 * DOLLARS (the backend stores integer cents and converts at the handler
 * via `centsToDollars`/`dollarsToCents`). NEVER expect a `*_cents` field
 * here — bind this value directly to a `<Input type="number">`.
 *
 * PUT is admin-only server-side (403 for members); the hook also gates the
 * call client-side via `canEdit` so members never fire a doomed request.
 */
export interface NotificationSettings {
  over_budget: boolean;
  txn_added: boolean;
  txn_deleted: boolean;
  txn_edited: boolean;
  large_txn: boolean;
  large_txn_threshold_dollars: number;
  digest_mode: string;
  /** Daily-digest send time as a 24h `HH:MM` string (backend NOT NULL default
   *  `"08:00"`). Only meaningful when `digest_mode === "daily"`; the backend
   *  always emits a valid value, never empty. */
  digest_time: string;
  quiet_start: string;
  quiet_end: string;
  quiet_tz: string;
  quiet_allow_over_budget: boolean;
}

// ---------- Reports year picker ----------

/**
 * Wire contract of `GET /api/reports/years` — every year the Reports and
 * Dashboard year pickers may offer, derived from the ledger rather than a
 * hard-coded constant (see `internal/api/reports_years_handlers.go`).
 *
 * Replaces `GET /api/settings/report-year-floor`, which could only express a
 * contiguous range from a single floor. A ledger is not contiguous, and a
 * single integer had nowhere to put the years it had to drop.
 */
export interface ReportYearsResponse {
  /**
   * Every year the picker may offer, newest first. The handler guarantees
   * each element is inside the data window, that none is greater than
   * `current_year`, and that `current_year` itself is always present — so the
   * list is never empty.
   */
  years: number[];
  /**
   * The SERVER's current year. Sent so the client does not have to trust its
   * own clock to work out which entry is "this year": `years` is capped by the
   * server's clock, so a browser that straddles a New Year boundary with it
   * would otherwise point at a year the list does not contain.
   */
  current_year: number;
  /**
   * False only when the ledger holds no LIVE rows at all. It tracks ROWS, not
   * offered years — a household whose only row is dated 3021 still has
   * transactions, and the UI needs to tell "you have no data" apart from "you
   * have data, none of it reportable".
   */
  has_transactions: boolean;
  /**
   * Every dropped year that is OUTSIDE the data window [1900, 2100]. Newest
   * first, deduplicated, and EMPTY for the ordinary household.
   *
   * The DEFECT bucket: legacy or corrupt rows. Every year-param endpoint 400s
   * on these (measured against a live server for 1850 and 3021), and no
   * passage of time changes that.
   *
   * Not optional, deliberately. This is the honesty signal: it is the only
   * thing that tells the user a row exists which no report can reach. Making
   * it optional would let a consumer skip it with `?.` and reintroduce exactly
   * the silent-drop bug this endpoint was built to kill.
   */
  out_of_range_years: number[];
  /**
   * Every dropped year that is later than `current_year` but still INSIDE the
   * data window. Newest first, deduplicated, and EMPTY for the ordinary
   * household.
   *
   * The FEATURE bucket, and the reason this is a separate key. A planned 2027
   * bill is normal: `POST /api/transactions` accepts the date, and every
   * year-param endpoint answers for 2027 with the amount present. Only the
   * picker's own cap withholds it, and that cap lifts on 1 January 2027.
   *
   * Consumers must NOT merge this with `out_of_range_years`. They used to be
   * one list, and the Reports notice consequently told a user their deliberate
   * plan was corrupt data. A year that qualifies as both — 3021 — is in
   * `out_of_range_years` ONLY; the server applies that precedence, so a
   * consumer can render both without double-naming a year.
   */
  future_years: number[];
}

// `ReportYearFloorResponse` (GET /api/settings/report-year-floor) lived here
// and is gone: nothing in this app reads that route any more. The ROUTE itself
// deliberately stays on the server for one more release — a stale PWA bundle
// keeps calling it until its service worker updates, and 404ing it now would
// degrade those users to a one-year picker.
