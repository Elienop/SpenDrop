import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';

import { useReportYearFloor } from './useReportYearFloor';
import { MIN_YEAR } from '@/lib/dates';

// This suite deliberately mocks `fetch`, NOT `@/api/client`.
//
// Mocking the client would let the hook ask for the wrong path, or read the
// wrong JSON field, and stay green — the same class of false-green that let a
// mocked SSE test pass an entire review while the real feature did nothing.
// Stubbing at the network boundary means the assertions below fail if the URL,
// the `/api` prefix, or any wire field name (`floor_year`, `has_transactions`,
// `clamped`) drifts from the Go handler's contract.
const FLOOR_URL = '/api/settings/report-year-floor';

interface FloorBody {
  floor_year: number;
  has_transactions: boolean;
  clamped: boolean;
}

const fetchMock = vi.fn();

/** Resolve every request with `body`, recording the URL it was asked for. */
function respondWith(body: FloorBody, status = 200) {
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

describe('useReportYearFloor', () => {
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

  it('asks the real endpoint and exposes the ledger floor', async () => {
    respondWith({ floor_year: 2019, has_transactions: true, clamped: false });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });

    await waitFor(() => expect(result.current.floorYear).toBe(2019));
    expect(result.current.hasTransactions).toBe(true);
    expect(result.current.clamped).toBe(false);
    expect(requestedUrls()).toContain(FLOOR_URL);
  });

  it('reports the current year while the request is still in flight', () => {
    // Never resolves: this is the first paint, before any response arrives.
    fetchMock.mockReturnValue(new Promise(() => {}));
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });

    expect(result.current.floorYear).toBe(CURRENT_YEAR);
    expect(result.current.loading).toBe(true);
  });

  it('falls back to the current year on an empty ledger', async () => {
    respondWith({
      floor_year: CURRENT_YEAR,
      has_transactions: false,
      clamped: false,
    });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });

    await waitFor(() => expect(result.current.hasTransactions).toBe(false));
    expect(result.current.floorYear).toBe(CURRENT_YEAR);
  });

  it('never lets a server clock ahead of the browser push the floor above the ceiling', async () => {
    // Clock skew across a New Year boundary: the server already thinks it is
    // 2027 and clamps its floor to its own current year. The picker counts
    // DOWN from the BROWSER's current year, so an unguarded 2027 floor with a
    // 2026 ceiling produces an empty dropdown.
    respondWith({ floor_year: 2027, has_transactions: true, clamped: false });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.floorYear).toBe(CURRENT_YEAR);
    expect(result.current.floorYear).toBeLessThanOrEqual(CURRENT_YEAR);
  });

  it('holds the floor at MIN_YEAR if a server ever returns something older', async () => {
    // The handler clamps to MinYear itself, so this only fires against an
    // older/rolled-back binary. Offering 1995 would make every year-param
    // request 400 — the picker must not be able to select an unusable year.
    respondWith({ floor_year: 1995, has_transactions: true, clamped: true });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.floorYear).toBe(MIN_YEAR);
  });

  it('surfaces clamped so the UI can say the pre-2000 rows are unreachable', async () => {
    respondWith({ floor_year: MIN_YEAR, has_transactions: true, clamped: true });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });

    await waitFor(() => expect(result.current.clamped).toBe(true));
    expect(result.current.floorYear).toBe(MIN_YEAR);
  });

  it('degrades to a usable current-year floor when the request 401s', async () => {
    respondWith(
      { floor_year: 0, has_transactions: false, clamped: false },
      401,
    );
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.floorYear).toBe(CURRENT_YEAR);
    expect(result.current.clamped).toBe(false);
    expect(result.current.hasTransactions).toBe(false);
  });

  it('degrades to a usable current-year floor when the request fails outright', async () => {
    fetchMock.mockRejectedValue(new TypeError('network'));
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.floorYear).toBe(CURRENT_YEAR);
  });

  it('refetches when the ["reports"] resource is invalidated (the SSE path)', async () => {
    // The SSE subscriber invalidates by bare resource name, so the key's first
    // segment must be 'reports' for a transaction mutation to widen the floor.
    respondWith({ floor_year: 2024, has_transactions: true, clamped: false });
    const { client, wrapper } = makeHarness();

    const { result } = renderHook(() => useReportYearFloor(), { wrapper });
    await waitFor(() => expect(result.current.floorYear).toBe(2024));

    respondWith({ floor_year: 2019, has_transactions: true, clamped: false });
    await client.invalidateQueries({ queryKey: ['reports'] });

    await waitFor(() => expect(result.current.floorYear).toBe(2019));
  });
});
