import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { ReportYearFloorResponse } from '@/api/types';
import { MIN_YEAR } from '@/lib/dates';

export interface UseReportYearFloorResult {
  /**
   * Oldest year the Reports year pickers may offer, already guarded against
   * both ends of the contract. Pass straight into `yearOptions(floorYear)`.
   */
  floorYear: number;
  /** False when the ledger holds no live rows; `floorYear` is then the
   *  current-year fallback rather than real data. */
  hasTransactions: boolean;
  /** True when live rows exist below `MIN_YEAR`. Those amounts still land in
   *  every date-range aggregate, but their year cannot be selected — the one
   *  signal that some imported data is unreachable in Reports. */
  clamped: boolean;
  /** True only on the very first fetch. The picker is already usable while it
   *  is true (see the fallback below), so this is for hints, not for gating. */
  loading: boolean;
}

/**
 * The Reports year-picker floor, derived from the ledger via
 * `GET /api/settings/report-year-floor`.
 *
 * Replaces `HISTORICAL_YEAR_START = 2024` in `@/lib/dates`. Import accepts
 * dates back to 1900, so that constant meant a user could import a 2019 bank
 * statement, have every aggregate include it, and never be able to select 2019
 * in any Reports tab.
 *
 * Keyed `['reports', 'year-floor']` — same first segment as every hook in
 * `useReports.ts`, so the SSE subscriber's
 * `invalidateQueries({ queryKey: ['reports'] })` prefix-matches it and adding
 * or deleting an older transaction re-derives the floor without a reload.
 * `staleTime`/`retry` come from the app-wide client defaults (`@/lib/queryClient`).
 *
 * DEGRADATION: the picker must never be empty, so `floorYear` falls back to the
 * browser's current year whenever there is no successful response — first
 * paint, 401, 500, offline. That is deliberately the NARROWEST non-empty
 * default: every tab selects the current year on mount, so the real floor
 * arriving later only ever WIDENS the option list. A wider guess (e.g. the old
 * 2024) could be narrowed by the response and strip an already-selected year
 * out of the `<Select>`, leaving a blank trigger.
 */
export function useReportYearFloor(): UseReportYearFloorResult {
  const query = useQuery({
    queryKey: ['reports', 'year-floor'],
    queryFn: () =>
      api.get<ReportYearFloorResponse>('settings/report-year-floor'),
  });

  const currentYear = new Date().getFullYear();
  const data = query.data;

  // Two guards, in this order:
  //
  //  - `Math.max(…, MIN_YEAR)` mirrors the handler's own clamp. It only bites
  //    against an older/rolled-back binary, but the cost of missing it is that
  //    the picker offers a year every year-param endpoint then 400s on.
  //  - `Math.min(…, currentYear)` is mandatory. `floor_year` is bounded by the
  //    SERVER's clock; across a New Year boundary a server ahead of the browser
  //    returns a floor above the browser's ceiling, and `yearOptions` counts
  //    DOWN from that ceiling — an empty dropdown.
  const floorYear = Math.min(
    Math.max(data?.floor_year ?? currentYear, MIN_YEAR),
    currentYear,
  );

  return {
    floorYear,
    hasTransactions: data?.has_transactions ?? false,
    clamped: data?.clamped ?? false,
    loading: query.isLoading,
  };
}
