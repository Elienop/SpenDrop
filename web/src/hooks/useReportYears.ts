import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { ReportYearsResponse } from '@/api/types';

export interface UseReportYearsResult {
  /**
   * Every year the pickers may offer, newest first. Never empty. Pass to
   * `yearOptionsFrom(years, ...currentSelections)`.
   */
  years: number[];
  /** Which entry is "this year", per the SERVER's clock — the same clock that
   *  capped `years`. */
  currentYear: number;
  /** False when the ledger holds no live rows at all. Tracks ROWS, not offered
   *  years: a household whose only row is dated 3021 still has transactions. */
  hasTransactions: boolean;
  /** Years the ledger holds that are OUTSIDE the data window [1900, 2100] —
   *  legacy or corrupt rows. Every year-param endpoint 400s on them, so no
   *  report can ever reach them. Empty for the ordinary household. */
  outOfRangeYears: number[];
  /** Years the ledger holds that are later than `currentYear` but inside the
   *  data window — a household planning ahead. The endpoints accept these; only
   *  the picker's cap withholds them, and it lifts when the year arrives.
   *
   *  Kept SEPARATE from `outOfRangeYears` on purpose: they were one list, and
   *  the Reports notice therefore described a deliberate 2027 entry in the
   *  same sentence as a corrupt 1850 row. A year in both buckets (3021) is
   *  reported by the server as out-of-range only, so a consumer can render
   *  both lists without naming a year twice. */
  futureYears: number[];
  /** True only on the very first fetch. The pickers are already usable while it
   *  is true (see the fallback below), so this is for hints, not for gating. */
  loading: boolean;
}

/**
 * The years the Reports and Dashboard year pickers offer, from the ledger via
 * `GET /api/reports/years`.
 *
 * Replaces `useReportYearFloor`, which could only describe a contiguous range
 * from a single floor. A ledger is full of gaps: a household with rows in 1984
 * and 2026 was offered forty-three years, forty-one of them empty.
 *
 * SSE INVALIDATION IS FREE, AND THE KEY IS WHAT BUYS IT. Keyed
 * `['reports', 'years']` — first segment 'reports', same as every hook in
 * `useReports.ts`. Every transaction mutation already publishes the bare
 * resource name "reports" (nine call sites in `transaction_handlers.go`), and
 * `useLiveUpdates` invalidates `queryKey: [resource]`, which prefix-matches.
 * So adding or deleting a 1984 row re-derives the picker with no new wiring at
 * all. Renaming the first segment silently severs that; the SSE test in this
 * hook's suite is what stops it. `staleTime`/`retry` come from the app-wide
 * client defaults (`@/lib/queryClient`).
 *
 * DEGRADATION. The pickers must never be empty, so `years` falls back to
 * `[browser current year]` whenever there is no usable response — first paint,
 * 401, 500, offline. That is deliberately the NARROWEST non-empty list: every
 * tab selects the current year on mount, so a real response arriving later can
 * only WIDEN the options and can never strip a value out from under a live
 * `<Select>`. A wider guess could be NARROWED by the response, which renders a
 * blank trigger with no error — see `yearOptionsFrom`.
 */
export function useReportYears(): UseReportYearsResult {
  const query = useQuery({
    queryKey: ['reports', 'years'],
    queryFn: () => api.get<ReportYearsResponse>('reports/years'),
  });

  const data = query.data;
  // The browser's clock is the fallback ONLY. Once a response lands, the
  // server's `current_year` wins: it is the clock that capped `years`, so
  // mixing in the browser's answer could point at a year the list lacks.
  const currentYear = data?.current_year ?? new Date().getFullYear();

  // `years` is guaranteed non-empty by the handler, but an older or
  // rolled-back binary is not bound by that promise, and an empty list renders
  // a Select with no options at all.
  const years =
    data?.years && data.years.length > 0 ? data.years : [currentYear];

  return {
    years,
    currentYear,
    hasTransactions: data?.has_transactions ?? false,
    // `?? []` is load-bearing despite the non-optional type: JSON from an
    // older binary is not type-checked, and the Reports notice reads
    // `.length` on both — an undefined would throw and unmount the page.
    // `future_years` makes that concrete rather than defensive: every binary
    // before this change omits the key, so a rollback hits this path.
    outOfRangeYears: data?.out_of_range_years ?? [],
    futureYears: data?.future_years ?? [],
    loading: query.isLoading,
  };
}
