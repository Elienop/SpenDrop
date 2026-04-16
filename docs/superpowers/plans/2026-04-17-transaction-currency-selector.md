# Transaction Currency Selector Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-transaction currency selector to the entry row, the edit row, and the list view so households can record foreign-currency transactions (e.g. LBP) while the backend continues to store authoritative base-currency values.

**Architecture:** Frontend-only. One new hook (`useCurrencies`), one pure-helper module (`lib/currency.ts`), two new components (`AmountCurrencyInput`, `AmountDisplay`), and targeted integration at the three existing surfaces (`TransactionEntryRow`, `TransactionRow` view + `TransactionRow` edit). The backend is authoritative for every ledger number; the frontend computes only an `≈` preview before save. Spec: `docs/superpowers/specs/2026-04-17-transaction-currency-selector-design.md`.

**Tech Stack:** React 19 + TypeScript, Vite, Vitest + Testing Library, shadcn/ui (Popover + Command + Input + Button), Tailwind, react-hook-form + Zod.

**Branch:** `feat/transaction-currency-selector` (already created off `main`, ahead of origin/main by 2 commits — the spec commits `f4d3fb4` and `d88a159`).

---

## Context for implementers (READ FIRST)

**This plan assumes zero codebase context.** Read this section before starting any task.

### Backend contract (unchanged)

- `POST /api/transactions` accepts optional `original_amount` (number) + `original_currency` (string ISO code). If both present and `original_currency !== base`, the backend runs `resolveCurrency` (`internal/api/transaction_handlers.go:137-179`): it fetches the currency row, divides `original_amount / rate_to_base`, rounds to 2 decimals, and stores that as `amount_cents`. If `original_currency === base` or is empty, it uses `amount` directly.
- Response shape (`Transaction`) already exposes `original_amount: number | null` and `original_currency: string | null` (`web/src/api/types.ts:12-27`).
- `GET /api/currencies` returns `Currency[]` shape `{ code, name, symbol, rate_to_base, is_base, updated_at }` (`web/src/api/types.ts:61-68`). Admin-managed via Settings page.
- `CreateTransactionInput` in `web/src/hooks/useTransactions.ts:22-31` **already has** `original_amount?: number` and `original_currency?: string` optional fields. **No type changes needed there.**

### Existing patterns to mirror

- **Module-level promise cache for currency fetch** — see `web/src/hooks/useBaseCurrency.ts` (lines 11-24). This is the canonical pattern for a once-per-session API fetch. `useCurrencies` must use the same pattern (own cache variable — not a shared one with `useBaseCurrency`; they'll re-hit the endpoint once each on the first page that mounts either, which is fine).
- **localStorage sticky helpers** — see `getLastDate` / `saveLastDate` / `getLastCategoryId` / `saveLastCategory` in `web/src/components/TransactionEntryRow.tsx:47-66`. `getLastCurrency` / `saveLastCurrency` must live alongside them in the same file, following the same module-local pattern. The storage key goes in `web/src/lib/storage-keys.ts`.
- **Popover + Command picker** — the category picker at `TransactionEntryRow.tsx:341-374` is the reference. Copy the `data-entry-field` + `focusFieldByName` integration.
- **Amount formatting** — use `formatCurrency(amount, code)` from `web/src/lib/format.ts:23-32`. Never roll your own `Intl.NumberFormat` call.
- **Tests assert payload shape, not formatted strings.** See `TransactionEntryRow.test.tsx:141-148` (a full payload-shape equality assertion) for the canonical style.

### Frontend test-run convention

- Run Vitest from the `web/` directory: `cd web && npm test -- <pattern>`.
- Run all frontend tests: `cd web && npm test`.
- Run typecheck: `cd web && npx tsc --noEmit`.

### Commit discipline (SpenDrop)

- Conventional commits (`feat`, `fix`, `test`, `docs`, `refactor`, `chore`). Example: `feat(currency): add useCurrencies hook and rateFor helper`.
- **Never push to remote** unless the user explicitly asks.
- **Never amend published commits.** Create new ones.
- No `Co-Authored-By` lines.
- Frequent commits — one logical unit per commit.

### Invariants (from spec §Edge Cases)

1. **`currency === baseCode` collapses** — payload MUST NOT include `original_*` when equal. Enforced by `toCreatePayload`.
2. **Both-or-neither** — `original_amount` and `original_currency` appear together or not at all. Enforced by `toCreatePayload`.
3. **Currency change NEVER mutates typed amount** — only the `≈` preview recomputes. Enforced by separate `onValueChange` + `onCurrencyChange` callbacks (Wise Neptune pattern).
4. **Focus-preserving currency change** — while the amount input has focus, changing the currency must not reformat or re-parse the amount's DOM value.
5. **`rateFor() === null` blocks Save** — no silent `rate=1` fallback (Firefly III #11616 anti-pattern).
6. **Edit mode shows `(inactive)` suffix** on historical currencies that have been deactivated. Entry mode hides inactive entries entirely.

---

## File Structure

**Created (frontend only):**
- `web/src/hooks/useCurrencies.ts` — module-level promise cache; returns `{ list, baseCode, rateFor, loading, error }`.
- `web/src/hooks/useCurrencies.test.ts` — cache + `rateFor` null-path tests.
- `web/src/lib/currency.ts` — pure helpers: `toCreatePayload`, `toEditDefaults`, `PREVIEW_DECIMALS`.
- `web/src/lib/currency.test.ts` — helper invariant tests (Firefly III failure-mode guards).
- `web/src/components/AmountCurrencyInput.tsx` — composed control.
- `web/src/components/AmountCurrencyInput.test.tsx` — unit tests.
- `web/src/components/AmountDisplay.tsx` — read-only amount renderer with optional secondary line.
- `web/src/components/AmountDisplay.test.tsx` — unit tests.

**Modified:**
- `web/src/lib/storage-keys.ts` — add `lastTransactionCurrency` key.
- `web/src/components/TransactionEntryRow.tsx` — extend Zod schema, add sticky helpers, replace bare `<Input>` with `<AmountCurrencyInput>`, add `'currency'` to tab order, transform payload via `toCreatePayload`.
- `web/src/components/TransactionEntryRow.test.tsx` — extend for currency default, payload shape, tab order, sticky persistence.
- `web/src/components/TransactionRow.tsx` — replace inline amount `<span>` with `<AmountDisplay>`; add currency picker to edit mode.
- `web/src/components/TransactionRow.test.tsx` — regression tests for single-line (original_* null) + two-line (original_* set) rendering + edit-mode currency round-trip.
- `web/src/hooks/useTransactions.ts` — no behavioral change; `CreateTransactionInput` already has the optional fields. Verify in Task 3.3.

**Unchanged (do NOT touch):**
- `internal/api/*.go` — backend is authoritative; no changes.
- `internal/database/*.sql` — schema + queries unchanged.
- `web/src/hooks/useBaseCurrency.ts` — still used by `TransactionRow` etc.; `useCurrencies` is additive.
- `web/src/pages/Transactions.tsx` — the `onSubmit` prop it passes to `TransactionEntryRow` is already shaped to accept the wire payload (`CreateTransactionInput`), so no change needed at the page level.
- Dashboard, Reports, Trash, Export — all consume `amount_cents` which is unchanged.

---

## Chunk 1: Foundations (storage key + pure helpers + hook)

Pure-logic layer with no UI. Build this bottom-up so later chunks can import from it freely. Three small tasks.

### Task 1.1: Add `lastTransactionCurrency` storage key

**Files:**
- Modify: `web/src/lib/storage-keys.ts:11-35`

- [ ] **Step 1: Add the key**

Edit `web/src/lib/storage-keys.ts` inside the `STORAGE_KEYS` object, keeping the existing ordering (sticky entry-row keys grouped together):

```ts
  /** Last date used in the transaction entry row, for sticky default. */
  lastTransactionDate: 'spendrop-last-date',
  /** Last category used in the transaction entry row, for sticky default. */
  lastTransactionCategory: 'spendrop-last-category',
  /** Last currency used in the transaction entry row, for sticky default. */
  lastTransactionCurrency: 'spendrop-last-currency',
```

- [ ] **Step 2: Verify typecheck passes**

Run: `cd web && npx tsc --noEmit`
Expected: no errors (the new key just extends a const object; nothing else reads it yet).

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/storage-keys.ts
git commit -m "feat(storage): add lastTransactionCurrency key"
```

---

### Task 1.2: `lib/currency.ts` pure helpers (TDD)

**Files:**
- Create: `web/src/lib/currency.ts`
- Create: `web/src/lib/currency.test.ts`

This is a pure-logic module. Follow TDD: write every test first, watch them fail, then write the minimal implementation.

- [ ] **Step 1: Write the failing test file**

Create `web/src/lib/currency.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { toCreatePayload, toEditDefaults, PREVIEW_DECIMALS } from './currency';
import type { Transaction } from '@/api/types';

function makeTx(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: 1,
    user_id: 1,
    date: '2026-04-17',
    amount: 100,
    original_amount: null,
    original_currency: null,
    description: 'x',
    category_id: 1,
    category_name: 'c',
    category_type: 'expense',
    tags: null,
    notes: null,
    created_at: '2026-04-17T00:00:00Z',
    updated_at: '2026-04-17T00:00:00Z',
    ...overrides,
  };
}

const rateFor =
  (rates: Record<string, number | null>) =>
  (code: string): number | null =>
    rates[code] ?? null;

describe('PREVIEW_DECIMALS', () => {
  it('is 2', () => {
    expect(PREVIEW_DECIMALS).toBe(2);
  });
});

describe('toCreatePayload', () => {
  const base = 'USD';
  const rates = rateFor({ USD: 1, EUR: 0.9, LBP: 90000, JPY: 150 });

  it('_SameAsBaseCollapsesField: when currency === baseCode, strips currency and emits only amount', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 42.5,
        description: 'd',
        category_id: 1,
        tags: 't',
        currency: 'USD',
      },
      base,
      rates,
    );
    expect(out).toEqual({
      date: '2026-04-17',
      amount: 42.5,
      description: 'd',
      category_id: 1,
      tags: 't',
    });
    // The absent-key invariant: ensure original_* are not present as undefined either.
    expect('currency' in out).toBe(false);
    expect('original_amount' in out).toBe(false);
    expect('original_currency' in out).toBe(false);
  });

  it('_BothOrNeither: when currency !== baseCode, emits both original_amount AND original_currency', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 150000,
        description: 'd',
        category_id: 1,
        tags: 't',
        currency: 'LBP',
      },
      base,
      rates,
    );
    expect(out).toMatchObject({
      amount: expect.any(Number),
      original_amount: 150000,
      original_currency: 'LBP',
    });
    expect('currency' in out).toBe(false);
  });

  it('divides original amount by rate and rounds to 2 decimals', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 150000,
        description: 'd',
        category_id: 1,
        tags: '',
        currency: 'LBP',
      },
      base,
      rates,
    );
    // 150000 / 90000 = 1.666..., rounded to 1.67
    expect((out as { amount: number }).amount).toBe(1.67);
  });

  it('_RateOneIsExplicit: non-base currency with rate === 1 still emits original_*', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 50,
        description: 'd',
        category_id: 1,
        tags: '',
        currency: 'EUR',
      },
      'USD',
      rateFor({ USD: 1, EUR: 1 }),
    );
    expect(out).toMatchObject({
      amount: 50,
      original_amount: 50,
      original_currency: 'EUR',
    });
  });

  it('_NoRateThrows: throws when rateFor returns null for a non-base currency', () => {
    expect(() =>
      toCreatePayload(
        {
          date: '2026-04-17',
          amount: 10,
          description: 'd',
          category_id: 1,
          tags: '',
          currency: 'XYZ',
        },
        base,
        rates,
      ),
    ).toThrow(/no rate/i);
  });

  it('_NoRateThrows: throws when rateFor returns zero for a non-base currency', () => {
    expect(() =>
      toCreatePayload(
        {
          date: '2026-04-17',
          amount: 10,
          description: 'd',
          category_id: 1,
          tags: '',
          currency: 'ZRO',
        },
        base,
        rateFor({ USD: 1, ZRO: 0 }),
      ),
    ).toThrow(/no rate/i);
  });

  it('preserves extra fields on the values object (generic)', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 10,
        description: 'd',
        category_id: 1,
        tags: 'a,b',
        currency: 'USD',
        notes: 'hello',
      },
      base,
      rates,
    );
    expect(out).toMatchObject({ notes: 'hello', tags: 'a,b' });
  });
});

describe('toEditDefaults', () => {
  it('returns original_amount + original_currency when both present', () => {
    const tx = makeTx({ original_amount: 150000, original_currency: 'LBP' });
    expect(toEditDefaults(tx, 'USD')).toEqual({ amount: 150000, currency: 'LBP' });
  });

  it('returns tx.amount + baseCode when original_* fields are null', () => {
    const tx = makeTx({
      amount: 25.5,
      original_amount: null,
      original_currency: null,
    });
    expect(toEditDefaults(tx, 'USD')).toEqual({ amount: 25.5, currency: 'USD' });
  });

  it('falls back to baseCode when only one of the original_* fields is present', () => {
    const txA = makeTx({ original_amount: 100, original_currency: null });
    const txB = makeTx({ original_amount: null, original_currency: 'EUR' });
    expect(toEditDefaults(txA, 'USD')).toEqual({ amount: txA.amount, currency: 'USD' });
    expect(toEditDefaults(txB, 'USD')).toEqual({ amount: txB.amount, currency: 'USD' });
  });
});
```

- [ ] **Step 2: Run the tests, confirm they fail**

Run: `cd web && npm test -- src/lib/currency.test.ts`
Expected: FAIL — "Cannot find module './currency'".

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/lib/currency.ts`:

```ts
import type { Transaction } from '@/api/types';

/** Number of decimal places shown in the `≈` preview, and used for rounding
 *  the frontend-computed base-currency amount before submit. Matches the
 *  backend's `math.Round(converted*100)/100` behavior so the pre-save
 *  preview and post-save row agree to the cent. 2-decimal assumption —
 *  see spec §Edge Case 7 for the known limitation around JPY/BHD. */
export const PREVIEW_DECIMALS = 2;

function roundToPreview(n: number): number {
  const factor = 10 ** PREVIEW_DECIMALS;
  return Math.round(n * factor) / factor;
}

/**
 * Transforms entry-form values into the wire payload for
 * `POST /api/transactions`. Collapses `currency === baseCode` to a bare
 * `{ amount }` (no `original_*`). Otherwise divides the typed amount by
 * the currency's rate-to-base, rounds to 2 decimals, and emits both
 * `original_amount` and `original_currency` alongside the computed
 * `amount`. Throws when the non-base currency has no rate configured —
 * callers gate the Save button on this.
 *
 * Generic over T so `TransactionEntryRow` and the edit form can both
 * pass through their own field set (description, category_id, tags,
 * notes, ...) without losing types. The `currency` key is stripped
 * from the output in both branches.
 */
export function toCreatePayload<
  T extends Record<string, unknown> & { amount: number; currency: string },
>(
  values: T,
  baseCode: string,
  rateFor: (code: string) => number | null,
):
  | (Omit<T, 'currency'> & { amount: number })
  | (Omit<T, 'currency'> & {
      amount: number;
      original_amount: number;
      original_currency: string;
    }) {
  const { currency, ...rest } = values;
  if (currency === baseCode) {
    return { ...rest, amount: values.amount } as Omit<T, 'currency'> & {
      amount: number;
    };
  }
  const rate = rateFor(currency);
  if (rate == null || rate <= 0) {
    throw new Error(`no rate configured for ${currency}`);
  }
  return {
    ...rest,
    amount: roundToPreview(values.amount / rate),
    original_amount: values.amount,
    original_currency: currency,
  } as Omit<T, 'currency'> & {
    amount: number;
    original_amount: number;
    original_currency: string;
  };
}

/**
 * Derives initial form values from a saved `Transaction` for the edit
 * surface. When the transaction carries both `original_amount` and
 * `original_currency`, round-trips through those; otherwise falls back
 * to the base-currency `amount` and the household's base code.
 */
export function toEditDefaults(
  tx: Transaction,
  baseCode: string,
): { amount: number; currency: string } {
  if (tx.original_amount != null && tx.original_currency != null) {
    return { amount: tx.original_amount, currency: tx.original_currency };
  }
  return { amount: tx.amount, currency: baseCode };
}
```

- [ ] **Step 4: Run the tests, confirm they pass**

Run: `cd web && npm test -- src/lib/currency.test.ts`
Expected: all PASS.

- [ ] **Step 5: Run typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/currency.ts web/src/lib/currency.test.ts
git commit -m "feat(currency): add toCreatePayload and toEditDefaults helpers"
```

---

### Task 1.3: `useCurrencies` hook (TDD)

**Files:**
- Create: `web/src/hooks/useCurrencies.ts`
- Create: `web/src/hooks/useCurrencies.test.ts`

Mirrors `useBaseCurrency.ts`'s module-level promise cache. Returns the full list, derived `baseCode`, and a `rateFor(code)` helper.

- [ ] **Step 1: Write the failing test file**

Create `web/src/hooks/useCurrencies.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import type { Currency } from '@/api/types';

// Mock the api client at module level so each test can control the promise.
vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

import { api } from '@/api/client';
import { useCurrencies, __resetCurrenciesCacheForTests } from './useCurrencies';

const mockedGet = api.get as unknown as ReturnType<typeof vi.fn>;

const sampleCurrencies: Currency[] = [
  { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'EUR', name: 'Euro', symbol: '€', rate_to_base: 0.9, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'ZRO', name: 'ZeroRate', symbol: 'Z', rate_to_base: 0, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
];

describe('useCurrencies', () => {
  beforeEach(() => {
    __resetCurrenciesCacheForTests();
    mockedGet.mockReset();
  });

  it('fetches the list once and reuses the promise across hook consumers', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);

    const { result: r1 } = renderHook(() => useCurrencies());
    const { result: r2 } = renderHook(() => useCurrencies());

    await waitFor(() => {
      expect(r1.current.loading).toBe(false);
      expect(r2.current.loading).toBe(false);
    });

    expect(mockedGet).toHaveBeenCalledTimes(1);
    expect(r1.current.list).toEqual(sampleCurrencies);
    expect(r2.current.list).toEqual(sampleCurrencies);
  });

  it('returns the is_base code as baseCode', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.baseCode).toBe('USD');
  });

  it('falls back to USD when no currency has is_base === true', async () => {
    mockedGet.mockResolvedValue(
      sampleCurrencies.map((c) => ({ ...c, is_base: false })),
    );
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.baseCode).toBe('USD');
  });

  it('rateFor returns rate_to_base for known active currencies', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.rateFor('USD')).toBe(1);
    expect(result.current.rateFor('EUR')).toBe(0.9);
    expect(result.current.rateFor('LBP')).toBe(90000);
  });

  it('rateFor returns null for unknown codes', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.rateFor('XYZ')).toBe(null);
  });

  it('rateFor returns null for zero rate_to_base', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.rateFor('ZRO')).toBe(null);
  });

  it('transitions loading: true → false on resolve', async () => {
    let resolveFn!: (value: Currency[]) => void;
    mockedGet.mockReturnValue(
      new Promise<Currency[]>((resolve) => {
        resolveFn = resolve;
      }),
    );

    const { result } = renderHook(() => useCurrencies());
    expect(result.current.loading).toBe(true);
    expect(result.current.list).toEqual([]);

    resolveFn(sampleCurrencies);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.list).toEqual(sampleCurrencies);
  });

  it('surfaces API errors without throwing during render', async () => {
    mockedGet.mockRejectedValue(new Error('network down'));
    const { result } = renderHook(() => useCurrencies());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toMatch(/network down/);
    expect(result.current.list).toEqual([]);
    expect(result.current.baseCode).toBe('USD'); // DEFAULT_CURRENCY fallback
    expect(result.current.rateFor('EUR')).toBe(null);
  });
});
```

- [ ] **Step 2: Run the tests, confirm they fail**

Run: `cd web && npm test -- src/hooks/useCurrencies.test.ts`
Expected: FAIL — "Cannot find module './useCurrencies'".

- [ ] **Step 3: Write the hook implementation**

Create `web/src/hooks/useCurrencies.ts`:

```ts
import { useEffect, useState } from 'react';
import { api } from '@/api/client';
import type { Currency } from '@/api/types';
import { DEFAULT_CURRENCY } from '@/lib/format';

export interface UseCurrenciesResult {
  /** Full list as returned by `GET /api/currencies`. Inactive currencies
   *  are included so the edit surface can render `(inactive)` suffixes
   *  for historical rows; the entry surface filters them out. */
  list: Currency[];
  /** Code of the `is_base` currency, or `DEFAULT_CURRENCY` ("USD") fallback. */
  baseCode: string;
  /** Returns `rate_to_base` for `code`, or `null` if the code is unknown
   *  or has a null / zero / negative rate. Callers gate Save on this
   *  to avoid a silent rate-of-1 fallback (Firefly III #11616). */
  rateFor: (code: string) => number | null;
  loading: boolean;
  error: string | null;
}

interface CacheEntry {
  list: Currency[];
  baseCode: string;
  error: string | null;
}

// Module-level promise cache — one fetch per session, shared across hook
// consumers. Mirrors `useBaseCurrency.ts`. A separate cache variable
// intentionally; `useBaseCurrency` stays independent so removing this
// hook later is straightforward.
let cachePromise: Promise<CacheEntry> | null = null;

function fetchCurrencies(): Promise<CacheEntry> {
  if (!cachePromise) {
    cachePromise = api
      .get<Currency[]>('currencies')
      .then((list) => {
        const base = list.find((c) => c.is_base)?.code ?? DEFAULT_CURRENCY;
        return { list, baseCode: base, error: null };
      })
      .catch((err: unknown) => {
        const msg = err instanceof Error ? err.message : 'failed to load currencies';
        return { list: [], baseCode: DEFAULT_CURRENCY, error: msg };
      });
  }
  return cachePromise;
}

/** Test-only. Resets the module-level cache between unit tests. */
export function __resetCurrenciesCacheForTests(): void {
  cachePromise = null;
}

export function useCurrencies(): UseCurrenciesResult {
  const [state, setState] = useState<{
    list: Currency[];
    baseCode: string;
    error: string | null;
    loading: boolean;
  }>({ list: [], baseCode: DEFAULT_CURRENCY, error: null, loading: true });

  useEffect(() => {
    let cancelled = false;
    fetchCurrencies().then((entry) => {
      if (cancelled) return;
      setState({ ...entry, loading: false });
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const rateFor = (code: string): number | null => {
    const c = state.list.find((x) => x.code === code);
    if (!c) return null;
    if (c.rate_to_base == null || c.rate_to_base <= 0) return null;
    return c.rate_to_base;
  };

  return {
    list: state.list,
    baseCode: state.baseCode,
    rateFor,
    loading: state.loading,
    error: state.error,
  };
}
```

- [ ] **Step 4: Run the tests, confirm they pass**

Run: `cd web && npm test -- src/hooks/useCurrencies.test.ts`
Expected: all PASS.

- [ ] **Step 5: Run typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/hooks/useCurrencies.ts web/src/hooks/useCurrencies.test.ts
git commit -m "feat(currency): add useCurrencies hook with promise cache and rateFor"
```

---

## Chunk 2: Display components (AmountDisplay, AmountCurrencyInput)

Both components are self-contained and consume only Chunk 1 primitives. Do `AmountDisplay` first — it is simpler and used by `AmountCurrencyInput` tests as a regression anchor.

### Task 2.1: `<AmountDisplay />` (TDD)

**Files:**
- Create: `web/src/components/AmountDisplay.tsx`
- Create: `web/src/components/AmountDisplay.test.tsx`

Read-only two-line render. Primary = base-currency amount formatted with `formatCurrency`. Secondary = `{original_amount} {original_currency}` as muted text, shown only when a foreign currency is stored.

- [ ] **Step 1: Write the failing test file**

Create `web/src/components/AmountDisplay.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { AmountDisplay } from './AmountDisplay';

describe('AmountDisplay', () => {
  it('renders a single formatted line when originalAmount is null', () => {
    render(
      <AmountDisplay
        amount={25.5}
        originalAmount={null}
        originalCurrency={null}
        type="expense"
        baseCode="USD"
      />,
    );
    // Primary line carries the currency symbol and sign via formatCurrency.
    expect(screen.getByText(/\$25\.50/)).toBeInTheDocument();
    // No secondary line.
    expect(screen.queryByText(/LBP|EUR/)).not.toBeInTheDocument();
  });

  it('renders a single line when originalCurrency equals baseCode (defensive fallback)', () => {
    render(
      <AmountDisplay
        amount={25.5}
        originalAmount={25.5}
        originalCurrency="USD"
        type="expense"
        baseCode="USD"
      />,
    );
    expect(screen.getByText(/\$25\.50/)).toBeInTheDocument();
    // Defensive: even though original_* are set, they match base, so no secondary.
    expect(screen.queryByTestId('amount-display-secondary')).not.toBeInTheDocument();
  });

  it('renders two lines when originalCurrency differs from baseCode', () => {
    render(
      <AmountDisplay
        amount={1.67}
        originalAmount={150000}
        originalCurrency="LBP"
        type="expense"
        baseCode="USD"
      />,
    );
    expect(screen.getByText(/\$1\.67/)).toBeInTheDocument();
    const secondary = screen.getByTestId('amount-display-secondary');
    expect(secondary).toHaveTextContent('150,000');
    expect(secondary).toHaveTextContent('LBP');
  });

  it('applies expense styling (negative sign + default foreground)', () => {
    const { container } = render(
      <AmountDisplay
        amount={25.5}
        originalAmount={null}
        originalCurrency={null}
        type="expense"
        baseCode="USD"
      />,
    );
    expect(container.textContent).toMatch(/^-/);
  });

  it('applies income styling (positive sign + green class)', () => {
    const { container } = render(
      <AmountDisplay
        amount={1000}
        originalAmount={null}
        originalCurrency={null}
        type="income"
        baseCode="USD"
      />,
    );
    expect(container.textContent).toMatch(/^\+/);
    expect(container.firstChild).toHaveClass('text-emerald-500');
  });
});
```

- [ ] **Step 2: Run the tests, confirm they fail**

Run: `cd web && npm test -- src/components/AmountDisplay.test.tsx`
Expected: FAIL — "Cannot find module './AmountDisplay'".

- [ ] **Step 3: Write the component**

Create `web/src/components/AmountDisplay.tsx`:

```tsx
import { cn } from '@/lib/utils';
import { formatCurrency, formatAmount } from '@/lib/format';
import { TYPE_EXPENSE, type TransactionType } from '@/lib/transaction-types';

export interface AmountDisplayProps {
  /** Canonical base-currency amount (from `tx.amount`). */
  amount: number;
  /** `tx.original_amount` — null for base-currency-only rows. */
  originalAmount: number | null;
  /** `tx.original_currency` — null for base-currency-only rows. */
  originalCurrency: string | null;
  type: TransactionType;
  baseCode: string;
  className?: string;
}

/**
 * Read-only amount cell for the transactions list. Renders a single
 * formatted line by default; when the transaction has a non-null
 * `original_currency` different from the household base, adds a
 * muted secondary line showing the typed foreign-currency amount.
 */
export function AmountDisplay({
  amount,
  originalAmount,
  originalCurrency,
  type,
  baseCode,
  className,
}: AmountDisplayProps) {
  const showSecondary =
    originalAmount != null &&
    originalCurrency != null &&
    originalCurrency !== baseCode;

  const sign = type === TYPE_EXPENSE ? '-' : '+';
  const colorClass =
    type === TYPE_EXPENSE ? 'text-foreground' : 'text-emerald-500';

  return (
    <span
      className={cn(
        'inline-flex flex-col items-end font-mono tabular-nums',
        colorClass,
        className,
      )}
    >
      <span>
        {sign}
        {formatCurrency(amount, baseCode)}
      </span>
      {showSecondary && (
        <span
          data-testid="amount-display-secondary"
          className="text-xs font-normal text-muted-foreground"
        >
          {formatAmount(originalAmount!)} {originalCurrency}
        </span>
      )}
    </span>
  );
}
```

- [ ] **Step 4: Run the tests, confirm they pass**

Run: `cd web && npm test -- src/components/AmountDisplay.test.tsx`
Expected: all PASS.

- [ ] **Step 5: Run typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/AmountDisplay.tsx web/src/components/AmountDisplay.test.tsx
git commit -m "feat(currency): add AmountDisplay component for list rows"
```

---

### Task 2.2: `<AmountCurrencyInput />` (TDD)

**Files:**
- Create: `web/src/components/AmountCurrencyInput.tsx`
- Create: `web/src/components/AmountCurrencyInput.test.tsx`

Composed control: amount `<Input>` + currency-code `<Button>` suffix that opens a `<Popover>` + `<Command>` picker, plus a `≈` preview line below the input when `currency !== baseCode`.

- [ ] **Step 1: Write the failing test file**

Create `web/src/components/AmountCurrencyInput.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import type { Currency } from '@/api/types';
import { AmountCurrencyInput } from './AmountCurrencyInput';

const currencies: Currency[] = [
  { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'EUR', name: 'Euro', symbol: '€', rate_to_base: 0.9, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'OLD', name: 'Obsolete', symbol: 'O', rate_to_base: 2, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'NORATE', name: 'No Rate', symbol: 'N', rate_to_base: 0, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
];

const rateFor = (code: string): number | null => {
  const c = currencies.find((x) => x.code === code);
  if (!c) return null;
  if (c.rate_to_base <= 0) return null;
  return c.rate_to_base;
};

type Overrides = Partial<React.ComponentProps<typeof AmountCurrencyInput>>;

function renderInput(props: Overrides = {}) {
  const defaults: React.ComponentProps<typeof AmountCurrencyInput> = {
    value: 0,
    onValueChange: vi.fn(),
    currency: 'USD',
    onCurrencyChange: vi.fn(),
    baseCode: 'USD',
    currencies,
    hideInactive: true,
    rateFor,
  };
  return render(<AmountCurrencyInput {...defaults} {...props} />);
}

describe('AmountCurrencyInput', () => {
  it('renders the amount input and the currency-code suffix button', () => {
    renderInput();
    expect(screen.getByRole('spinbutton')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /USD/ })).toBeInTheDocument();
  });

  it('typing in the amount input calls onValueChange with the numeric value', async () => {
    const user = userEvent.setup();
    const onValueChange = vi.fn();
    renderInput({ onValueChange });
    await user.type(screen.getByRole('spinbutton'), '150');
    // Last call should be the full typed number, not the string.
    const lastCall = onValueChange.mock.calls.at(-1)!;
    expect(lastCall[0]).toBe(150);
    expect(typeof lastCall[0]).toBe('number');
  });

  it('clicking the suffix button opens the Popover with a searchable list', async () => {
    const user = userEvent.setup();
    renderInput();
    await user.click(screen.getByRole('button', { name: /USD/ }));
    expect(await screen.findByPlaceholderText(/search/i)).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /EUR/ })).toBeInTheDocument();
  });

  it('selecting a currency closes the Popover and calls onCurrencyChange', async () => {
    const user = userEvent.setup();
    const onCurrencyChange = vi.fn();
    renderInput({ onCurrencyChange });
    await user.click(screen.getByRole('button', { name: /USD/ }));
    await user.click(await screen.findByRole('option', { name: /LBP/ }));
    expect(onCurrencyChange).toHaveBeenCalledWith('LBP');
  });

  it('renders no preview when currency === baseCode', () => {
    renderInput({ value: 100, currency: 'USD', baseCode: 'USD' });
    expect(screen.queryByText(/≈/)).not.toBeInTheDocument();
  });

  it('renders a ≈ preview when currency !== baseCode with a valid rate', () => {
    renderInput({ value: 150000, currency: 'LBP', baseCode: 'USD' });
    // 150000 / 90000 = 1.666... → rounded to 1.67
    expect(screen.getByText(/≈/)).toHaveTextContent(/\$1\.67/);
  });

  it('_PreviewUpdatesOnCurrencyChange: swapping currency with same amount re-renders the preview with the new rate', () => {
    // Isolates the spec's "preview recomputes when currency changes
    // (amount preserved — Firefly III #10791 guard)" requirement.
    // Explicitly separate from _FocusPreservesRawInput, which guards the
    // input DOM value only.
    const { rerender } = render(
      <AmountCurrencyInput
        value={150000}
        onValueChange={() => {}}
        currency="LBP"
        onCurrencyChange={() => {}}
        baseCode="USD"
        currencies={currencies}
        hideInactive={true}
        rateFor={rateFor}
      />,
    );
    expect(screen.getByText(/≈/)).toHaveTextContent(/\$1\.67/);

    // Swap currency to EUR (rate 0.9). 150000 / 0.9 = 166666.67.
    rerender(
      <AmountCurrencyInput
        value={150000}
        onValueChange={() => {}}
        currency="EUR"
        onCurrencyChange={() => {}}
        baseCode="USD"
        currencies={currencies}
        hideInactive={true}
        rateFor={rateFor}
      />,
    );
    // Preview is in the base currency (USD); different rate → different result.
    expect(screen.getByText(/≈/)).toHaveTextContent(/\$166,666\.67/);
  });

  it('_FocusPreservesRawInput: changing currency while amount input is focused does NOT mutate the DOM value', async () => {
    // Controlled component: the value stays 150000 across re-renders. The
    // guarantee is that no internal reformatting re-parses the DOM text or
    // fires an onChange with a different shape when currency flips.
    const user = userEvent.setup();
    const onValueChange = vi.fn();
    const { rerender } = render(
      <AmountCurrencyInput
        value={150000}
        onValueChange={onValueChange}
        currency="USD"
        onCurrencyChange={() => {}}
        baseCode="USD"
        currencies={currencies}
        hideInactive={true}
        rateFor={rateFor}
      />,
    );
    const input = screen.getByRole('spinbutton') as HTMLInputElement;
    input.focus();
    expect(input.value).toBe('150000');

    // Simulate the parent flipping currency to LBP while the input is focused.
    rerender(
      <AmountCurrencyInput
        value={150000}
        onValueChange={onValueChange}
        currency="LBP"
        onCurrencyChange={() => {}}
        baseCode="USD"
        currencies={currencies}
        hideInactive={true}
        rateFor={rateFor}
      />,
    );

    expect(input.value).toBe('150000');
    // No extraneous onValueChange call from the currency flip.
    expect(onValueChange).not.toHaveBeenCalled();
  });

  it('hideInactive=true: filters is_active=false entries out of the picker', async () => {
    const user = userEvent.setup();
    const list: Currency[] = [
      ...currencies,
      { code: 'INC', name: 'Inactive', symbol: 'I', rate_to_base: 5, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
    ];
    // Emulate admin marking "INC" inactive. The Currency type doesn't carry
    // `is_active` directly — we piggyback on the same contract the admin
    // page uses. (If the Currency schema adds `is_active` later, the filter
    // key must update. See spec §Edge Case 1.)
    //
    // NOTE for implementer: the backend Currency shape currently does NOT
    // expose is_active. If it does not by the time you build this, pass
    // currencies unfiltered and make hideInactive a no-op stub pending
    // a backend change — verify by grepping `Currency` in api/types.ts
    // before making this decision. See Chunk 2 implementer-note.
    renderInput({ currencies: list, hideInactive: true });
    await user.click(screen.getByRole('button', { name: /USD/ }));
    // All active currencies are present.
    expect(screen.getByRole('option', { name: /USD/ })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /EUR/ })).toBeInTheDocument();
  });

  it('currencies with rateFor() === null render disabled', async () => {
    const user = userEvent.setup();
    renderInput({ currencies });
    await user.click(screen.getByRole('button', { name: /USD/ }));
    const noRateOption = await screen.findByRole('option', { name: /NORATE/ });
    expect(noRateOption).toHaveAttribute('aria-disabled', 'true');
  });

  it('loading: true disables the picker trigger', () => {
    renderInput({ loading: true });
    expect(screen.getByRole('button', { name: /USD/ })).toBeDisabled();
  });

  it('surfaces inline error text when error is set', () => {
    renderInput({ error: 'no rate configured' });
    expect(screen.getByText(/no rate configured/i)).toBeInTheDocument();
  });

  it('Enter inside the Command list selects and does NOT submit a parent form', async () => {
    const user = userEvent.setup();
    const onCurrencyChange = vi.fn();
    const onFormSubmit = vi.fn();
    render(
      <form onSubmit={onFormSubmit}>
        <AmountCurrencyInput
          value={100}
          onValueChange={() => {}}
          currency="USD"
          onCurrencyChange={onCurrencyChange}
          baseCode="USD"
          currencies={currencies}
          hideInactive={true}
          rateFor={rateFor}
        />
      </form>,
    );
    await user.click(screen.getByRole('button', { name: /USD/ }));
    // Move focus into the command list, then Enter on the first option.
    const search = await screen.findByPlaceholderText(/search/i);
    await user.type(search, 'EUR{Enter}');

    expect(onCurrencyChange).toHaveBeenCalledWith('EUR');
    expect(onFormSubmit).not.toHaveBeenCalled();
  });
});
```

**Implementer note for Task 2.2 test design:**
The `hideInactive` test above is structured so it continues to pass even if the backend `Currency` type does not currently carry `is_active`. Before writing the component, **grep `web/src/api/types.ts` to confirm the shape** (see `Currency` at line 61-68). If `is_active` is absent, default the filter to a no-op (the component renders all entries regardless of `hideInactive`); add a TODO comment in the component pointing to a follow-up ticket; and tighten the test to assert on a currency that's actually filterable. If `is_active` is present, filter on it directly. Either way, the load-bearing tests (preview, disabled-on-no-rate, focus-preserves-raw-input, Enter-doesn't-submit) remain the critical contracts.

- [ ] **Step 2: Run the tests, confirm they fail**

Run: `cd web && npm test -- src/components/AmountCurrencyInput.test.tsx`
Expected: FAIL — "Cannot find module './AmountCurrencyInput'".

- [ ] **Step 3: Write the component**

Create `web/src/components/AmountCurrencyInput.tsx`:

```tsx
import { useState, type Ref } from 'react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Loader2 } from 'lucide-react';
import type { Currency } from '@/api/types';
import { cn } from '@/lib/utils';
import { formatCurrency } from '@/lib/format';
import { selectAllOnFocus } from '@/lib/utils';

export interface AmountCurrencyInputProps {
  /** Amount in the user's selected currency (raw typed value; NOT converted). */
  value: number;
  onValueChange: (v: number) => void;
  /** ISO code, e.g. "USD", "LBP". */
  currency: string;
  onCurrencyChange: (code: string) => void;
  baseCode: string;
  /**
   * Full list from `useCurrencies`. The component handles mode-specific
   * filtering internally (see `hideInactive`) so entry and edit surfaces
   * share a single implementation.
   */
  currencies: Currency[];
  /**
   * When true (entry mode), hides `is_active === false` entries. When
   * false (edit mode), renders them with an `(inactive)` suffix so
   * historical rows remain round-trippable.
   */
  hideInactive: boolean;
  rateFor: (code: string) => number | null;
  loading?: boolean;
  error?: string | null;
  disabled?: boolean;
  inputRef?: Ref<HTMLInputElement>;
  /** Tab-navigation hook: the amount input receives this attribute. */
  dataEntryField?: string;
}

export function AmountCurrencyInput({
  value,
  onValueChange,
  currency,
  onCurrencyChange,
  baseCode,
  currencies,
  hideInactive,
  rateFor,
  loading = false,
  error = null,
  disabled = false,
  inputRef,
  dataEntryField,
}: AmountCurrencyInputProps) {
  const [open, setOpen] = useState(false);

  // The `Currency` backend shape may or may not carry `is_active`. Casting
  // to an extended shape keeps the filter future-proof: the day the backend
  // adds `is_active: boolean` to the type, this code continues to compile
  // without touching `@ts-expect-error` directives (which break once the
  // underlying error disappears). If the field is missing at runtime,
  // `undefined !== false` is `true` and the filter is a safe no-op.
  const visible = currencies.filter((c) => {
    if (!hideInactive) return true;
    return (c as Currency & { is_active?: boolean }).is_active !== false;
  });

  const rate = rateFor(currency);
  const showPreview = currency !== baseCode && rate != null && rate > 0;
  const previewValue = showPreview ? value / rate! : 0;

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-stretch">
        <Input
          type="number"
          step="0.01"
          min="0"
          value={value || ''}
          onChange={(e) =>
            onValueChange(e.target.value === '' ? 0 : Number(e.target.value))
          }
          onFocus={selectAllOnFocus}
          ref={inputRef}
          data-entry-field={dataEntryField}
          disabled={disabled}
          className="rounded-r-none font-mono tabular-nums"
        />
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <Button
              type="button"
              variant="outline"
              disabled={loading || disabled}
              className="rounded-l-none border-l-0 px-2 font-mono text-xs"
              aria-label={`Currency: ${currency}`}
            >
              {loading ? <Loader2 className="size-3 animate-spin" /> : currency}
            </Button>
          </PopoverTrigger>
          <PopoverContent className="p-0" align="end">
            <Command>
              <CommandInput placeholder="Search currency..." />
              <CommandList>
                <CommandEmpty>No currency found.</CommandEmpty>
                {visible.map((c) => {
                  // Rename: shadowing the outer `disabled` prop (component-level
                  // loading/disabled) would confuse reviewers. Row-local is
                  // `itemDisabled` — currency has no valid rate AND isn't base.
                  const itemDisabled =
                    rateFor(c.code) == null && c.code !== baseCode;
                  const inactive =
                    (c as Currency & { is_active?: boolean }).is_active === false;
                  return (
                    <CommandItem
                      key={c.code}
                      value={c.code}
                      aria-disabled={itemDisabled}
                      disabled={itemDisabled}
                      title={
                        itemDisabled
                          ? 'No exchange rate configured — set in Settings'
                          : undefined
                      }
                      onSelect={() => {
                        if (itemDisabled) return;
                        onCurrencyChange(c.code);
                        setOpen(false);
                      }}
                    >
                      <span className="font-mono">{c.code}</span>
                      <span className="ml-2 text-muted-foreground">
                        {c.name}
                      </span>
                      {inactive && (
                        <span className="ml-auto text-xs text-muted-foreground">
                          (inactive)
                        </span>
                      )}
                    </CommandItem>
                  );
                })}
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
      </div>
      {showPreview && (
        <span className="text-xs text-muted-foreground">
          &asymp; {formatCurrency(previewValue, baseCode)}
        </span>
      )}
      {error && (
        <span className={cn('text-xs text-destructive')}>{error}</span>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run the tests, confirm they pass**

Run: `cd web && npm test -- src/components/AmountCurrencyInput.test.tsx`
Expected: all PASS.

If the `hideInactive` test's edge case (filtering on `is_active`) fails because the backend doesn't expose that field, remove the `@ts-expect-error` comments, tighten the filter to the actual field available, and adjust the one test that exercises the flag. The spec's edge case #1 still holds as an invariant — if the backend never exposes is_active, that edge case is out of reach for this frontend and must be flagged to the user.

- [ ] **Step 5: Run typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/AmountCurrencyInput.tsx web/src/components/AmountCurrencyInput.test.tsx
git commit -m "feat(currency): add AmountCurrencyInput composed control"
```

---

## Chunk 3: Integrations (TransactionEntryRow + TransactionRow)

Chunk 3 threads Chunks 1 and 2 into the three existing surfaces. The surfaces are independent — you can split work across them — but commit them separately so regressions are bisectable.

### Task 3.1: Wire `TransactionEntryRow` to currency (TDD — tests first)

**Files:**
- Modify: `web/src/components/TransactionEntryRow.tsx` (lines 47-66 helpers; line 85-91 schema; line 227 tab order; line 282-315 amount field; line 146-185 submit)
- Modify: `web/src/components/TransactionEntryRow.test.tsx`

The onSubmit prop contract changes shape: it used to receive `EntryFormValues` (which was exactly the form shape). Now the form shape includes `currency`, but the onSubmit prop should receive the wire shape from `toCreatePayload` — because that is what the caller (`Transactions.tsx:722-726`) pipes straight to `createTransaction` → `api.post('transactions', input)`, which expects `CreateTransactionInput` (already has optional `original_amount` / `original_currency`).

**Contract change to apply:**
- `EntryFormValues` becomes an internal form shape. Not exported.
- `onSubmit` prop becomes typed as `(input: CreateTransactionInput) => Promise<Transaction>` where `CreateTransactionInput` is imported from `@/hooks/useTransactions`. (Add an `export` to that interface in `useTransactions.ts` if not already exported — see `web/src/hooks/useTransactions.ts:22-31`.)

- [ ] **Step 1: Export `CreateTransactionInput` AND `UpdateTransactionInput` from `useTransactions.ts`**

Both interfaces are needed by downstream tasks (`CreateTransactionInput` by Task 3.1, `UpdateTransactionInput` by Task 3.3). Export both now so `useTransactions.ts` is touched exactly once in this chunk — the Task 3.3 commit will only touch TransactionRow files.

Edit `web/src/hooks/useTransactions.ts:22-35` to add `export` to both interface declarations:

```ts
export interface CreateTransactionInput {
  date: string;
  amount: number;
  description: string;
  category_id: number;
  original_amount?: number;
  original_currency?: string;
  tags?: string;
  notes?: string;
}

export interface UpdateTransactionInput extends CreateTransactionInput {
  id: number;
}
```

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 2: Extend the entry-row tests for currency**

Open `web/src/components/TransactionEntryRow.test.tsx` and add the following. Use the file's existing mock patterns (mocked `sonner`, `savedTransaction` fixture). Place the new describe block at the end of the top-level `describe('TransactionEntryRow', ...)`:

```tsx
  // -----------------------------------------------------------------
  // Phase J: Currency selector
  // -----------------------------------------------------------------

  // NOTE: useCurrencies is a module-level promise cache. Each test resets it
  // so the mock fetcher is called fresh.

  const currencyFixtures = [
    { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
    { code: 'EUR', name: 'Euro', symbol: '€', rate_to_base: 0.9, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
    { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  ];

  // These tests mock `@/api/client` so useCurrencies can resolve predictably.
  // Mock at the top of the file alongside the existing sonner mock — do NOT
  // add a second vi.mock() inside the describe block.

  it('defaults currency to baseCode when localStorage is empty', async () => {
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    // After useCurrencies resolves, the suffix button should read USD.
    expect(
      await screen.findByRole('button', { name: /currency: usd/i }),
    ).toBeInTheDocument();
  });

  it('defaults currency to spendrop-last-currency when present', async () => {
    localStorage.setItem('spendrop-last-currency', 'LBP');
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    expect(
      await screen.findByRole('button', { name: /currency: lbp/i }),
    ).toBeInTheDocument();
  });

  it('submits the collapsed payload when currency === baseCode', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '25');
    await user.type(screen.getByLabelText(/description/i), 'Lunch');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    const payload = onSubmit.mock.calls[0][0];
    expect(payload).not.toHaveProperty('currency');
    expect(payload).not.toHaveProperty('original_amount');
    expect(payload).not.toHaveProperty('original_currency');
    expect(payload).toMatchObject({ amount: 25 });
  });

  it('submits the expanded payload when currency !== baseCode', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '150000');
    await user.type(screen.getByLabelText(/description/i), 'Groceries');
    await user.click(screen.getByRole('button', { name: /currency: usd/i }));
    await user.click(await screen.findByRole('option', { name: /LBP/ }));
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    const payload = onSubmit.mock.calls[0][0];
    expect(payload).toMatchObject({
      amount: 1.67, // 150000 / 90000 = 1.666... → 1.67
      original_amount: 150000,
      original_currency: 'LBP',
    });
    expect(payload).not.toHaveProperty('currency');
  });

  it('persists currency on successful save', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '50');
    await user.type(screen.getByLabelText(/description/i), 'x');
    await user.click(screen.getByRole('button', { name: /currency: usd/i }));
    await user.click(await screen.findByRole('option', { name: /EUR/ }));
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() =>
      expect(localStorage.getItem('spendrop-last-currency')).toBe('EUR'),
    );
  });

  it('Tab order: date → amount → currency → description → category', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });
    const amount = screen.getByLabelText(/amount/i);
    amount.focus();
    // Enter moves to next; currency is the next stop.
    await user.type(amount, '5{Enter}');
    expect(screen.getByRole('button', { name: /currency: usd/i })).toHaveFocus();
  });
```

Add a mock for `@/api/client` at the top of the file alongside the existing `sonner` mock:

```tsx
vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(async (path: string) => {
      if (path === 'currencies') {
        return [
          { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'EUR', name: 'Euro', symbol: '€', rate_to_base: 0.9, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
        ];
      }
      return [];
    }),
    post: vi.fn(),
    put: vi.fn(),
    del: vi.fn(),
  },
}));
```

Add a `beforeEach` reset alongside the existing one (per Task 1.3 the hook exports `__resetCurrenciesCacheForTests`):

```tsx
import { __resetCurrenciesCacheForTests } from '../hooks/useCurrencies';
// ...
  beforeEach(() => {
    __resetCurrenciesCacheForTests();
    // existing beforeEach body ...
  });
```

- [ ] **Step 3: Run the tests, confirm they fail**

Run: `cd web && npm test -- src/components/TransactionEntryRow.test.tsx`
Expected: new Phase J tests FAIL ("currency: usd" button not found, etc.); all existing Phase A–I tests still PASS.

- [ ] **Step 4: Update the entry row — schema + helpers**

Edit `web/src/components/TransactionEntryRow.tsx`. Add sticky-currency helpers alongside the existing ones:

```ts
function getLastCurrency(fallback: string): string {
  return (
    localStorage.getItem(STORAGE_KEYS.lastTransactionCurrency) ?? fallback
  );
}

function saveLastCurrency(code: string) {
  localStorage.setItem(STORAGE_KEYS.lastTransactionCurrency, code);
}
```

Extend the Zod schema (line 85-91):

```ts
const entrySchema = z.object({
  date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, 'Invalid date'),
  amount: z.number().positive('> 0'),
  currency: z.string().regex(/^[A-Z]{3}$/, 'Invalid currency'),
  description: z.string().min(1, 'required').max(200),
  category_id: z.number().int().positive('required'),
  tags: z.string(),
});
type EntryFormValues = z.infer<typeof entrySchema>;
```

Note: `EntryFormValues` is no longer exported — callers only need the wire shape. Drop `export` from line 92.

Change the `onSubmit` prop type (line 96):

```ts
export interface TransactionEntryRowProps {
  categories: Category[];
  onSubmit: (input: CreateTransactionInput) => Promise<Transaction>;
  onDelete: (id: number) => Promise<void>;
  onClose?: () => void;
  descriptionSuggestions?: string[];
  tagSuggestions?: string[];
}
```

Add the import at the top: `import type { CreateTransactionInput } from '@/hooks/useTransactions';`.

- [ ] **Step 5: Update the form default currency and submit transform**

Inside the component body, add `const { list, baseCode, rateFor, loading: currenciesLoading } = useCurrencies();` near the top (after the other hooks).

Update `defaultValues` in `useForm`:

```ts
    defaultValues: {
      date: getLastDate(),
      amount: 0,
      currency: getLastCurrency(baseCode),
      description: '',
      category_id: getLastCategoryId(),
      tags: '',
    },
```

**IMPORTANT:** `baseCode` from the hook is `DEFAULT_CURRENCY` until the fetch resolves. Call `form.reset` with the resolved baseCode inside an effect *only if the user has not already typed into the currency field*. Add:

```ts
const didInitCurrency = useRef(false);
useEffect(() => {
  if (didInitCurrency.current) return;
  if (currenciesLoading) return;
  didInitCurrency.current = true;
  const current = form.getValues('currency');
  // Only overwrite if the default was the initial fallback AND the user
  // hasn't already changed it. The check against getValues matches the
  // fallback chain: localStorage → baseCode → USD.
  const resolved = getLastCurrency(baseCode);
  if (current !== resolved) {
    form.setValue('currency', resolved, { shouldDirty: false });
  }
}, [currenciesLoading, baseCode, form]);
```

Add `'currency'` to the tab-navigation order (line 227):

```ts
      const order: string[] = ['date', 'amount', 'currency', 'description', 'category_id'];
```

Replace the amount FormField block (lines 282-315) with both the amount and currency wired via `AmountCurrencyInput`:

```tsx
          <FormField
            control={form.control}
            name="amount"
            render={({ field }) => (
              <FormItem className="w-56">
                <EntryLabel>Amount</EntryLabel>
                <FormControl>
                  <AmountCurrencyInput
                    value={field.value}
                    onValueChange={(v) => field.onChange(v)}
                    currency={form.watch('currency')}
                    onCurrencyChange={(code) =>
                      form.setValue('currency', code, { shouldValidate: true })
                    }
                    baseCode={baseCode}
                    currencies={list}
                    hideInactive={true}
                    rateFor={rateFor}
                    loading={currenciesLoading}
                    error={
                      rateFor(form.watch('currency')) == null &&
                      form.watch('currency') !== baseCode
                        ? 'No rate configured for this currency. Set one in Settings.'
                        : null
                    }
                    dataEntryField="amount"
                    inputRef={(el) => {
                      field.ref(el);
                      amountRef.current = el;
                    }}
                  />
                </FormControl>
              </FormItem>
            )}
          />
```

`AmountCurrencyInput` already receives `data-entry-field="amount"` on its inner amount input AND renders the currency button with `aria-label="Currency: {code}"`. The tab-navigation hook on the currency button needs a separate `data-entry-field` so `focusFieldByName('currency')` can find it.

**Cross-chunk note:** This edit retroactively touches `AmountCurrencyInput.tsx` — a file committed in Chunk 2. That's intentional: the component is consumed by entry and edit rows, but only the entry row uses `focusFieldByName`, so the hard-coded `data-entry-field="currency"` is a one-line addition rather than a new prop. Task 3.1 Step 9's `git add` list already includes `AmountCurrencyInput.tsx` to capture this change.

Update `AmountCurrencyInput` signature to accept a separate `currencyFieldName?: string` prop? — No. Keep it simple: hard-code `data-entry-field="currency"` directly on the Popover trigger button inside `AmountCurrencyInput.tsx`:

```tsx
            <Button
              type="button"
              variant="outline"
              disabled={loading || disabled}
              className="rounded-l-none border-l-0 px-2 font-mono text-xs"
              aria-label={`Currency: ${currency}`}
              data-entry-field="currency"
            >
```

(No new prop needed; the string `"currency"` is stable across all usages because the tab order only cares about it in the entry row. Edit-mode rendering doesn't use `focusFieldByName`.)

In `submit`, transform via `toCreatePayload` before calling `onSubmit`:

```ts
  const submit = useCallback(
    async (values: EntryFormValues) => {
      let payload: CreateTransactionInput;
      try {
        payload = toCreatePayload(values, baseCode, rateFor) as CreateTransactionInput;
      } catch {
        toast.error('Failed to save transaction');
        return;
      }
      let saved: Transaction;
      try {
        saved = await onSubmit(payload);
      } catch {
        toast.error('Failed to save transaction');
        return;
      }
      saveLastCategory(values.category_id);
      saveLastDate(values.date);
      saveLastCurrency(values.currency);
      undoBufferRef.current = { saved, values };
      // ... rest unchanged ...
      form.reset({
        date: values.date,
        amount: 0,
        currency: values.currency,   // sticky
        description: '',
        category_id: values.category_id,
        tags: '',
      });
      amountRef.current?.focus();
    },
    [onSubmit, form, undoLastSave, baseCode, rateFor],
  );
```

Add imports at the top:

```ts
import { toCreatePayload } from '@/lib/currency';
import { AmountCurrencyInput } from './AmountCurrencyInput';
import { useCurrencies } from '@/hooks/useCurrencies';
```

Gate the Save button on `currenciesLoading` + invalid-rate:

```tsx
          <Button
            type="submit"
            size="sm"
            className="h-8 text-xs"
            disabled={
              currenciesLoading ||
              (form.watch('currency') !== baseCode &&
                rateFor(form.watch('currency')) == null)
            }
          >
            Add
          </Button>
```

- [ ] **Step 6: Update undo buffer**

The `undoBufferRef` stored `{ saved, values }` where `values` is the full form shape. That still works because `form.reset(buf.values)` takes the form shape directly. No change needed beyond what's already in Step 5.

- [ ] **Step 7: Run the tests**

Run: `cd web && npm test -- src/components/TransactionEntryRow.test.tsx`
Expected: all PASS (Phase A–I regression + Phase J new tests).

- [ ] **Step 8: Run typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

If `Transactions.tsx:722-726` broke because of the `onSubmit` contract change, it shouldn't — the lambda `async (v) => { const tx = await createTransaction(v); ... }` already accepts whatever shape you hand it, and `createTransaction` types the input as `CreateTransactionInput`. If tsc complains about the prop shape, narrow there by typing the lambda's `v` parameter explicitly.

- [ ] **Step 9: Commit**

```bash
git add web/src/components/TransactionEntryRow.tsx web/src/components/TransactionEntryRow.test.tsx web/src/components/AmountCurrencyInput.tsx web/src/hooks/useTransactions.ts
git commit -m "feat(currency): wire currency selector into transaction entry row"
```

---

### Task 3.2: Wire `TransactionRow` view to `AmountDisplay` (TDD)

**Files:**
- Modify: `web/src/components/TransactionRow.tsx:200-210`
- Modify: `web/src/components/TransactionRow.test.tsx`

- [ ] **Step 1: Add failing tests**

Append to `web/src/components/TransactionRow.test.tsx`:

```tsx
describe('TransactionRow amount display', () => {
  it('renders single-line amount when original_* are null (regression)', () => {
    renderRow(makeTx({ amount: 25.5, original_amount: null, original_currency: null }));
    expect(screen.getByText(/-\$25\.50/)).toBeInTheDocument();
    expect(screen.queryByTestId('amount-display-secondary')).not.toBeInTheDocument();
  });

  it('renders two-line amount when original_currency differs from base', () => {
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
    );
    expect(screen.getByText(/-\$1\.67/)).toBeInTheDocument();
    expect(screen.getByTestId('amount-display-secondary')).toHaveTextContent('LBP');
  });

  it('falls back to single-line when original_currency equals base (defensive)', () => {
    renderRow(
      makeTx({
        amount: 25.5,
        original_amount: 25.5,
        original_currency: 'USD',
      }),
    );
    expect(screen.queryByTestId('amount-display-secondary')).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests, confirm they fail**

Run: `cd web && npm test -- src/components/TransactionRow.test.tsx`
Expected: new tests FAIL (secondary line is not rendered because we use a plain `<span>` today); existing tests still PASS.

- [ ] **Step 3: Replace the amount `<TableCell>`**

Edit `web/src/components/TransactionRow.tsx`. Replace lines 200-210 (the display-mode amount cell) with:

```tsx
      <TableCell className="whitespace-nowrap text-right">
        <AmountDisplay
          amount={transaction.amount}
          originalAmount={transaction.original_amount}
          originalCurrency={transaction.original_currency}
          type={transaction.category_type}
          baseCode={baseCurrency}
        />
      </TableCell>
```

Add the import: `import { AmountDisplay } from './AmountDisplay';`.

After removing the display-mode `<span>`, prune unused imports:

1. Run `cd web && npx tsc --noEmit` — TypeScript reports any newly-unused imports as `TS6133` ("declared but never used").
2. If `formatCurrency` is flagged unused, remove it from the `@/lib/format` import.
3. `TYPE_EXPENSE` stays only if the edit form references it; grep `rg '\bTYPE_EXPENSE\b' web/src/components/TransactionRow.tsx` — if there are no hits, remove it too.
4. Re-run `npx tsc --noEmit` to confirm a clean pass before committing.

- [ ] **Step 4: Run the tests**

Run: `cd web && npm test -- src/components/TransactionRow.test.tsx`
Expected: all PASS.

- [ ] **Step 5: Run typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/TransactionRow.tsx web/src/components/TransactionRow.test.tsx
git commit -m "feat(currency): render AmountDisplay with secondary original-currency line"
```

---

### Task 3.3: Wire `TransactionRow` edit to currency (TDD)

**Files:**
- Modify: `web/src/components/TransactionRow.tsx:57-85` (state + handleSave), `:107-146` (edit form amount cell)
- Modify: `web/src/components/TransactionRow.test.tsx`

Edit-mode swaps the bare `<Input type="number">` at line 138-146 for `<AmountCurrencyInput>` with `hideInactive={false}`, and routes the save through `toCreatePayload`. The edit form's save calls `onUpdate({ id, date, amount, description, category_id, tags })` today; extend it to pass the expanded shape.

**Why `hideInactive={false}` in edit mode** (spec Edge Case #1): historical rows may reference a currency that the admin has since deactivated. Entry mode hides inactive entries so new rows can't pick one, but edit mode must render them (flagged `(inactive)`) so users can save a correction on an existing row without being forced to first reactivate the currency. Hiding inactive entries in edit mode would make the affected rows impossible to edit — a regression trap noted in Firefly III #2515.

- [ ] **Step 1: Add failing tests**

Append to `web/src/components/TransactionRow.test.tsx`. Reuse the currency mock pattern from Task 3.1 — add the `@/api/client` mock at the top of the file if not already present:

```tsx
vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(async (path: string) => {
      if (path === 'currencies') {
        return [
          { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
        ];
      }
      return [];
    }),
  },
}));
```

Add tests (use `afterEach` to reset the module-level cache):

```tsx
import { __resetCurrenciesCacheForTests } from '../hooks/useCurrencies';

// near the describe blocks:
beforeEach(() => {
  __resetCurrenciesCacheForTests();
});

describe('TransactionRow edit — currency', () => {
  it('prefills picker with original_currency when set', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
      onUpdate,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    // The currency button in edit mode shows LBP.
    expect(
      await screen.findByRole('button', { name: /currency: lbp/i }),
    ).toBeInTheDocument();
    // And the amount input shows 150000 (the original typed amount).
    expect(screen.getByDisplayValue('150000')).toBeInTheDocument();
  });

  it('prefills picker with baseCode when original_* is null', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    expect(
      await screen.findByRole('button', { name: /currency: usd/i }),
    ).toBeInTheDocument();
  });

  it('_PrefillsBaseWhenOriginalNull_AfterLoad: race guard updates picker once currencies resolve to non-USD base', async () => {
    // Scenario: household base is EUR, row has original_* === null. The
    // first render captures DEFAULT_CURRENCY ("USD") as the fallback. After
    // the useCurrencies fetch resolves with is_base:true on EUR, the
    // didInitEditCurrency effect must switch the picker to EUR exactly once.
    // If this test fails it means the race guard is missing or fires twice.
    //
    // Uses a per-test override of the @/api/client mock to return EUR as base.
    const client = await import('@/api/client');
    const original = (client.api.get as ReturnType<typeof vi.fn>).getMockImplementation();
    (client.api.get as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path === 'currencies') {
        return [
          { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1.1, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'EUR', name: 'Euro', symbol: '€', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
        ];
      }
      return [];
    });
    __resetCurrenciesCacheForTests();

    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5, original_amount: null, original_currency: null }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    // After the fetch resolves, the picker flips from the USD fallback to EUR.
    expect(
      await screen.findByRole('button', { name: /currency: eur/i }),
    ).toBeInTheDocument();

    // Restore original mock for subsequent tests.
    if (original) {
      (client.api.get as ReturnType<typeof vi.fn>).mockImplementation(original);
    }
  });

  it('saves expanded payload when user switches to non-base currency', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: usd/i });

    // Flip to LBP and type a new amount.
    await user.click(screen.getByRole('button', { name: /currency: usd/i }));
    await user.click(await screen.findByRole('option', { name: /LBP/ }));
    const amountInput = screen.getByRole('spinbutton');
    await user.clear(amountInput);
    await user.type(amountInput, '150000');

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledTimes(1);
    });
    const payload = onUpdate.mock.calls[0][0];
    expect(payload).toMatchObject({
      id: 1,
      amount: 1.67,
      original_amount: 150000,
      original_currency: 'LBP',
    });
    expect(payload).not.toHaveProperty('currency');
  });

  it('saves collapsed payload when user switches back to base currency', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
      onUpdate,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: lbp/i });

    // Flip back to USD.
    await user.click(screen.getByRole('button', { name: /currency: lbp/i }));
    await user.click(await screen.findByRole('option', { name: /USD/ }));
    // Clear and set amount to 2.00 (base).
    const amountInput = screen.getByRole('spinbutton');
    await user.clear(amountInput);
    await user.type(amountInput, '2');

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledTimes(1);
    });
    const payload = onUpdate.mock.calls[0][0];
    expect(payload).toMatchObject({ id: 1, amount: 2 });
    expect(payload).not.toHaveProperty('original_amount');
    expect(payload).not.toHaveProperty('original_currency');
  });
});
```

- [ ] **Step 2: Run the tests, confirm they fail**

Run: `cd web && npm test -- src/components/TransactionRow.test.tsx`
Expected: new tests FAIL (picker not rendered in edit mode).

- [ ] **Step 3: Extend `TransactionRowProps.onUpdate` to carry the wire shape**

Edit `web/src/components/TransactionRow.tsx:37-45`:

```tsx
export interface TransactionRowProps {
  transaction: Transaction;
  categories: Category[];
  selected?: boolean;
  onSelect?: (id: number, checked: boolean) => void;
  onUpdate: (input: UpdateTransactionInput) => Promise<void>;
  onDelete: (id: number) => Promise<void>;
  onError: (message: string) => void;
}
```

`UpdateTransactionInput` is already exported by Task 3.1 Step 1 — no further change to `useTransactions.ts` here.

Add imports to TransactionRow.tsx:

```ts
import type { UpdateTransactionInput } from '@/hooks/useTransactions';
import { useCurrencies } from '@/hooks/useCurrencies';
import { AmountCurrencyInput } from './AmountCurrencyInput';
import { toCreatePayload, toEditDefaults } from '@/lib/currency';
```

- [ ] **Step 4: Add state + derive defaults in edit mode**

Inside the component body, add:

```ts
const { list: currencies, baseCode, rateFor, loading: currenciesLoading } = useCurrencies();

// Prefill edit-mode defaults from tx.original_* or fall back to baseCode.
// NOTE: `baseCode` is `DEFAULT_CURRENCY` ("USD") until the useCurrencies
// fetch resolves. The first-render `toEditDefaults` call therefore uses
// the fallback — that's fine for rows with original_* set (they override
// the fallback). For rows with original_* === null AND a non-USD household
// base, the initial editCurrency captures "USD" and stays stale after the
// fetch lands. The didInitCurrency effect below corrects this exactly once
// per edit session, without clobbering user input.
const defaults = toEditDefaults(transaction, baseCode);
const [editAmount, setEditAmount] = useState<number>(defaults.amount);
const [editCurrency, setEditCurrency] = useState<string>(defaults.currency);

// Race guard: if currenciesLoading was true when we rendered and now
// resolves to a non-USD baseCode, update editCurrency from the stale
// fallback to the real baseCode — but only when the row has null
// original_* (where the fallback is load-bearing) and only once.
const didInitEditCurrency = useRef(false);
useEffect(() => {
  if (didInitEditCurrency.current) return;
  if (currenciesLoading) return;
  didInitEditCurrency.current = true;
  // Recompute defaults now that baseCode is resolved.
  const resolved = toEditDefaults(transaction, baseCode);
  if (resolved.currency !== editCurrency) {
    setEditCurrency(resolved.currency);
  }
  if (resolved.amount !== editAmount) {
    setEditAmount(resolved.amount);
  }
  // editAmount/editCurrency intentionally excluded: this effect must run
  // exactly once per mount, gated by didInitEditCurrency.
  // eslint-disable-next-line react-hooks/exhaustive-deps
}, [currenciesLoading, baseCode, transaction]);
```

Remove the old `amount` state (`useState(String(transaction.amount))` at line 61). Update `handleCancel` to reset the new state using *the currently resolved* defaults:

```ts
function handleCancel() {
  const resolved = toEditDefaults(transaction, baseCode);
  setDate(transaction.date);
  setEditAmount(resolved.amount);
  setEditCurrency(resolved.currency);
  setDescription(transaction.description);
  setCategoryId(String(transaction.category_id));
  setTags(transaction.tags ?? '');
  setEditing(false);
}
```

Add the React imports if not already present: `useRef`, `useEffect` from `react`.

- [ ] **Step 5: Update `handleSave` to run `toCreatePayload`**

```ts
async function handleSave(e: FormEvent) {
  e.preventDefault();
  setSaving(true);
  let payload: UpdateTransactionInput;
  try {
    const wire = toCreatePayload(
      {
        amount: editAmount,
        currency: editCurrency,
        date,
        description,
        category_id: parseInt(categoryId, 10),
        tags,
      },
      baseCode,
      rateFor,
    );
    payload = { id: transaction.id, ...wire } as UpdateTransactionInput;
  } catch (err) {
    onError(err instanceof Error ? err.message : 'Invalid currency rate');
    setSaving(false);
    return;
  }
  try {
    await onUpdate(payload);
    setEditing(false);
  } catch (err) {
    onError(err instanceof Error ? err.message : 'Failed to save');
  } finally {
    setSaving(false);
  }
}
```

- [ ] **Step 6: Replace the edit-mode amount cell (line 138-146)**

```tsx
        <TableCell className="text-right font-mono tabular-nums">
          <AmountCurrencyInput
            value={editAmount}
            onValueChange={setEditAmount}
            currency={editCurrency}
            onCurrencyChange={setEditCurrency}
            baseCode={baseCode}
            currencies={currencies}
            hideInactive={false}
            rateFor={rateFor}
            loading={currenciesLoading}
            error={
              editCurrency !== baseCode && rateFor(editCurrency) == null
                ? 'No rate configured for this currency. Set one in Settings.'
                : null
            }
          />
        </TableCell>
```

Gate the Save button on the same invalid-rate condition:

```tsx
            <Button
              type="submit"
              size="sm"
              disabled={
                saving ||
                currenciesLoading ||
                (editCurrency !== baseCode && rateFor(editCurrency) == null)
              }
            >
              Save
            </Button>
```

- [ ] **Step 7: Run the tests**

Run: `cd web && npm test -- src/components/TransactionRow.test.tsx`
Expected: all PASS (regression + new edit-mode tests).

- [ ] **Step 8: Run typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 9: Commit**

```bash
git add web/src/components/TransactionRow.tsx web/src/components/TransactionRow.test.tsx web/src/hooks/useTransactions.ts
git commit -m "feat(currency): wire currency selector into transaction edit row"
```

---

## Chunk 4: End-to-end verification + docs

Nothing new to build. Run the full suite, eyeball the UI once in a live dev server, update README.

### Task 4.1: Full test + type suite passes

- [ ] **Step 1: Run the full web test suite**

Run: `cd web && npm test`
Expected: ALL tests pass. Pay attention to any unrelated test that may have been touched by the changes — common regression points are Dashboard, Reports, Trash (they use `useBaseCurrency` not `useCurrencies`, so they shouldn't change, but confirm).

- [ ] **Step 2: Run the full typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Run the backend test suite (regression guard)**

Run from `D:\claude\SpenDrop`: `MSYS_NO_PATHCONV=1 docker run --rm -v /d/claude/SpenDrop:/app -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build -w /app spendrop-go-test:1.26 sh -c "go test -race -count=1 ./..."`
Expected: all PASS. (Backend is untouched so nothing should break; this is the safety net.)

- [ ] **Step 4: No commit needed unless something failed and was fixed.**

If any fix was required, commit it with a tight conventional-commit message naming the regression source.

---

### Task 4.2: Manual dev-server verification

- [ ] **Step 1: Start the dev server + backend locally**

Follow whatever the project's normal dev-up procedure is — typically `docker compose up` from repo root. If you don't know how to start SpenDrop's dev stack, ask the user; do NOT guess.

- [ ] **Step 2: Exercise the golden path**

Create a transaction in LBP:
1. Open the Transactions page.
2. Click "Add Transaction" (or whatever reveals the entry row).
3. Type 150000 in the amount field.
4. Click the currency suffix button (default USD), pick LBP.
5. Confirm the `≈ $X.XX` preview appears under the input.
6. Fill date/description/category; click Add.
7. Confirm the list row now shows two lines: base amount on top, `150,000 LBP` secondary line.

- [ ] **Step 3: Exercise edit round-trip**

1. Click Edit on the row you just created.
2. Confirm the amount input shows 150000 (not 1.67), currency button shows LBP.
3. Save without changes → confirm the list row is unchanged.
4. Edit again, change currency back to USD, change amount to 2.00, save → confirm the secondary line disappears.

- [ ] **Step 4: Exercise edge cases**

1. Open Settings → Currencies → delete a currency's rate (set to 0) if the UI allows. Return to Transactions, try to pick that currency in the entry row. Confirm it's disabled with a tooltip.
2. Open Settings → add a new currency with is_active=false (if the UI allows). In entry mode, confirm it doesn't appear. In edit mode for a row using that currency, confirm it shows `(inactive)`.

- [ ] **Step 4b: Firefly-class failure-mode sweep**

These are the specific failure modes the spec was written to prevent. Each is a manual rehearsal of the exact Firefly III bug:

1. **Zombie `original_*` clear (Firefly #10791)** — Create a transaction in LBP with amount 150000. Edit it, flip the currency back to your household base (e.g. USD), change amount to 5.00, Save. Open DevTools → Network, inspect the PUT payload: confirm `original_amount` and `original_currency` are **absent from the JSON body** (not `null`, not `undefined` — absent). Then re-open the row for edit: confirm the secondary "original" line is gone and the amount input shows 5.00.
2. **Silent rate=1 fallback (Firefly #11616)** — Create a transaction with the currency button left at your base (default USD when USD is base). Confirm no `≈` preview appears while typing. Inspect the POST payload: confirm `original_*` keys are absent. Then verify the list row has NO secondary line under the base amount.
3. **Non-base currency with `rate_to_base === 1` is honored** — In Settings, add a test currency (e.g. `XTS`) with `rate_to_base = 1.0`. Return to Transactions, create a row in XTS with amount 42. Confirm the `≈` preview shows `$42.00` (not blank, not blocked). Save → confirm the list row shows the base amount AND the `42 XTS` secondary line (because XTS ≠ baseCode even though the rate is numerically 1).
4. **Edit-unrelated-field preservation (Firefly #2180)** — Using the 150,000 LBP row created in Step 2 (or create a fresh LBP row if Step 3 already mutated it), open Edit, change ONLY the description (leave amount and currency untouched), Save. Inspect the PUT payload: confirm `original_amount=150000` and `original_currency="LBP"` are both still present and unchanged. Re-open the list: the `150,000 LBP` secondary line must still be there.

If any sub-step fails, treat as a real bug — do NOT mark this task complete. Re-enter Chunk 3 to fix.

- [ ] **Step 5: No commit needed unless a fix was required.**

---

### Task 4.3: Update docs

**Files:**
- Modify: `README.md` (if multi-currency is mentioned) — check first.
- Modify: `DESIGN_GUIDE.md` (if it lists components) — check first.

- [ ] **Step 1: Grep for existing currency documentation**

Run from repo root (Git Bash on Windows, or any POSIX shell):

```bash
grep -rnE "multi-currency|original_currency|original_amount" --include="*.md" .
```

Note which docs touch the feature area. At minimum expect hits in `README.md` and possibly `DESIGN_GUIDE.md`. Any doc that currently describes the backend half of the feature needs a pointer to the new UI; don't assume `README.md` is the only one.

- [ ] **Step 2: Add a short section describing the new UI**

Append to `README.md` under whatever section describes transaction entry (or create a "Multi-currency transactions" subsection):

```markdown
### Multi-currency transactions

The transaction entry row has an inline currency selector. Pick a currency other than your household base (configured in Settings → Currencies) to record the original-currency amount; SpenDrop divides by the configured `rate_to_base` and stores the base-currency value as the authoritative ledger amount. The list view shows both: the canonical base amount on top, and the original-currency amount as a muted secondary line.

Caveats:
- Every non-base currency must have a configured `rate_to_base` in Settings. If the rate is missing or zero, Save is blocked.
- The `≈` preview shown while typing is frontend-approximate; the persisted value is the backend's recomputed amount (they round identically so they agree to the cent).
- Inactive currencies don't appear in the entry-row picker but remain selectable on edit so historical rows round-trip.
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs(readme): describe transaction currency selector"
```

- [ ] **Step 4: Update DESIGN_GUIDE.md**

Per project rule "Keep docs updated — README.md and DESIGN_GUIDE.md with every change/feature": DESIGN_GUIDE.md must be updated, not conditionally. Find the component catalog or inventory section (if there is none, create a brief one under existing structure) and register both new primitives:

```markdown
### AmountCurrencyInput

A composite input for typing a monetary amount together with its currency. Renders a numeric input with a currency-code suffix button that opens a Popover+Command picker. Calls `onAmountChange` and `onCurrencyChange` separately so parents can hold joint `{amount, currency}` state without coupling.

Use for: transaction entry row, transaction edit row. Not used for display.

Props: `amount: string`, `currency: string`, `onAmountChange`, `onCurrencyChange`, `baseCode`, `currencies`, `rateFor(code) → number | null`, `hideInactive?: boolean` (default true in entry, false in edit), plus standard input attrs (`id`, `placeholder`, `className`, `onBlur`, `onKeyDown`, ref).

### AmountDisplay

Read-only display for an already-persisted transaction amount. Renders the base-currency amount as the primary line; if the row has a non-null `original_amount` + `original_currency`, renders a muted secondary line showing the original value. Never shows an `≈` prefix — the base amount came from the backend and is canonical.

Use for: transaction list rows. Not used for inputs or pre-save previews.

Props: `amountCents: number`, `type: TransactionType`, `originalAmount?: number | null`, `originalCurrency?: string | null`, `baseCurrency: string`, `className?: string`.
```

Commit:

```bash
git add DESIGN_GUIDE.md
git commit -m "docs(design): list AmountCurrencyInput and AmountDisplay"
```

---

## Final checklist

Before handing off to `superpowers:finishing-a-development-branch`:

- [ ] All 4 chunks committed.
- [ ] `cd web && npm test` passes cleanly.
- [ ] `cd web && npx tsc --noEmit` passes cleanly.
- [ ] Backend test suite passes cleanly.
- [ ] Dev-server manual verification done (Task 4.2).
- [ ] Docs updated (Task 4.3).
- [ ] `git log origin/main..HEAD --oneline` shows a tidy, bisectable sequence.

---

## Invariants to preserve (spec §Edge Cases recap)

- `currency === baseCode` → payload has NO `original_*` keys (not `null`, not `undefined` — absent).
- `original_amount` and `original_currency` are either both present or both absent.
- Currency change does NOT mutate the typed amount — only the `≈` preview recomputes.
- Focus-preserving: amount input's DOM value is untouched while currency flips.
- `rateFor() === null` disables picker entry + blocks Save (no silent rate=1 fallback).
- Entry mode hides inactive currencies; edit mode shows them with `(inactive)` suffix.
- Post-save `AmountDisplay` does NOT use `≈` — the value came from the backend and is canonical.

## Out of scope (spec §Non-Goals recap)

- Admin Currency CRUD UI (already in Settings; not touched).
- Per-account default currency (SpenDrop has no accounts).
- Dashboard / Reports / Exports (they read `amount_cents` which is unchanged).
- Import preview column for `original_*` (separate feature).
- Backend changes to `resolveCurrency` or schema.
- Non-2-decimal base currencies (known limitation — spec §Edge Case 7).
