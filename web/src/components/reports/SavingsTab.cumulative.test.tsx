import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, cleanup, waitFor } from '@testing-library/react';
import React from 'react';
import type { IncomeExpenseEntry, YoYResponse } from '@/api/types';

// Separate file from `SavingsTab.test.tsx` because of the mock below: that file
// deliberately renders with an UNSIZED ResponsiveContainer (happy-dom measures
// its parent as 0x0 and recharts bails), so no chart body exists there to read
// a series off. Sizing it is file-scoped, so the two environments cannot share
// a file.
//
// SIZING-ONLY mock, the pattern proven in `components/ui/chart.test.tsx`:
// everything below the container is the genuine library, so what is asserted
// here is the geometry recharts actually emitted.
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    // Typed explicitly: `cloneElement` has no overload for an element whose
    // props are unknown, so the size props must be declared for `tsc -b` —
    // which type-checks test files, unlike `--noEmit`.
    ResponsiveContainer: ({
      children,
    }: {
      children: React.ReactElement<{ width?: number; height?: number }>;
    }) => React.cloneElement(children, { width: 400, height: 300 }),
  };
});

const YEAR = 2026;

// Three months whose signed nets make the two candidate series DISAGREE in
// shape, not just in scale: the cumulative curve runs 1000 -> 700 -> 1200
// (dipping, then finishing highest), while a non-accumulating one would run
// 1000 -> -300 -> 500 (finishing BELOW its start, and crossing zero). The
// negative month is the point — SavingsTab deliberately does not clamp, so a
// losing month must pull the curve down without erasing the total before it.
const NETS = [1000, -300, 500];
const CUMULATIVE = [1000, 700, 1200];

// A fourth month in ANOTHER year, larger than every net above. SavingsTab
// filters `incExp.data` to the selected year before accumulating; without a
// foreign row here, deleting that filter would leave this file green.
const INC_EXP: IncomeExpenseEntry[] = [
  ...NETS.map((net, i) => ({
    year: YEAR,
    month: i + 1,
    income: net > 0 ? net : 0,
    expenses: net > 0 ? 0 : -net,
    net,
  })),
  { year: YEAR - 1, month: 12, income: 9000, expenses: 0, net: 9000 },
];

const emptyYoY: YoYResponse = {
  current_year: YEAR,
  previous_year: YEAR - 1,
  current: [],
  previous: [],
};

const useIncomeExpenses = vi.fn();
const useYearOverYear = vi.fn();
vi.mock('@/hooks/useReports', () => ({
  useIncomeExpenses: (months: number) => useIncomeExpenses(months),
  useYearOverYear: (year: number) => useYearOverYear(year),
}));

const apiGet = vi.fn();
vi.mock('@/api/client', () => ({
  api: { get: (...args: unknown[]) => apiGet(...args) },
}));

vi.mock('@/hooks/useBaseCurrency', () => ({ useBaseCurrency: () => 'USD' }));

vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => ({
    years: [YEAR],
    currentYear: YEAR,
    hasTransactions: true,
    outOfRangeYears: [],
    futureYears: [],
    loading: false,
  }),
}));

import { SavingsTab } from './SavingsTab';

/**
 * The formatted labels of a chart's Y axis, in document order.
 *
 * recharts 3 hoists tick LABELS out of `.recharts-yAxis` into their own
 * z-index layer, so `.recharts-yAxis .recharts-cartesian-axis-tick-value`
 * matches nothing on this version — which reads as "the axis rendered no
 * labels" rather than as a failure. Query the label group by its own class.
 */
function axisTickLabels(scope: Element): string[] {
  const labels = scope.querySelector('.recharts-yAxis-tick-labels');
  if (!labels) throw new Error('chart rendered no y-axis labels');
  return Array.from(
    labels.querySelectorAll('.recharts-cartesian-axis-tick-value'),
  ).map((el) => el.textContent?.trim() ?? '');
}

/**
 * The dollar values an `<Area>` actually plotted, recovered from the chart's
 * own Y axis.
 *
 * A `type="monotone"` curve is emitted as `M x,y` followed by one cubic
 * `C cx1,cy1,cx2,cy2,x,y` per subsequent point, so each segment's LAST
 * coordinate pair is a data vertex. The pixel `y` means nothing on its own, so
 * it is mapped back into dollars through the linear scale the rendered ticks
 * define (every tick `<text>` carries both its pixel `y` and its formatted
 * label). Reading the axis rather than hard-coding a plot size keeps the
 * assertion in the units the reader thinks in, and keeps it valid if the chart
 * is ever resized.
 *
 * (Duplicated from `OverviewTab.test.tsx`, which pins the same invariant on the
 * Net Cash Flow curve. Kept local to each file rather than shared, so neither
 * test can be silently retargeted by an edit to the other's helper.)
 */
function plottedAreaValues(scope: Element): number[] {
  const ticks = Array.from(
    scope.querySelectorAll(
      '.recharts-yAxis-tick-labels .recharts-cartesian-axis-tick-value',
    ),
  ).map((el) => ({
    px: Number(el.getAttribute('y')),
    // `en-US` currency, so a negative tick is "-$300.00": strip everything
    // that is not a digit, a decimal point or the leading sign.
    value: Number((el.textContent ?? '').replace(/[^0-9.-]/g, '')),
  }));
  if (ticks.length < 2) throw new Error('y axis rendered fewer than two ticks');

  const lo = ticks[0];
  const hi = ticks[ticks.length - 1];
  const dollarsPerPx = (hi.value - lo.value) / (hi.px - lo.px);

  const curve = scope.querySelector('.recharts-area-curve');
  if (!curve) throw new Error('area chart rendered no curve');
  const d = curve.getAttribute('d') ?? '';

  return d
    .split(/(?=[MC])/)
    .filter((segment) => segment.length > 1)
    .map((segment) => {
      const nums = segment.slice(1).split(',').map(Number);
      const y = nums[nums.length - 1];
      return lo.value + (y - lo.px) * dollarsPerPx;
    });
}

/** The Savings Goal card, located by the heading it is labelled by. */
function savingsCard(container: HTMLElement): HTMLElement {
  const el = container.querySelector<HTMLElement>(
    '[aria-labelledby="savings-goal-heading"]',
  );
  if (!el) throw new Error('Savings Goal card did not render');
  return el;
}

// Nothing pinned this series before: `SavingsTab.test.tsx` feeds an EMPTY
// income/expenses list, so a cumulative curve and a per-month one are the same
// chart there (both empty). Replacing the accumulation with `savings: e.net`
// left the whole frontend suite green.
describe('SavingsTab cumulative savings curve', () => {
  beforeEach(() => {
    useIncomeExpenses.mockReturnValue({
      data: INC_EXP,
      loading: false,
      fetching: false,
      error: '',
    });
    useYearOverYear.mockReturnValue({
      data: emptyYoY,
      loading: false,
      fetching: false,
      error: '',
    });
    apiGet.mockResolvedValue([
      { id: 1, year: YEAR, target_amount: 5000, current_amount: 0 },
    ]);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  test('plots the running total of monthly net, not each month on its own', async () => {
    const { container } = render(<SavingsTab />);
    const card = savingsCard(container);

    // The `<Area>` mounts before its geometry does — the curve `<path>` arrives
    // on the first animation frame — so wait for it rather than reading an
    // empty chart and calling the series correct.
    await waitFor(() => {
      expect(card.querySelector('.recharts-area-curve')).not.toBeNull();
    });

    expect(plottedAreaValues(card)).toEqual(
      CUMULATIVE.map((v) => expect.closeTo(v, 4)),
    );
  });

  test('the Y axis is domained by the running total, so no month reads as a loss', async () => {
    // The same claim in the units a reader actually sees. The cumulative
    // series never goes below zero here, so every tick is positive; the
    // non-accumulating series dips to -300 and the axis would say so.
    //
    // Awaited because the savings goal arrives from a promise: the card's body
    // (and with it the whole chart) only mounts once that resolves, so a
    // synchronous read finds no axis at all.
    const { container } = render(<SavingsTab />);
    await waitFor(() => {
      expect(
        savingsCard(container).querySelector('.recharts-yAxis-tick-labels'),
      ).not.toBeNull();
    });

    expect(axisTickLabels(savingsCard(container))).toEqual([
      '$0.00',
      '$300.00',
      '$600.00',
      '$900.00',
      '$1,200.00',
    ]);
  });

  test('the year-end figure is the whole run, losses included', async () => {
    // `currentSavings` is `savingsData.at(-1)`, so this is the last point of
    // the curve above read as text — and it is the number the goal ring and
    // the Remaining line are both computed from. 1200, not 500 (the final
    // month alone) and not 1500 (the run with the loss clamped away).
    const { container } = render(<SavingsTab />);
    await waitFor(() => {
      expect(container.textContent).toContain('Saved:');
    });

    expect(container.textContent).toContain('Saved: $1,200.00');
    expect(container.textContent).toContain('Remaining: $3,800.00');
  });
});
