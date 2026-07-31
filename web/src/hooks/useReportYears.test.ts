import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';

import { useReportYears } from './useReportYears';

// This suite deliberately mocks `fetch`, NOT `@/api/client`.
//
// Mocking the client would let the hook ask for the wrong path, or read the
// wrong JSON field, and stay green — the same class of false-green that let a
// mocked SSE test pass an entire review while the real feature did nothing in
// production (the mock called `onmessage`; the server sends a named event).
// Stubbing at the network boundary means every assertion below fails if the
// URL, the `/api` prefix, or any wire field name (`years`, `current_year`,
// `has_transactions`, `out_of_range_years`) drifts from the Go handler.
const YEARS_URL = '/api/reports/years';

interface YearsBody {
  years: number[];
  current_year: number;
  has_transactions: boolean;
  out_of_range_years: number[];
}

const fetchMock = vi.fn();

/** Resolve every request with `body`, recording the URL it was asked for. */
function respondWith(body: Partial<YearsBody>, status = 200) {
  fetchMock.mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  });
}

function requestedUrls(): string[] {
  return fetchMock.mock.calls.map((c) => String(c[0]));
}

function makeHarness() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return { client, wrapper };
}

const CURRENT_YEAR = 2026;

describe('useReportYears', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    // Pinned so "the browser's current year" is deterministic; otherwise the
    // clock-skew case silently changes meaning every January.
    vi.setSystemTime(new Date(Date.UTC(CURRENT_YEAR, 6, 15)));
    fetchMock.mockReset();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('asks the real endpoint and exposes every field of the contract', async () => {
    respondWith({
      years: [2026, 2024, 1984],
      current_year: 2026,
      has_transactions: true,
      out_of_range_years: [3021, 1850],
    });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    await waitFor(() =>
      expect(result.current.years).toEqual([2026, 2024, 1984]),
    );
    expect(result.current.currentYear).toBe(2026);
    expect(result.current.hasTransactions).toBe(true);
    expect(result.current.outOfRangeYears).toEqual([3021, 1850]);
    expect(requestedUrls()).toContain(YEARS_URL);
  });

  it('keeps the gap in a sparse ledger', async () => {
    // The headline behaviour: 2025 holds no rows, so it is not offered. The
    // old floor-based contract could only express a contiguous range and
    // would have produced 43 options here.
    respondWith({
      years: [2026, 2024, 1984],
      current_year: 2026,
      has_transactions: true,
      out_of_range_years: [],
    });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.years).not.toContain(2025);
  });

  it('offers the browser current year while the request is in flight', () => {
    // Never resolves: this is the first paint, before any response arrives.
    fetchMock.mockReturnValue(new Promise(() => {}));
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    expect(result.current.years).toEqual([CURRENT_YEAR]);
    expect(result.current.currentYear).toBe(CURRENT_YEAR);
    expect(result.current.loading).toBe(true);
  });

  it('offers exactly the current year when the ledger is empty', async () => {
    respondWith({
      years: [CURRENT_YEAR],
      current_year: CURRENT_YEAR,
      has_transactions: false,
      out_of_range_years: [],
    });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    await waitFor(() => expect(result.current.hasTransactions).toBe(false));
    expect(result.current.years).toEqual([CURRENT_YEAR]);
  });

  it('degrades to a usable one-year picker when the request 401s', async () => {
    respondWith({}, 401);
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.years).toEqual([CURRENT_YEAR]);
    expect(result.current.hasTransactions).toBe(false);
    expect(result.current.outOfRangeYears).toEqual([]);
  });

  it('degrades to a usable one-year picker when the request fails outright', async () => {
    fetchMock.mockRejectedValue(new TypeError('network'));
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.years).toEqual([CURRENT_YEAR]);
  });

  it('never returns an empty list, even if the server sends one', async () => {
    // The handler unions the current year in so `years` is never empty, but
    // an older or rolled-back binary is not bound by that promise. An empty
    // list would render every year Select with no options at all.
    respondWith({
      years: [],
      current_year: 2026,
      has_transactions: true,
      out_of_range_years: [],
    });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.years).toEqual([CURRENT_YEAR]);
  });

  it('treats a missing out_of_range_years as no out-of-range years', async () => {
    // The field is non-optional in the TS type, but JSON from an older binary
    // is not type-checked. The Reports notice reads `.length`, so an undefined
    // here would throw and unmount the whole page.
    respondWith({
      years: [2026],
      current_year: 2026,
      has_transactions: true,
    });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.outOfRangeYears).toEqual([]);
  });

  it('trusts the server clock for currentYear, not the browser', async () => {
    // `years` is capped at the SERVER's current year, so "which entry is this
    // year" must be answered by the same clock that built the list. Across a
    // New Year boundary the two disagree, and taking the browser's answer
    // would point at a year that is not in the list.
    respondWith({
      years: [2027, 2026],
      current_year: 2027,
      has_transactions: true,
      out_of_range_years: [],
    });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });

    await waitFor(() => expect(result.current.currentYear).toBe(2027));
    expect(result.current.years).toContain(2027);
  });

  it('refetches when the ["reports"] resource is invalidated (the SSE path)', async () => {
    // Every transaction mutation publishes the bare resource name "reports"
    // (transaction_handlers.go), and useLiveUpdates invalidates
    // `queryKey: [resource]`. So this key's FIRST segment must be 'reports'
    // for adding a 1984 row to make 1984 appear in the picker without a
    // reload. That is the whole of the SSE wiring — there is no other.
    respondWith({
      years: [2026],
      current_year: 2026,
      has_transactions: true,
      out_of_range_years: [],
    });
    const { client, wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYears(), { wrapper });
    await waitFor(() => expect(result.current.years).toEqual([2026]));

    respondWith({
      years: [2026, 1984],
      current_year: 2026,
      has_transactions: true,
      out_of_range_years: [],
    });
    await client.invalidateQueries({ queryKey: ['reports'] });

    await waitFor(() => expect(result.current.years).toEqual([2026, 1984]));
  });
});
