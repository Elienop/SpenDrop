# Transaction Entry Currency Selector — Design Spec

## Overview

SpenDrop already has full multi-currency support in the backend and data model: every transaction carries nullable `original_amount` + `original_currency` columns, the admin-managed `currencies` table holds `rate_to_base` for each code, and `resolveCurrency` (`internal/api/transaction_handlers.go:137-179`) converts a foreign amount to the base currency at write time. What is missing is a UI to mark a transaction's original currency at entry time. Today the transaction entry row (`web/src/components/TransactionEntryRow.tsx`) accepts only the base-currency amount, so a household that earns in USD and spends in LBP cannot record the LBP value of a purchase.

This spec introduces a **per-transaction currency selector** on the entry row, on the edit row, and in the list view. Users can pick the original currency from the admin-configured list; the frontend computes the converted amount and sends both `{amount, original_amount, original_currency}` to the backend; the list displays a secondary line under the amount when the original currency differs from the base.

**Design axis:** lightweight, keyboard-first, one new compact control — an amount input with a currency-code suffix that opens a picker Popover. No modals, no wizards, no account-inherited currency, no rate-edit dialog. The backend is authoritative for every ledger number; the frontend only computes a `≈` preview for the pre-save moment.

**Frontend-only change.** The backend API, schema, and conversion logic are untouched.

---

## Non-Goals / Out of Scope

- **Admin currency CRUD UI** — the Settings page that manages the `currencies` table already exists and is not modified.
- **Per-account default currency** (Firefly III pattern) — SpenDrop has no accounts concept; every transaction carries its own currency explicitly.
- **Inline "add new rate" affordance from the entry row** — when a currency has no rate, the user is linked to Settings. Keeps the entry surface thin.
- **Bulk currency edit for historical transactions** — out of scope for this feature.
- **Dashboard / Reports / Exports changes** — these consume `amount_cents` (canonical base-currency value) which is unchanged. Any display of the original currency in reports is a separate feature.
- **Import flow changes** — the import preview already has `original_amount` / `original_currency` plumbing (`web/src/api/types.ts:127-167`). Surfacing those columns in the preview UI is a separate feature.
- **Rate math / `resolveCurrency` changes** — backend stays authoritative.

---

## Data Flow (end-to-end)

```
[1] User opens Transactions page
       ↓
[2] useCurrencies() mounts → GET /api/currencies → module-level promise cache
       ↓
[3] Entry row renders:
     - amount field with currency-code suffix button
     - default currency = localStorage.lastTransactionCurrency ?? baseCode ?? "USD"
     - picker disabled while currencies loading; Save button disabled
       ↓
[4] User types amount (e.g. 150000)
       ↓
[5] (optional) User Tab-focuses the suffix button → Space/Enter → Popover opens →
     searches / selects "LBP" → Popover closes → focus returns to amount input
       ↓
[6] Live preview renders under the input: "≈ $1.67" (only when currency !== base)
     Preview uses rateFor(code) from useCurrencies.
       ↓
[7] User submits the form (Enter on any field or click Add)
       ↓
[8] toCreatePayload({amount: 150000, currency: "LBP", baseCode: "USD", rateFor}) →
     { amount: 1.67, original_amount: 150000, original_currency: "LBP" }
     (if currency === baseCode, payload collapses to { amount: 150000 })
       ↓
[9] POST /api/transactions → backend resolveCurrency recomputes authoritative
     amount_cents; response returns canonical Transaction row
       ↓
[10] Frontend updates localStorage.lastTransactionCurrency = "LBP"; form resets
      with currency sticky
       ↓
[11] Row renders in the list via TransactionRow with AmountDisplay:
      primary line: `$1.67` (base)
      secondary line: `150,000 LBP`
```

**Critical invariant:** the frontend NEVER writes `amount_cents` or computes rounding that the backend will disagree with. The `≈` preview is frontend-approximate; the persisted value is always backend-computed. The post-save row shows exact values (no `≈`).

---

## File Structure

**Created (frontend only):**
- `web/src/hooks/useCurrencies.ts` — module-level promise cache of `GET /api/currencies`, returns `{ list, baseCode, rateFor, loading, error }`.
- `web/src/hooks/useCurrencies.test.ts` — cache, loading, `rateFor` null paths.
- `web/src/components/AmountCurrencyInput.tsx` — composed control: amount `<Input>` + inline currency-code `<Button>` suffix that opens a `<Popover>` + `<Command>` picker; renders the `≈` preview below.
- `web/src/components/AmountCurrencyInput.test.tsx` — unit tests (below).
- `web/src/components/AmountDisplay.tsx` — read-only display for list rows: primary formatted amount + optional secondary `original_amount original_currency` line.
- `web/src/components/AmountDisplay.test.tsx` — unit tests (below).
- `web/src/lib/currency.ts` — pure helpers: `toCreatePayload(values, baseCode, rateFor)`, `toEditDefaults(tx, baseCode)`, `PREVIEW_DECIMALS` constant.
- `web/src/lib/currency.test.ts` — helper unit tests.

**Modified:**
- `web/src/components/TransactionEntryRow.tsx` — replace the bare `<Input type="number">` at lines 289-311 with `<AmountCurrencyInput>` (passing `hideInactive: true`); extend Zod schema (line 85-91) with `currency: z.string().regex(/^[A-Z]{3}$/)`; add `'currency'` to the Tab-navigation order (line 227); add module-local `getLastCurrency` / `saveLastCurrency` helpers in the same file alongside the existing `getLastDate` / `saveLastDate` / `getLastCategoryId` / `saveLastCategory` helpers (lines 47-66).
- `web/src/components/TransactionEntryRow.test.tsx` — extend for default currency, payload shape, tab order, sticky persistence.
- `web/src/components/TransactionRow.tsx` — replace the inline amount `<span>` formatter with `<AmountDisplay>`.
- `web/src/components/TransactionRow.test.tsx` — regression test that base-currency rows (original_* null) render unchanged.
- `web/src/lib/storage-keys.ts` — add `lastTransactionCurrency: 'spendrop-last-currency'`.

**Unchanged:**
- `internal/api/transaction_handlers.go` (`resolveCurrency` at line 137, request shape at line 58, response shape at line 71).
- `internal/database/queries.sql` / generated bindings.
- `web/src/hooks/useBaseCurrency.ts` — still used; `useCurrencies` returns the same `baseCode` from its own fetch response so callers can pick either hook.
- Dashboard, Reports, Trash, Export — all consume `amount_cents`.

---

## Interfaces

### `useCurrencies()` hook

Module-level promise cache, mirroring the existing `useBaseCurrency.ts` pattern. One network request per session; the list rarely changes.

```ts
interface UseCurrenciesResult {
  /** Active currencies (Settings → Currencies). Inactive ones are excluded from new-entry picker but see edit-mode notes. */
  list: Currency[];
  /** Code of the is_base currency, or DEFAULT_CURRENCY ("USD") fallback. */
  baseCode: string;
  /** Returns rate_to_base for `code`, or null if the code is unknown or has null/zero rate. */
  rateFor: (code: string) => number | null;
  loading: boolean;
  error: string | null;
}

export function useCurrencies(): UseCurrenciesResult;
```

### `<AmountCurrencyInput />`

Composed control: amount input + currency-code suffix button + `≈` preview. Two separate callbacks (Wise Neptune `MoneyInput` pattern — parent owns the joint `{amount, currency}` state object).

```ts
interface AmountCurrencyInputProps {
  value: number;                       // amount in its own currency (raw typed value, NOT converted)
  onValueChange: (v: number) => void;
  currency: string;                    // ISO code (e.g. "USD", "LBP")
  onCurrencyChange: (code: string) => void;
  baseCode: string;
  /**
   * FULL list from useCurrencies (including inactive). The component renders
   * an `(inactive)` suffix on any entry with `is_active === false` and
   * disables entries for which `rateFor(code) === null`. The parent does
   * NOT pre-filter — mode-specific filtering lives in the component so
   * entry and edit surfaces share one implementation. See Edge Case #1.
   */
  currencies: Currency[];
  /**
   * If true, the picker hides entries whose `is_active === false`. Entry
   * mode passes `true`; edit mode passes `false` so the stored-but-now-
   * inactive currency on a historical row is still selectable for round-trip.
   */
  hideInactive: boolean;
  rateFor: (code: string) => number | null;
  loading?: boolean;                   // disables the picker + shows spinner
  error?: string | null;               // inline error text
  disabled?: boolean;                  // for submit-in-flight
  inputRef?: React.Ref<HTMLInputElement>;
  dataEntryField?: string;             // Tab-navigation hook used by TransactionEntryRow
}
```

**Rendering:**
- Base-currency pick: no `≈` preview (redundant).
- Non-base pick with valid `rateFor`: `≈ $X.XX` under the input, using `formatCurrency(value / rateFor(code), baseCode)`.
- Non-base pick with null `rateFor`: picker entry is disabled (`aria-disabled` + tooltip `"No exchange rate configured — set in Settings"`); if somehow selected (edit mode of an older row), inline error + Save blocked.
- **Inactive currencies:** when `hideInactive === true` (entry mode), entries with `is_active === false` are not rendered at all. When `hideInactive === false` (edit mode), they render with an `(inactive)` suffix after the code and remain selectable for round-trip fidelity.

**Keyboard:**
- The amount input owns text focus.
- Suffix button is its own Tab stop; Space/Enter opens the Popover; ArrowDown moves into the list; Esc closes; selection closes and returns focus to the amount input.
- Enter inside the Command list selects and does NOT submit the enclosing form.

### `<AmountDisplay />`

Read-only list-row display. Uses existing `formatCurrency` from `web/src/lib/format.ts`.

```ts
interface AmountDisplayProps {
  amount: number;                      // canonical base-currency amount (from tx.amount)
  originalAmount: number | null;       // from tx.original_amount
  originalCurrency: string | null;     // from tx.original_currency
  type: TransactionType;               // for sign + color (expense vs income)
  baseCode: string;
}
```

**Rendering:**
- `originalAmount === null` OR `originalCurrency === null` OR `originalCurrency === baseCode` → single line: `formatCurrency(amount, baseCode)`.
- Otherwise → two lines: primary base amount + secondary `{originalAmount} {originalCurrency}` in muted color. No `≈` — both values are canonical after save.

### `lib/currency.ts` helpers

```ts
/**
 * Collapses to { amount } when currency === baseCode; otherwise produces
 * the full payload. Generic over the caller's value object so
 * TransactionEntryRow and the edit form can both pass their own field set
 * (description, category_id, tags, ...) through without losing types.
 */
export function toCreatePayload<
  T extends Record<string, unknown> & { amount: number; currency: string },
>(
  values: T,
  baseCode: string,
  rateFor: (code: string) => number | null,
): Omit<T, 'currency'> &
  ({ amount: number } | { amount: number; original_amount: number; original_currency: string });

/** For edit mode: derive initial form values from a saved Transaction. */
export function toEditDefaults(
  tx: Transaction,
  baseCode: string,
): { amount: number; currency: string };

/** Number of decimal places shown in the `≈` preview. */
export const PREVIEW_DECIMALS = 2;
```

**`toCreatePayload` rules:**
- `currency === baseCode` → strips `currency` from `values` and returns `{ ...rest, amount: values.amount }` (no `original_*`).
- `currency !== baseCode` AND `rateFor(currency) > 0` → strips `currency` and returns `{ ...rest, amount: round(values.amount / rate, 2), original_amount: values.amount, original_currency: currency }`.
- `currency !== baseCode` AND `rateFor(currency)` null → throws `Error("no rate")` — caller is responsible for disabling Save before this path.

**Edit path uses the same `toCreatePayload`.** The edit form invokes `toCreatePayload` on Save with the current form values; a re-saved transaction gets the same collapse / expand treatment as a new one (so switching back to base currency on edit correctly drops `original_*` on the wire).

**`toEditDefaults` rules:**
- `tx.original_amount != null && tx.original_currency != null` → `{ amount: tx.original_amount, currency: tx.original_currency }`.
- Else → `{ amount: tx.amount, currency: baseCode }`.

---

## Edge Cases & Error Handling

1. **Inactive / delisted currency on a historical transaction.**
   - New-entry picker never lists inactive currencies.
   - Edit-mode picker shows the stored code with an `(inactive)` suffix so the round-trip is preserved; the user can keep it on save, or pick an active one.
   - Strictly improves on Firefly III's "block disable entirely" constraint (issue #2515).

2. **Missing / zero `rate_to_base` (`rateFor() === null`).**
   - Picker entry renders disabled + tooltip `"No exchange rate configured — set in Settings"`.
   - If a row was saved earlier with a currency whose rate was later deleted and the user opens the edit: the picker trigger shows the stale code; an inline error appears; Save is blocked until a valid currency is picked or Settings is updated.
   - **Avoids Firefly III's silent `rate=1` fallback** (documented in their exchange-rates page, observed as a correctness bug in issue #11616).

3. **Unknown currency code on edit (code deleted from the `currencies` table entirely).**
   - Same handling as #2 — inline error, Save blocked. The backend would reject the save with `"unknown currency"` anyway (`transaction_handlers.go:151`); the client guards ahead of that.

4. **Currencies list still loading at render time.**
   - `AmountCurrencyInput` renders with picker trigger disabled + small spinner.
   - The amount input remains fully usable.
   - Form Save button disabled until `useCurrencies.loading === false`. Prevents a race where the user submits while the base code is unknown.
   - On load, the currency auto-fills from the fallback chain: `localStorage.lastTransactionCurrency` → `baseCode` → `"USD"`.

5. **No base currency configured at all (`is_base = true` on zero rows).**
   - `useCurrencies.baseCode` falls back to `DEFAULT_CURRENCY` (`"USD"`), consistent with the existing `useBaseCurrency.ts` behavior. We do not introduce a new banner or special-case — if the rest of the app tolerates this, so does the entry row.

6. **Currency change mid-entry (typed amount already present).**
   - **Invariant (design rule):** currency change NEVER mutates the typed amount. The `≈` preview recomputes only.
   - Firefly III issue #10791 is the canonical failure mode to avoid: their dropdown clears to `(none)` but leaves a stale `foreign_amount=5`. Our separate-callbacks interface (`onValueChange` + `onCurrencyChange` — Wise Neptune pattern) with parent-owned joint state eliminates the path.
   - **Focus-guarded reformatting** (Neptune `MoneyInput.js:66-76` pattern): while the amount input is focused, changing the currency selector does NOT trigger any reformatting or re-parsing of the amount's text. The numeric value passes through untouched to the preview.

7. **Preview precision vs. persisted amount.**
   - `AmountCurrencyInput` preview uses `≈` explicitly and 2-decimal rounding (`PREVIEW_DECIMALS`).
   - `AmountDisplay` post-save row does NOT use `≈` — the value came from the backend and is canonical.
   - The backend's own rounding (`math.Round(converted*100)/100` at `transaction_handlers.go:173`) matches the frontend's preview rounding, so the pre-save preview and post-save display agree to the cent.
   - **Known limitation: 2-decimal assumption.** The `PREVIEW_DECIMALS = 2` constant assumes the base currency has 2 fractional digits — true for USD, EUR, LBP, and most common currencies, but not for JPY / KRW (0) or BHD / KWD (3). This matches the backend's rounding and the existing `formatCurrency` helper. Non-2-decimal base currencies are out of scope for this feature; if a household ever configures one as their base, both the preview and the backend's `amount_cents` rounding will be wrong together, and it's a separate ticket to fix both surfaces in lockstep.

8. **`currency === baseCode` collapse.**
   - **Invariant:** when the selected currency equals `baseCode`, the submit payload MUST NOT include `original_amount` or `original_currency` — only `amount`. `toCreatePayload` enforces this.
   - Firefly III has this invariant conceptually but leaks zombie rows where `foreign_amount` is set without `foreign_currency` (issue #10791). Our client-side collapse prevents the leak; `AmountDisplay` has a defensive fallback that still renders single-line if it ever encounters `original_currency === baseCode` from a legacy row.

9. **Save while the suffix Popover is open.**
   - Pressing Enter inside the Command list selects the highlighted currency and closes the Popover; it does NOT submit the form. This mirrors the existing category Popover in the same file (`TransactionEntryRow.tsx:361-365`).
   - Pressing Ctrl/Cmd+Enter anywhere on the form still submits (existing behavior at line 206).

10. **Sticky-currency across sessions.**
    - On successful save, `localStorage.lastTransactionCurrency = submittedCurrency`. Parallels the existing `saveLastDate` / `saveLastCategory` helpers.
    - On first-ever entry (empty localStorage), default is `baseCode` (resolved once `useCurrencies` loads).

---

## Testing Strategy (frontend)

Vitest + Testing Library. All tests assert on integer minor-units or JSON payload shape, never on snapshot-diffed formatted currency strings (anti-pattern from Firefly III's lack of contract tests).

### `useCurrencies.test.ts`
- **Module-level cache:** two consumers mounted in sequence receive the same promise and trigger one `fetch`.
- **`rateFor` happy path:** returns `rate_to_base` for an active currency.
- **`rateFor` null paths:** returns `null` for unknown code, null `rate_to_base`, or `rate_to_base === 0`.
- **Loading transition:** `loading: true → false` once the fetch resolves.
- **API error:** `error` surfaces; no throw during render.

### `AmountCurrencyInput.test.tsx`
- Renders with default currency = `baseCode`.
- Typing an amount calls `onValueChange` with the numeric value (not the formatted string).
- Clicking the suffix button opens the Popover; selecting a currency closes it and calls `onCurrencyChange`.
- `≈` preview renders only when `currency !== baseCode`; absent when equal.
- Preview recomputes when amount changes, and when currency changes (amount preserved — **Firefly III #10791 guard**).
- Currencies with `rateFor() === null` render disabled in the picker (`aria-disabled` + tooltip).
- `loading: true` → picker disabled, spinner visible.
- **`*_FocusPreservesRawInput`**: while the amount input has focus, switching the currency does NOT reformat or mutate the input's DOM value (**Neptune pattern**).
- Keyboard: Tab reaches picker; Space/Enter opens; Enter inside Command list selects but does not submit the parent form.

### `AmountDisplay.test.tsx`
- `originalAmount === null` → single-line render (regression guard for existing rows).
- `originalAmount` set and `originalCurrency !== baseCode` → secondary line `"150000 LBP"` visible.
- `originalCurrency === baseCode` → defensive fallback to single-line (should never reach this if `toCreatePayload` is correct, but covered).
- Expense renders negative/red; income renders positive/green (delegates to existing formatter).

### `lib/currency.test.ts`
- **`*_BothOrNeither`**: `toCreatePayload` returns both `original_amount` and `original_currency` together, or neither. Never one-without-the-other (**Firefly III #10791 class**).
- **`*_SameAsBaseCollapsesField`**: picking `baseCode` → output has no `original_*` fields at all (not `null`, not `undefined` as keys — absent from the object).
- **`*_RateOneIsExplicit`**: non-base currency with `rate_to_base === 1.0` still sends `original_*` fields; only `currency === baseCode` triggers collapse (**Firefly III #11616 class**).
- **`*_NoRateThrows`**: `toCreatePayload` throws when `rateFor(code)` is `null`; caller is expected to gate on this.
- **`toEditDefaults`**: returns `(original_amount, original_currency)` when both present; otherwise `(amount, baseCode)`.

### `TransactionEntryRow.test.tsx` (extend existing)
- First-render default currency follows `localStorage.lastTransactionCurrency` → `baseCode` → `"USD"` chain.
- Submit with currency === base: payload has `{amount}`; `original_amount` and `original_currency` absent.
- Submit with currency !== base: payload has `{amount: computed, original_amount: typed, original_currency: code}`; `amount === round(typed / rateFor(code), 2)`.
- After successful submit, `localStorage.lastTransactionCurrency` equals submitted currency.
- Tab order regression guard: `date → amount → currency → description → category → submit`.
- Save disabled while `useCurrencies.loading === true`; re-enables on resolve.
- Selected currency with `rateFor() === null` → Save disabled, inline error visible.

### `TransactionRow.test.tsx` (extend existing)
- Row with `original_amount: 150000, original_currency: "LBP"` renders two lines.
- Row with null `original_*` fields renders single-line (regression guard — no existing behavior changes).

### Edit round-trip tests
- **`*_RoundTripExactBits`**: create → reload → integer minor-units equal; parametrized over `[expense, income]` (**Firefly III #2180 symmetry guard**).
- Opening edit on a transaction with `original_*` fields prefills picker with `original_currency` and amount input with `original_amount`.
- Changing currency on edit recomputes `amount` on save.
- Editing a row whose stored currency is now inactive: picker shows `(inactive)` suffix; Save without changing preserves the code.

### Deliberately NOT tested (out of scope)
- Backend rate math and `resolveCurrency` — already covered by `transaction_handlers_test.go`.
- Admin currency CRUD — separate feature.
- Dashboard / Reports / Export rendering — they consume `amount_cents`, unchanged.
- Tag input, autocomplete, category Popover — unchanged surfaces.

---

## Prior-Art Notes (for reviewers)

The design is informed by a systematic review of six tools (Firefly III, GnuCash, Wise Neptune, Actual Budget, YNAB, Beancount). Full research notes are in the session transcript; the load-bearing conclusions are embedded throughout this spec. Key takeaways:

- **Wise Neptune's `MoneyInput`** (`@transferwise/neptune`) is the canonical integrated amount + currency control. Borrowed: separate `onAmountChange` / `onCurrencyChange` callbacks with parent-owned joint state; `≈` symbol on pre-submission conversions; focus-guarded reformatting.
- **GnuCash's `utest-Split.cpp` permutation matrix** is the canonical currency-pair test pattern. Borrowed: integer minor-unit assertions, exhaustive `{currency === base, currency !== base}` coverage, explicit collapse-path test.
- **Firefly III's bugs** (#10791, #11616, #2180) are the canonical failure modes. Their root cause is an untested invariant surface — all foreign-currency validation rules in Firefly exist but have zero test coverage. We encode those invariants as tests here (`*_BothOrNeither`, `*_SameAsBaseCollapsesField`, `*_RateOneIsExplicit`, `*_RoundTripExactBits`).
- **GnuCash's modal rate-entry dialog** was evaluated and rejected. Keyboard-first inline disable + Settings link fits SpenDrop's spreadsheet-style entry flow better.
- **Firefly III's always-visible twin-field layout** was evaluated and rejected in favor of the integrated suffix + live `≈` preview. The preview is redundant when `currency === baseCode` and can be cleanly hidden; Firefly's layout has no such compaction.
