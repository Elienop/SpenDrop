import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import React from 'react';

// Every other chart test in this repo mocks recharts wholesale
// (`Tooltip: () => <div />`), which makes the suite structurally blind to a
// recharts upgrade: the 2 -> 3 bump changed three library DEFAULTS with no
// compile error, and 1406 green tests said nothing about any of them.
//
// This file mocks ONLY the sizing. `ResponsiveContainer` measures its parent,
// which is 0x0 under happy-dom, and recharts bails on a non-positive size —
// so a fixed size is the single thing standing between us and real charts in
// tests. Everything else is the genuine library, which is the whole point:
// the behaviours pinned below live in recharts' own selectors, not in our
// wrapper, and a stubbed Legend or Tooltip would assert nothing.
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

import spendingTabSource from '../reports/SpendingTab.tsx?raw';
import savingsTabSource from '../reports/SavingsTab.tsx?raw';
import { LineChart, Line, BarChart, Bar, XAxis } from 'recharts';
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from './chart';

afterEach(cleanup);

describe('ChartLegendContent ordering', () => {
  // recharts 3 added `itemSorter` to Legend, defaulting to 'value' and applied
  // inside recharts' OWN selector, so it reorders even custom legend content.
  // recharts 2 never sorted. Observed live on the Expense Velocity chart,
  // which rendered "Current Month, Budget Pace, Previous Month" against a
  // declared current / previous / pace.
  //
  // The dataKeys below are deliberately NOT in lexicographic order relative to
  // their labels: sorted by dataKey they would come out zulu, alpha, mike.
  // A legend whose keys happen to sort into declaration order would pass this
  // test with the fix removed — which is exactly why two of the three real
  // legends could not have caught the regression.
  const CONFIG = {
    zulu: { label: 'First Declared', color: 'hsl(1 1% 1%)' },
    alpha: { label: 'Second Declared', color: 'hsl(2 2% 2%)' },
    mike: { label: 'Third Declared', color: 'hsl(3 3% 3%)' },
  } satisfies ChartConfig;

  const DATA = [
    { name: 'Jan', zulu: 3, alpha: 2, mike: 1 },
    { name: 'Feb', zulu: 4, alpha: 3, mike: 2 },
  ];

  function renderLegend() {
    return render(
      <ChartContainer config={CONFIG}>
        <LineChart data={DATA}>
          <XAxis dataKey="name" />
          <ChartLegend content={<ChartLegendContent />} itemSorter={null} />
          <Line dataKey="zulu" stroke="var(--color-zulu)" />
          <Line dataKey="alpha" stroke="var(--color-alpha)" />
          <Line dataKey="mike" stroke="var(--color-mike)" />
        </LineChart>
      </ChartContainer>,
    );
  }

  test('renders legend items in DECLARATION order, not sorted by dataKey', () => {
    const { container } = renderLegend();

    const wrapper = container.querySelector('.recharts-legend-wrapper');
    expect(wrapper).not.toBeNull();

    const labels = Array.from(
      wrapper!.querySelectorAll(':scope > div > div'),
    ).map((el) => el.textContent?.trim());

    // Declaration order. Sorted by dataKey this would be
    // ['Second Declared', 'Third Declared', 'First Declared'] (alpha/mike/zulu)
    // — the mutant this test exists to kill.
    expect(labels).toEqual([
      'First Declared',
      'Second Declared',
      'Third Declared',
    ]);
  });

  test('positive control: the legend actually rendered all three series', () => {
    // Without this, the assertion above would pass just as happily against an
    // empty legend — `[] !== [three labels]` would fail, but a partially
    // rendered legend or a silently dropped series would not be distinguished
    // from an ordering bug. This pins that the fixture is live.
    renderLegend();
    expect(screen.getByText('First Declared')).toBeInTheDocument();
    expect(screen.getByText('Second Declared')).toBeInTheDocument();
    expect(screen.getByText('Third Declared')).toBeInTheDocument();
  });
});

describe('ChartTooltipContent money rendering', () => {
  // Our chart.tsx is a FORK of shadcn's, differing from upstream in two ways
  // that matter for a currency app. Neither is covered anywhere else, and a
  // wholesale-mocked test cannot see either:
  //
  //   1. `{item.value != null && ...}` where upstream has a truthy `{item.value && ...}`
  //      — a row whose value is 0 must still RENDER. Upstream's version deletes it.
  //   2. numeric values forced to exactly two fraction digits — amounts carry cents.
  //
  // A future `shadcn add chart --overwrite`, or a careless merge of upstream,
  // silently reintroduces both. These tests are what make that loud.
  const CONFIG = {
    income: { label: 'Income', color: 'hsl(1 1% 1%)' },
    expense: { label: 'Expense', color: 'hsl(2 2% 2%)' },
  } satisfies ChartConfig;

  const DATA = [{ name: 'Jan', income: 0, expense: 1234.5 }];

  function renderTooltip() {
    return render(
      <ChartContainer config={CONFIG}>
        <BarChart data={DATA}>
          <XAxis dataKey="name" />
          <ChartTooltip defaultIndex={0} content={<ChartTooltipContent />} />
          <Bar dataKey="income" fill="var(--color-income)" />
          <Bar dataKey="expense" fill="var(--color-expense)" />
        </BarChart>
      </ChartContainer>,
    );
  }

  test('renders a row whose value is 0 instead of dropping it', () => {
    renderTooltip();
    // The label proves the row exists at all; the value proves it was not
    // suppressed by a truthiness test.
    expect(screen.getByText('Income')).toBeInTheDocument();
    expect(screen.getByText('0.00')).toBeInTheDocument();
  });

  test('formats numeric values with exactly two fraction digits', () => {
    renderTooltip();
    // 1234.5 must render as 1,234.50 — not 1,234.5 and not 1234.5.
    expect(screen.getByText('1,234.50')).toBeInTheDocument();
    expect(screen.queryByText('1,234.5')).toBeNull();
  });
});

describe('every production <ChartLegend> opts out of recharts 3 sorting', () => {
  // WIRING SEAM. The ordering test above proves `itemSorter={null}` WORKS; it
  // cannot prove the real charts pass it. Verified by mutation: deleting
  // `itemSorter={null}` from both SpendingTab call sites left the entire suite
  // green — 93 files, 1410 tests — while the Expense Velocity legend silently
  // reordered in the browser. This is the seventh instance of that shape in
  // this repo, so it gets a source pin, the same mechanism `useIsMobileViewport`
  // uses against MobileNav.
  //
  // A legend whose dataKeys happen to sort into declaration order shows no
  // symptom, so "it looks right" is not a check. If a future chart genuinely
  // WANTS recharts' sorting, change this pin deliberately rather than deleting
  // it — that is the whole point of it failing loudly.
  const SOURCES: ReadonlyArray<readonly [string, string]> = [
    ['SpendingTab.tsx', spendingTabSource],
    ['SavingsTab.tsx', savingsTabSource],
  ];

  // `<ChartLegend>` always wraps a self-closing `<ChartLegendContent />`, and a
  // non-greedy match would stop at THAT `/>` rather than the outer one — which
  // is exactly how the first version of this pin failed against correct code.
  // Neutralise the inner element first, then the outer match is unambiguous.
  const outerLegends = (source: string): string[] =>
    source
      .replace(/<ChartLegendContent[^>]*\/>/g, 'CONTENT')
      .match(/<ChartLegend[\s\S]*?\/>/g) ?? [];

  test.each(SOURCES)('%s passes itemSorter on every ChartLegend', (_name, source) => {
    const legends = outerLegends(source);

    // Positive control: if the regex ever stops matching — a rename, a
    // reformat, a move to another file — this is what catches it, rather than
    // the test vacuously passing over an empty list.
    expect(legends.length).toBeGreaterThan(0);

    for (const legend of legends) {
      expect(legend).toContain('itemSorter={null}');
    }
  });

  test('the pinned call sites are the only ones in the app', () => {
    // Guards the other half: a NEW <ChartLegend> added to some third file
    // would not be covered by the pin above. Counting here means the new file
    // has to be added to SOURCES deliberately.
    const total = SOURCES.reduce((n, [, src]) => n + outerLegends(src).length, 0);
    expect(total).toBe(3);
  });
});
