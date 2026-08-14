import { describe, expect, test } from 'vitest';
import {
  OPACITY_STOPS,
  WEEKDAY_COUNT,
  buildIntensityScale,
  chipFill,
  generateMonthDates,
  generateYearDates,
  heatmapCellLabel,
  heaviestDay,
  isUpcoming,
  monthLabelColumns,
  spendingSummaryPhrase,
  toWeekRows,
  toWeekdayRows,
} from './heatmapGrid';

const usd = (amount: number) =>
  amount.toLocaleString('en-US', { style: 'currency', currency: 'USD' });

/** Monday = 0, derived independently of the code under test. */
function mondayIndex(iso: string): number {
  const [y, m, d] = iso.split('-').map(Number);
  return (new Date(Date.UTC(y, m - 1, d)).getUTCDay() + 6) % 7;
}

describe('the year grid derives its column count from the data', () => {
  // The seven 54-column years are the ENTIRE value of this table: the old
  // hardcoded `repeat(53, 1fr)` passes every 53 row. They are the leap years
  // beginning on a Sunday, and all seven fall inside the [1900, 2100] window
  // `MinDataYear` / `MaxDataYear` let the picker offer — 2012 is importable
  // today and 1984 is the year `yearPicker.test.tsx` already drives the
  // component with, entirely unaware of this.
  test.each([
    [1900, 53],
    [2026, 53],
    [2100, 53],
    [1928, 54],
    [1956, 54],
    [1984, 54],
    [2012, 54],
    [2040, 54],
    [2068, 54],
    [2096, 54],
  ])('%i needs %i columns', (year, columns) => {
    const cells = generateYearDates(year);
    expect(cells.length).toBe(columns * WEEKDAY_COUNT);
    expect(toWeekdayRows(cells)).toHaveLength(WEEKDAY_COUNT);
    for (const row of toWeekdayRows(cells)) {
      expect(row).toHaveLength(columns);
    }
  });

  test('every cell lands on the weekday its row claims', () => {
    // The invariant `gridAutoFlow: 'column'` used to carry implicitly, and
    // whose loss produced no error and no test failure. Now it is in the data,
    // so this can check it directly.
    for (const year of [1984, 2026]) {
      const rows = toWeekdayRows(generateYearDates(year));
      rows.forEach((row, r) => {
        for (const date of row) {
          if (date === null) continue;
          expect(mondayIndex(date)).toBe(r);
        }
      });
    }
  });

  test('padding cells stay in the array, only at the two ends', () => {
    const cells = generateYearDates(2026);
    const real = cells.filter((c) => c !== null);
    expect(real).toHaveLength(365);
    expect(real[0]).toBe('2026-01-01');
    expect(real[real.length - 1]).toBe('2026-12-31');
    const firstReal = cells.findIndex((c) => c !== null);
    const lastReal = cells.length - 1 - [...cells].reverse().findIndex((c) => c !== null);
    expect(cells.slice(firstReal, lastReal + 1).every((c) => c !== null)).toBe(true);
  });
});

describe('the month grid', () => {
  test.each([
    [2026, 0, 31, '2026-01-01', '2026-01-31'],
    [2026, 1, 28, '2026-02-01', '2026-02-28'],
    [2024, 1, 29, '2024-02-01', '2024-02-29'],
    [2026, 11, 31, '2026-12-01', '2026-12-31'],
  ])('%i-%i holds %i days', (year, month, days, first, last) => {
    const cells = generateMonthDates(year, month);
    expect(cells.length % WEEKDAY_COUNT).toBe(0);
    const real = cells.filter((c): c is string => c !== null);
    expect(real).toHaveLength(days);
    expect(real[0]).toBe(first);
    expect(real[real.length - 1]).toBe(last);
  });

  test('rows are weeks and columns are weekdays', () => {
    const rows = toWeekRows(generateMonthDates(2026, 2));
    for (const row of rows) {
      expect(row).toHaveLength(WEEKDAY_COUNT);
      row.forEach((date, c) => {
        if (date === null) return;
        expect(mondayIndex(date)).toBe(c);
      });
    }
  });

  test('leading padding matches the first weekday of the month', () => {
    // March 2026 starts on a Sunday, so six pads precede it. A month grid that
    // dropped them would put the 1st under Monday.
    const cells = generateMonthDates(2026, 2);
    expect(cells.slice(0, 6).every((c) => c === null)).toBe(true);
    expect(cells[6]).toBe('2026-03-01');
  });
});

describe('month labels', () => {
  test('all twelve land, in distinct columns, in a 54-column year', () => {
    const cells = generateYearDates(1984);
    const labels = monthLabelColumns(1984, cells);
    expect(labels).toHaveLength(54);
    const placed = labels.filter((l): l is string => l !== null);
    expect(placed).toEqual([
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ]);
  });

  test('a label sits on the column holding that month’s first day', () => {
    const cells = generateYearDates(2026);
    const labels = monthLabelColumns(2026, cells);
    const marchColumn = Math.floor(cells.indexOf('2026-03-01') / WEEKDAY_COUNT);
    expect(labels[marchColumn]).toBe('Mar');
    expect(labels.filter((l) => l === 'Mar')).toHaveLength(1);
  });
});

describe('chipFill', () => {
  test('renders a stop as a token wash, not an element opacity', () => {
    expect(chipFill(0.5)).toBe(
      'color-mix(in oklab, hsl(var(--primary)) 50%, transparent)',
    );
  });

  test('every shipped stop comes out as a whole percentage', () => {
    expect(OPACITY_STOPS.map(chipFill)).toEqual([
      'color-mix(in oklab, hsl(var(--primary)) 30%, transparent)',
      'color-mix(in oklab, hsl(var(--primary)) 50%, transparent)',
      'color-mix(in oklab, hsl(var(--primary)) 75%, transparent)',
      'color-mix(in oklab, hsl(var(--primary)) 100%, transparent)',
    ]);
  });

  test('roundsTheStop: a stop that IS a float artefact still comes out clean', () => {
    // The four stops above happen not to need this — `0.3 * 100` is exactly
    // 30 — so the rounding inside `chipFill` is invisible through the rendered
    // component and a mutant that removes it survives every test that goes via
    // the DOM. These two values are the ones that would ship
    // `28.999999999999996%` and `7.000000000000001%` into a style attribute,
    // and they are the whole reason the guard is there. Changing OPACITY_STOPS
    // to such a value must not be the moment anyone finds out.
    expect(chipFill(0.29)).toBe(
      'color-mix(in oklab, hsl(var(--primary)) 29%, transparent)',
    );
    expect(chipFill(0.07)).toBe(
      'color-mix(in oklab, hsl(var(--primary)) 7%, transparent)',
    );
    // The naive form really does produce those, so the case is not imaginary.
    expect(0.29 * 100).not.toBe(29);
    expect(0.07 * 100).not.toBe(7);
  });
});

describe('the intensity scale', () => {
  // B27, closed. These four cases replace `outOfPopulation`, which pinned the
  // OPPOSITE behaviour: a total the scale had never seen came back DARKEST,
  // via a `rank.get(total) ?? n` fallback. That was reachable by the commonest
  // input there is — a day with no rows, which `cellProps` looks up as 0 — and
  // showed nothing only because every consumer gated on `total > 0` before it
  // read the opacity. Moving those gates to has-rows, which is what a refund
  // day requires, would have painted refunded and empty days black.
  test('a day that spent nothing NET gets the palest stop, never the darkest', () => {
    const scale = buildIntensityScale([10, 20, 30, 40]);
    expect(scale(0)).toBe(OPACITY_STOPS[0]);
    expect(scale(-50)).toBe(OPACITY_STOPS[0]);
    // The magnitude of a refund does not make it intense: a day that gave
    // back more than the heaviest day spent is still the pale end.
    expect(scale(-9999)).toBe(OPACITY_STOPS[0]);
    // Control: a real spending day still ranks normally, so the assertions
    // above are about the <= 0 decision and not about a broken scale.
    expect(scale(10)).toBe(OPACITY_STOPS[0]);
    expect(scale(40)).toBe(OPACITY_STOPS[OPACITY_STOPS.length - 1]);
  });

  test('a spending total outside the population lands between its neighbours', () => {
    // The other half of removing the fallback: the scale is TOTAL now, so an
    // unseen value is ranked by counting rather than by a lookup miss.
    const scale = buildIntensityScale([10, 20, 30, 40]);
    expect(scale(25)).toBe(OPACITY_STOPS[1]); // two days spent <= 25
    expect(scale(999)).toBe(OPACITY_STOPS[OPACITY_STOPS.length - 1]);
    expect(scale(1)).toBe(OPACITY_STOPS[0]);
  });

  test('refund days do not take up ranks and dim the real spending days', () => {
    // The population is the SPENDING days. With the four refund days counted
    // in, 40 would rank 4th of 8 — the second stop — and the heaviest day of
    // the year would paint mid-grey.
    const withRefunds = buildIntensityScale([-5, -6, -7, 0, 10, 20, 30, 40]);
    const without = buildIntensityScale([10, 20, 30, 40]);
    for (const total of [10, 20, 30, 40]) {
      expect(withRefunds(total)).toBe(without(total));
    }
    expect(withRefunds(40)).toBe(OPACITY_STOPS[OPACITY_STOPS.length - 1]);
  });

  test('a year of nothing but refunds paints nothing dark', () => {
    const scale = buildIntensityScale([-5, -10, 0]);
    expect([-5, -10, 0].map(scale)).toEqual([
      OPACITY_STOPS[0],
      OPACITY_STOPS[0],
      OPACITY_STOPS[0],
    ]);
  });

  // The defect the old percentile arithmetic had: `pct(0.75)` indexed the LAST
  // element for n <= 4, and the top stop was only returned for `total > p75`
  // strictly — so the biggest spend in a sparse set painted at the PALEST
  // stop. A fixture with five or more distinct days passes either way, which
  // is why every case here is sparse.
  test.each([[1], [2], [3], [4]])(
    'with %i spending day(s), the largest is not the palest',
    (n) => {
      const totals = Array.from({ length: n }, (_, i) => (i + 1) * 10);
      const scale = buildIntensityScale(totals);
      const max = totals[totals.length - 1];
      expect(scale(max)).not.toBe(OPACITY_STOPS[0]);
      expect(scale(max)).toBe(OPACITY_STOPS[OPACITY_STOPS.length - 1]);
    },
  );

  test('four distinct days use all four stops, in order', () => {
    const scale = buildIntensityScale([10, 20, 30, 40]);
    expect([10, 20, 30, 40].map(scale)).toEqual([...OPACITY_STOPS]);
  });

  test('a realistic year splits into quartiles', () => {
    const totals = Array.from({ length: 200 }, (_, i) => i + 1);
    const scale = buildIntensityScale(totals);
    expect(scale(1)).toBe(OPACITY_STOPS[0]);
    expect(scale(50)).toBe(OPACITY_STOPS[0]);
    expect(scale(51)).toBe(OPACITY_STOPS[1]);
    expect(scale(150)).toBe(OPACITY_STOPS[2]);
    expect(scale(151)).toBe(OPACITY_STOPS[3]);
    expect(scale(200)).toBe(OPACITY_STOPS[3]);
  });

  test('ties share a rank rather than inventing a gradient', () => {
    const scale = buildIntensityScale([50, 50, 50, 50]);
    expect([50, 50].map(scale)).toEqual([
      OPACITY_STOPS[OPACITY_STOPS.length - 1],
      OPACITY_STOPS[OPACITY_STOPS.length - 1],
    ]);
  });

  test('an empty year does not divide by zero', () => {
    // The n === 0 arm is only reachable for a POSITIVE total now — a zero
    // takes the palest branch above it — so it is probed with one.
    expect(buildIntensityScale([])(10)).toBe(
      OPACITY_STOPS[OPACITY_STOPS.length - 1],
    );
    expect(buildIntensityScale([])(0)).toBe(OPACITY_STOPS[0]);
  });
});

describe('the cell label', () => {
  // Exact strings, never a regex fragment: /March 4/ matches the wrong year,
  // a padding-cell regression and a half-built label alike.
  test('a day with spending names the weekday, the date and the amount', () => {
    expect(heatmapCellLabel('2025-03-04', 42.5, 1, usd, '2025-06-01')).toBe(
      'Tuesday, March 4th, 2025: $42.50 spent',
    );
  });

  test('a day with no rows says so rather than reporting a zero total', () => {
    expect(heatmapCellLabel('2025-03-04', 0, 0, usd, '2025-06-01')).toBe(
      'Tuesday, March 4th, 2025: no spending',
    );
  });

  // The pair the whole has-rows rework exists for. Both days total zero; one
  // of them holds four transactions, and telling the household "no spending"
  // about it — while the sheet behind the cell refuses to fetch those rows —
  // is the surface denying its own data.
  test('a day REFUNDED to nothing reports its rows, not "no spending"', () => {
    expect(heatmapCellLabel('2025-03-04', 0, 4, usd, '2025-06-01')).toBe(
      'Tuesday, March 4th, 2025: $0.00 net across 4 transactions',
    );
  });

  test('a day whose refunds outweigh its spending reports the net and the rows', () => {
    expect(heatmapCellLabel('2025-03-04', -20, 2, usd, '2025-06-01')).toBe(
      'Tuesday, March 4th, 2025: -$20.00 net across 2 transactions',
    );
  });

  test('one row is a transaction, not "1 transactions"', () => {
    expect(heatmapCellLabel('2025-03-04', -20, 1, usd, '2025-06-01')).toBe(
      'Tuesday, March 4th, 2025: -$20.00 net across 1 transaction',
    );
  });

  test('today is announced as today', () => {
    expect(heatmapCellLabel('2025-03-04', 42.5, 1, usd, '2025-03-04')).toBe(
      'Today, Tuesday, March 4th, 2025: $42.50 spent',
    );
  });

  test('the date is read in local time, not shifted by UTC parsing', () => {
    // `new Date('2026-01-01')` is UTC midnight, which renders as December 31st
    // for anyone west of GMT. Pinned because the ISO strings come out of a
    // deliberately UTC-built grid and the label is the one place they get
    // parsed back.
    expect(heatmapCellLabel('2026-01-01', 0, 0, usd, '2026-06-01')).toBe(
      'Thursday, January 1st, 2026: no spending',
    );
  });

  test('a date that has not happened yet is "upcoming", not "no spending"', () => {
    // A future day has no expense rows for exactly the same reason a quiet day
    // has none, so it arrives with no rows — and "no spending" would then be a
    // claim about the future rather than a report about the past. The phone
    // opens on the CURRENT month, so this is most of the default view.
    expect(heatmapCellLabel('2026-08-31', 0, 0, usd, '2026-08-09')).toBe(
      'Monday, August 31st, 2026: upcoming',
    );
  });

  test('recorded spending beats "upcoming" on a future-dated row', () => {
    // The ledger accepts any date up to MaxDataYear, so a household CAN book a
    // future expense. Blanking a day someone deliberately recorded would be
    // hiding their own data.
    expect(heatmapCellLabel('2026-08-31', 42.5, 1, usd, '2026-08-09')).toBe(
      'Monday, August 31st, 2026: $42.50 spent',
    );
    expect(isUpcoming('2026-08-31', '2026-08-09', true)).toBe(false);
    expect(isUpcoming('2026-08-31', '2026-08-09', false)).toBe(true);
    expect(isUpcoming('2026-08-09', '2026-08-09', false)).toBe(false);
  });

  test('a future day booked and then refunded is NOT blanked as upcoming', () => {
    // Keyed on the total, this day netted zero and was blanked — the exact
    // hole the has-rows flag closes. It is reachable: a future-dated expense
    // and its refund are both legal entries.
    expect(isUpcoming('2026-08-31', '2026-08-09', true)).toBe(false);
    expect(heatmapCellLabel('2026-08-31', 0, 2, usd, '2026-08-09')).toBe(
      'Monday, August 31st, 2026: $0.00 net across 2 transactions',
    );
  });
});

describe('the shared spending phrase', () => {
  // Exported and shared because THREE surfaces say this — the day cell, the
  // month picker and the day sheet's header. The defect it prevents is a
  // header reading "No spending" over a list of four transactions.
  test.each([
    ['no rows at all', 0, 0, 'no spending'],
    ['no rows, whatever the total claims', 12, 0, 'no spending'],
    ['a spending day', 42.5, 3, '$42.50 spent'],
    ['a refunded day', 0, 2, '$0.00 net across 2 transactions'],
    ['a net-negative day', -20, 2, '-$20.00 net across 2 transactions'],
  ] as const)('%s', (_name, total, count, expected) => {
    expect(spendingSummaryPhrase(total, count, usd)).toBe(expected);
  });
});

describe('the heaviest day', () => {
  const cells = generateMonthDates(2026, 2); // March 2026

  test('names the largest spending day among the cells shown', () => {
    const lookup = new Map([
      ['2026-03-02', 10],
      ['2026-03-17', 4200],
      ['2026-03-20', 250],
    ]);
    expect(heaviestDay(cells, lookup)).toEqual({
      date: '2026-03-17',
      total: 4200,
      tiedWith: 0,
    });
  });

  test('ignores days the grid is not showing', () => {
    // The lookup is year-wide; the cells are one month. A July maximum must
    // not surface under a March grid.
    const lookup = new Map([
      ['2026-03-17', 100],
      ['2026-07-04', 9999],
    ]);
    expect(heaviestDay(cells, lookup)?.date).toBe('2026-03-17');
  });

  test('on a tie the EARLIEST day wins, and the tie is counted', () => {
    // Not "whichever the sort happened to put first": `dates` is chronological
    // and the first strict maximum is kept, so this is deterministic — and the
    // count is what stops one arbitrary winner being presented as unique.
    const lookup = new Map([
      ['2026-03-05', 500],
      ['2026-03-11', 500],
      ['2026-03-22', 500],
      ['2026-03-25', 12],
    ]);
    expect(heaviestDay(cells, lookup)).toEqual({
      date: '2026-03-05',
      total: 500,
      tiedWith: 2,
    });
  });

  test('a month with no spending at all has no heaviest day', () => {
    expect(heaviestDay(cells, new Map())).toBeNull();
    // A zero-valued entry is not a spending day either — otherwise the caller
    // renders "Heaviest day: … $0.00".
    expect(heaviestDay(cells, new Map([['2026-03-04', 0]]))).toBeNull();
  });

  // Signed nets, deliberately: "heaviest" means the day the household SPENT
  // the most, so the day that gave the most back is not a candidate and a
  // month of nothing but refunds names nobody.
  test('a refund day never wins, however large the refund', () => {
    const lookup = new Map([
      ['2026-03-05', -9999],
      ['2026-03-11', 12],
    ]);
    expect(heaviestDay(cells, lookup)).toEqual({
      date: '2026-03-11',
      total: 12,
      tiedWith: 0,
    });
  });

  test('a month of nothing but refunds has no heaviest day', () => {
    const lookup = new Map([
      ['2026-03-05', -50],
      ['2026-03-11', -10],
      ['2026-03-22', 0],
    ]);
    expect(heaviestDay(cells, lookup)).toBeNull();
  });

  test('a refund day does not break the tie count of the days that did spend', () => {
    // The negative sits BETWEEN two tied maxima in date order, so a `tiedWith`
    // that reset on any non-winning value would come back 0.
    const lookup = new Map([
      ['2026-03-05', 500],
      ['2026-03-11', -300],
      ['2026-03-22', 500],
    ]);
    expect(heaviestDay(cells, lookup)).toEqual({
      date: '2026-03-05',
      total: 500,
      tiedWith: 1,
    });
  });

  test('padding cells are skipped, not treated as days', () => {
    // March 2026 opens on a Sunday, so the array starts with six nulls.
    expect(cells.slice(0, 6).every((c) => c === null)).toBe(true);
    expect(heaviestDay(cells, new Map([['2026-03-01', 7]]))).toEqual({
      date: '2026-03-01',
      total: 7,
      tiedWith: 0,
    });
  });
});
