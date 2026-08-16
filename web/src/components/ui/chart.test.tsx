import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
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

describe('ChartLegendContent swatch colour', () => {
  // A gradient-filled series reports `color: "url(#…)"` in the legend payload,
  // because recharts reports a Bar's `fill` verbatim. That is not a CSS colour,
  // so React drops the declaration and the swatch renders fully transparent —
  // browser-verified on Savings' Year-over-Year, where both swatches computed
  // to `rgba(0, 0, 0, 0)` beside three gradient shapes.
  //
  // Literal colours, not `hsl(var(--primary))`. Checked directly rather than
  // assumed: happy-dom keeps `var(--color-x)` VERBATIM in `style.backgroundColor`
  // and drops `url(#foo)` to `''`. So a var-based fixture would read back the
  // same `var(...)` string whether the swatch came from the config or from the
  // payload — the config path and the mutant would be indistinguishable. A
  // literal is what makes the two differ.
  const CONFIG = {
    gradientSeries: { label: 'Gradient Series', color: 'rgb(1, 2, 3)' },
    plainSeries: { label: 'Plain Series' },
    emptyColourSeries: { label: 'Empty Colour Series', color: '' },
  } satisfies ChartConfig

  const DATA = [
    { name: 'Jan', gradientSeries: 3, plainSeries: 2, emptyColourSeries: 1 },
    { name: 'Feb', gradientSeries: 4, plainSeries: 3, emptyColourSeries: 2 },
  ]

  /** Each legend chip's label and the inline colour of its swatch. */
  function swatches(container: HTMLElement): Record<string, string> {
    const wrapper = container.querySelector('.recharts-legend-wrapper')
    if (!wrapper) throw new Error('no .recharts-legend-wrapper rendered')
    const out: Record<string, string> = {}
    for (const chip of wrapper.querySelectorAll<HTMLElement>(
      ':scope > div > div'
    )) {
      const swatch = chip.querySelector<HTMLElement>('div')
      out[chip.textContent?.trim() ?? ''] = swatch?.style.backgroundColor ?? ''
    }
    return out
  }

  function renderLegend() {
    return render(
      <ChartContainer config={CONFIG}>
        <BarChart data={DATA}>
          <defs>
            <linearGradient id="swatch-test-gradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="rgb(1, 2, 3)" stopOpacity={0.8} />
              <stop offset="95%" stopColor="rgb(1, 2, 3)" stopOpacity={0.1} />
            </linearGradient>
          </defs>
          <XAxis dataKey="name" />
          <ChartLegend content={<ChartLegendContent />} itemSorter={null} />
          <Bar dataKey="gradientSeries" fill="url(#swatch-test-gradient)" />
          <Bar dataKey="plainSeries" fill="rgb(9, 8, 7)" />
          <Bar dataKey="emptyColourSeries" fill="rgb(4, 5, 6)" />
        </BarChart>
      </ChartContainer>
    )
  }

  test('a gradient-filled series takes its swatch from the config', () => {
    const { container } = renderLegend()
    // Asserted POSITIVELY and exactly. `not.toBe('url(#…)')` would pass on the
    // mutant too: happy-dom rejects the invalid value and the style reads back
    // as the empty string either way.
    expect(swatches(container)['Gradient Series']).toBe('rgb(1, 2, 3)')
  })

  test('positive control: a series with no config colour still uses its own', () => {
    // Guards the other direction. Resolving from the config must not become
    // "only ever the config" — a series that declares no colour has to keep
    // falling through to the payload exactly as it did before.
    const { container } = renderLegend()
    expect(swatches(container)['Plain Series']).toBe('rgb(9, 8, 7)')
  })

  test('an empty-string config colour counts as absent, not as a colour', () => {
    // This is what makes `||` load-bearing rather than a style choice: under
    // `??` the empty string wins and the swatch goes transparent again. It
    // matches how ChartStyle filters (`config.theme || config.color`), so the
    // two cannot disagree about whether a series has a colour.
    const { container } = renderLegend()
    expect(swatches(container)['Empty Colour Series']).toBe('rgb(4, 5, 6)')
  })
})

describe('ChartLegendContent and ChartTooltipContent React keys', () => {
  // `key={item.value}` keyed each chip by the series NAME, which two series may
  // legitimately share. The obvious test is vacuous — React renders both chips
  // regardless — so the only assertion that distinguishes the two is React's
  // duplicate-key warning, and a spy that never fires proves nothing without a
  // control that makes it fire.
  //
  // BOTH `key={index}` sites are covered here. They are one accepted decision
  // (SonarQube S6479, argued at both sites in chart.tsx) and they need one
  // guard each: a legend case cannot fail for a tooltip mutant, and the tooltip
  // half went unguarded until a mutation pass put `key="dup"` on its rows and
  // the whole file stayed green.
  const DUPLICATE_KEY_WARNING = /Encountered two children with the same key/

  // `item.value` is recharts' `name ?? dataKey`, NOT the config label — the
  // first version of this fixture gave the two series the same config LABEL and
  // survived the mutant, because their dataKeys still differed. An explicit
  // shared `name` is what makes `key={item.value}` actually collide, and it is
  // a legitimate thing for a chart to do: two series can share a display name
  // and be distinguished by the axis they sit on.
  const CONFIG = {
    first: { label: 'Same Name', color: 'rgb(1, 1, 1)' },
    second: { label: 'Same Name', color: 'rgb(2, 2, 2)' },
  } satisfies ChartConfig

  const SHARED_NAME = 'Shared Series Name'
  const DATA = [{ name: 'Jan', first: 1, second: 2 }]

  test('positive control: the spy DOES catch a duplicate key', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      render(
        <div>
          {['dup', 'dup'].map((k) => (
            <span key={k}>{k}</span>
          ))}
        </div>
      )
      expect(
        spy.mock.calls.some((args) =>
          args.some((a) => typeof a === 'string' && DUPLICATE_KEY_WARNING.test(a))
        )
      ).toBe(true)
    } finally {
      spy.mockRestore()
    }
  })

  test('two series sharing a name do not collide on a React key', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      render(
        <ChartContainer config={CONFIG}>
          <BarChart data={DATA}>
            <XAxis dataKey="name" />
            <ChartLegend content={<ChartLegendContent />} itemSorter={null} />
            <Bar dataKey="first" name={SHARED_NAME} fill="var(--color-first)" />
            <Bar dataKey="second" name={SHARED_NAME} fill="var(--color-second)" />
          </BarChart>
        </ChartContainer>
      )
      // Both chips render either way — that is why this is asserted on the
      // warning and not on the DOM.
      expect(screen.getAllByText('Same Name')).toHaveLength(2)
      expect(
        spy.mock.calls.filter((args) =>
          args.some((a) => typeof a === 'string' && DUPLICATE_KEY_WARNING.test(a))
        )
      ).toHaveLength(0)
    } finally {
      spy.mockRestore()
    }
  })

  test('two tooltip rows sharing a name do not collide on a React key', () => {
    // The tooltip keys by `nameKey || item.name || item.dataKey`, so a shared
    // `name` is what would collide there — and it is the same legitimate chart
    // as above, read through the other component. The rows show the raw name
    // here rather than a config label: the tooltip's lookup key is the NAME,
    // which is not a config key.
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      render(
        <ChartContainer config={CONFIG}>
          <BarChart data={DATA}>
            <XAxis dataKey="name" />
            <ChartTooltip defaultIndex={0} content={<ChartTooltipContent />} />
            <Bar dataKey="first" name={SHARED_NAME} fill="var(--color-first)" />
            <Bar dataKey="second" name={SHARED_NAME} fill="var(--color-second)" />
          </BarChart>
        </ChartContainer>
      )
      // Positive control: both rows are really on screen, so the warning count
      // below is measured over a tooltip that actually rendered two children.
      expect(screen.getAllByText(SHARED_NAME)).toHaveLength(2)
      expect(
        spy.mock.calls.filter((args) =>
          args.some((a) => typeof a === 'string' && DUPLICATE_KEY_WARNING.test(a))
        )
      ).toHaveLength(0)
    } finally {
      spy.mockRestore()
    }
  })

  // The two cases above share a NAME and differ in `dataKey`, which is the
  // collision `key={item.value}` produced. The keys now come from `withRowKeys`,
  // whose contract is one step stronger: two rows that agree on BOTH halves of
  // their identity still may not share a key — that is what its repeat counter
  // is for, and it is the part a "just concatenate the fields" rewrite would
  // quietly drop.
  //
  // Driven through the content components' own `payload` prop rather than a
  // chart, because recharts will not build two payload rows for one series;
  // `payload` is a declared prop of both, and the ordering/identity tests in
  // this file already render `ChartLegendContent` that way.
  const TWIN_CONFIG = {
    first: { label: 'Twin', color: 'rgb(1, 1, 1)' },
  } satisfies ChartConfig

  // Two fixtures, because the two components read a row's display name from
  // different fields: the legend from `value` (recharts' `name ?? dataKey`),
  // the tooltip from `name`. A single shared row would leave one of them
  // matching on an absent field, and the test would pass without the identity
  // halves ever being equal.
  const TWIN_LEGEND_ROW = {
    value: SHARED_NAME,
    dataKey: 'first',
    color: 'rgb(1, 1, 1)',
  }

  const TWIN_TOOLTIP_ROW = {
    name: SHARED_NAME,
    dataKey: 'first',
    // Required by recharts 3's tooltip `Payload`. Identical on both twins on
    // purpose: it is the series' generated handle, and these two rows stand for
    // the SAME series, so it must not be what pulls them apart.
    graphicalItemId: 'first',
    // A value, so the row wrapper carries text the name span does not and the
    // count below is over the two name spans rather than over their ancestors
    // as well.
    value: 1,
    color: 'rgb(1, 1, 1)',
    payload: {},
  }

  test('two legend chips identical in dataKey AND name do not collide', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      render(
        <ChartContainer config={TWIN_CONFIG}>
          <ChartLegendContent payload={[TWIN_LEGEND_ROW, TWIN_LEGEND_ROW]} />
        </ChartContainer>
      )
      // Positive control: two indistinguishable chips really are on screen, so
      // the warning count below is measured over a list that could collide.
      expect(screen.getAllByText('Twin')).toHaveLength(2)
      expect(
        spy.mock.calls.filter((args) =>
          args.some((a) => typeof a === 'string' && DUPLICATE_KEY_WARNING.test(a))
        )
      ).toHaveLength(0)
    } finally {
      spy.mockRestore()
    }
  })

  test('two tooltip rows identical in dataKey AND name do not collide', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      render(
        <ChartContainer config={TWIN_CONFIG}>
          <ChartTooltipContent
            active
            hideLabel
            payload={[TWIN_TOOLTIP_ROW, TWIN_TOOLTIP_ROW]}
          />
        </ChartContainer>
      )
      // The rows show the raw name, not the config label: the tooltip's config
      // lookup key is `name`, which is not a key of `TWIN_CONFIG` — the same
      // asymmetry the shared-name tooltip test above notes.
      expect(screen.getAllByText(SHARED_NAME)).toHaveLength(2)
      expect(
        spy.mock.calls.filter((args) =>
          args.some((a) => typeof a === 'string' && DUPLICATE_KEY_WARNING.test(a))
        )
      ).toHaveLength(0)
    } finally {
      spy.mockRestore()
    }
  })

  // Everything above guards against a COLLIDING key. Nothing above guards
  // against going back to `key={index}`, which collides with nothing and was
  // what both sites carried until this change — the duplicate-key spy stays
  // silent for it. What an index actually costs is only visible on a REORDER:
  // keyed by position, React keeps each DOM node where it is and rewrites its
  // text, so the node that showed the first series ends up showing the second.
  // Keyed by identity it moves the node instead. Node identity is therefore
  // the observable, the same way the Calendar renderer tests distinguish an
  // update from a remount.
  //
  // Reordering is not hypothetical here: recharts 3 sorts legend items in its
  // own selector (`itemSorter`, see the ordering tests at the top of this
  // file), so this payload arrives already sorted and re-sorts when a value
  // changes.
  const ORDER_CONFIG = {
    alpha: { label: 'Alpha', color: 'rgb(1, 1, 1)' },
    beta: { label: 'Beta', color: 'rgb(2, 2, 2)' },
  } satisfies ChartConfig

  const ALPHA_LEGEND = { value: 'Alpha', dataKey: 'alpha', color: 'rgb(1, 1, 1)' }
  const BETA_LEGEND = { value: 'Beta', dataKey: 'beta', color: 'rgb(2, 2, 2)' }

  test('a reordered legend payload moves a chip instead of rewriting it', () => {
    const legend = (payload: typeof ALPHA_LEGEND[]) => (
      <ChartContainer config={ORDER_CONFIG}>
        <ChartLegendContent payload={payload} />
      </ChartContainer>
    )

    const { rerender } = render(legend([ALPHA_LEGEND, BETA_LEGEND]))
    const alphaBefore = screen.getByText('Alpha')
    // Positive control: both chips are really on screen, so the comparison
    // below is between two rendered nodes.
    expect(screen.getByText('Beta')).toBeInTheDocument()

    rerender(legend([BETA_LEGEND, ALPHA_LEGEND]))

    // The order really did change...
    expect(screen.getByText('Beta').compareDocumentPosition(alphaBefore)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING
    )
    // ...and Alpha is still the SAME element, moved rather than rebuilt.
    expect(screen.getByText('Alpha')).toBe(alphaBefore)
  })

  const ALPHA_TIP = {
    name: 'Alpha',
    dataKey: 'alpha',
    graphicalItemId: 'alpha',
    value: 1,
    color: 'rgb(1, 1, 1)',
    payload: {},
  }
  const BETA_TIP = {
    name: 'Beta',
    dataKey: 'beta',
    graphicalItemId: 'beta',
    value: 2,
    color: 'rgb(2, 2, 2)',
    payload: {},
  }

  test('a reordered tooltip payload moves a row instead of rewriting it', () => {
    const tip = (payload: typeof ALPHA_TIP[]) => (
      <ChartContainer config={ORDER_CONFIG}>
        <ChartTooltipContent active hideLabel payload={payload} />
      </ChartContainer>
    )

    const { rerender } = render(tip([ALPHA_TIP, BETA_TIP]))
    const alphaBefore = screen.getByText('Alpha')
    expect(screen.getByText('Beta')).toBeInTheDocument()

    rerender(tip([BETA_TIP, ALPHA_TIP]))

    expect(screen.getByText('Beta').compareDocumentPosition(alphaBefore)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING
    )
    expect(screen.getByText('Alpha')).toBe(alphaBefore)
  })
})

describe('ChartLegendContent overflow', () => {
  // WIRING SEAM, and it says so. happy-dom performs no layout, so the defect
  // this pins — recharts gives the legend wrapper a FIXED pixel width, and a
  // `nowrap` row with `justify-center` pushed the excess out of BOTH edges, so
  // the first and last chips were clipped in half with ~10 series — is not
  // observable here at all. What IS observable is that the classes which fix it
  // are on the elements that need them.
  //
  // Browser-measured on Category Trends with 6 series at a 390px viewport: the
  // row wraps to 3 lines inside the 283px wrapper, no chip lands outside the
  // SVG, and recharts re-measures so the plot ends above the legend rather than
  // under it.
  const CONFIG = {
    alpha: { label: 'Alpha', color: 'rgb(1, 1, 1)' },
  } satisfies ChartConfig

  test('the legend row wraps and its chips do not break mid-label', () => {
    const { container } = render(
      <ChartContainer config={CONFIG}>
        <BarChart data={[{ name: 'Jan', alpha: 1 }]}>
          <XAxis dataKey="name" />
          <ChartLegend content={<ChartLegendContent />} itemSorter={null} />
          <Bar dataKey="alpha" fill="var(--color-alpha)" />
        </BarChart>
      </ChartContainer>
    )
    const row = container.querySelector('.recharts-legend-wrapper > div')
    if (!row) throw new Error('no legend row rendered')
    expect(row).toHaveClass('flex-wrap')

    const chips = row.querySelectorAll(':scope > div')
    expect(chips.length).toBeGreaterThan(0)
    for (const chip of chips) {
      // Without this a long label wraps INSIDE its own chip while the row
      // refuses to wrap — the shape the owner's screenshot showed.
      expect(chip).toHaveClass('whitespace-nowrap')
    }
  })

  test('a chip is shrinkable and bounded, so nowrap cannot re-create the overflow', () => {
    // `whitespace-nowrap` alone reintroduces the exact defect it was added to
    // help fix. A flex item's automatic minimum size is its min-content, and
    // nowrap raises that to the WHOLE string — so the chip becomes
    // unshrinkable, and recharts sizes the legend wrapper in fixed pixels.
    // Category names are user-supplied and the server caps them at 100
    // characters (`internal/api/limits.go`), so one long one is enough.
    //
    // The three classes are one mechanism and each is load-bearing: `min-w-0`
    // lifts the automatic minimum, `max-w-full` stops the chip exceeding the
    // wrapper, `overflow-hidden` contains what is left, and the inner
    // `truncate` makes the remainder an ellipsis rather than a cut glyph.
    // happy-dom lays nothing out, so this asserts the wiring; the geometry was
    // measured in Chrome with a 100-character category on Category Trends.
    const LONG = 'Household Groceries Pharmacy And Sundries '.padEnd(100, 'Z')
    expect(LONG).toHaveLength(100)

    const { container } = render(
      <ChartContainer config={{ alpha: { label: LONG, color: 'rgb(1, 1, 1)' } }}>
        <BarChart data={[{ name: 'Jan', alpha: 1 }]}>
          <XAxis dataKey="name" />
          <ChartLegend content={<ChartLegendContent />} itemSorter={null} />
          <Bar dataKey="alpha" fill="var(--color-alpha)" />
        </BarChart>
      </ChartContainer>
    )

    const chip = container.querySelector('.recharts-legend-wrapper > div > div')
    if (!chip) throw new Error('no legend chip rendered')
    expect(chip).toHaveClass('min-w-0', 'max-w-full', 'overflow-hidden')

    // The label sits in its own truncating span rather than as a bare text
    // node — `truncate` on the flex CONTAINER would fight `flex`, and a text
    // node cannot be ellipsised at all.
    const label = chip.querySelector('span')
    expect(label).not.toBeNull()
    expect(label).toHaveClass('truncate')
    // Positive control: the full name still reaches the DOM. Bounding it must
    // not shorten the string, only how much of it is painted.
    expect(label).toHaveTextContent(LONG)
  })
})

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
  // Globbed, not listed. The previous version pinned two named files and then
  // asserted "the pinned call sites are the only ones in the app" by counting
  // legends INSIDE those same two files — which is true of any list, so a
  // legend added to a third file (a new Reports tab, say) sailed past it. The
  // glob is the fix: a new file is covered the moment it exists, and nobody has
  // to remember to add it here.
  //
  // `.test.tsx` is excluded because this very file renders `<ChartLegend>` many
  // times, deliberately without `itemSorter` in places, to test the component
  // itself.
  const APP_SOURCES = import.meta.glob('/src/**/*.tsx', {
    query: '?raw',
    import: 'default',
    eager: true,
  });

  const SOURCES: ReadonlyArray<readonly [string, string]> = Object.entries(
    APP_SOURCES
  )
    .filter(([path]) => !path.endsWith('.test.tsx'))
    .sort(([a], [b]) => a.localeCompare(b));

  // `<ChartLegend>` always wraps a self-closing `<ChartLegendContent />`, and a
  // non-greedy match would stop at THAT `/>` rather than the outer one — which
  // is exactly how the first version of this pin failed against correct code.
  // Neutralise the inner element first, then the outer match is unambiguous.
  const outerLegends = (source: string): string[] =>
    source
      .replace(/<ChartLegendContent[^>]*\/>/g, 'CONTENT')
      .match(/<ChartLegend[\s\S]*?\/>/g) ?? [];

  const CALL_SITES: ReadonlyArray<readonly [string, string]> = SOURCES.flatMap(
    ([path, source]) =>
      outerLegends(source).map((legend) => [path, legend] as const)
  );

  test('every <ChartLegend> in the app passes itemSorter', () => {
    // Positive control, and the one thing the glob cannot prove on its own: it
    // really found the charts. If `outerLegends` stops matching — a rename, a
    // reformat — or the glob pattern goes stale, this fails here rather than
    // passing over an empty list.
    expect(CALL_SITES.length).toBeGreaterThan(0);

    for (const [path, legend] of CALL_SITES) {
      expect(legend, path).toContain('itemSorter={null}');
    }
  });

  test('the glob really reaches the production charts', () => {
    // Names the files that carry the legends today, so a glob that silently
    // stopped matching `src/components/reports/**` (a moved directory, a
    // changed pattern) cannot leave the test above vacuously green on a
    // shorter list. It does NOT pin the count: a new chart in a new file is
    // covered by the loop above automatically, which is the whole point of
    // globbing rather than listing.
    const files = new Set(CALL_SITES.map(([path]) => path.split('/').pop()));
    expect(files).toContain('SpendingTab.tsx');
    expect(files).toContain('SavingsTab.tsx');
  });
});

describe('ChartLegendContent placement', () => {
  // The 12px gap has to sit on the side of the legend that FACES THE PLOT, for
  // every way a caller can place it. recharts 3.10 deprecated `verticalAlign`
  // in favour of `position` but did not remove it — `legendDefaultProps` still
  // defaults it to "bottom" and `getDefaultPosition` still branches on it
  // whenever `position` is unset — so BOTH props move the legend today and both
  // arms have to be pinned. `position` wins when set, mirroring recharts' own
  // precedence.
  //
  // Every case below drives a real `<Legend>`, which is the other half of the
  // claim: `<Legend>` spreads its resolved props onto the `content` element, so
  // a prop it did NOT forward would leave the padding on the default arm and
  // the branch would be silently dead.
  const CONFIG = { alpha: { label: 'Alpha', color: 'rgb(1, 1, 1)' } } satisfies ChartConfig

  type Placement = Pick<
    React.ComponentProps<typeof ChartLegend>,
    'position' | 'verticalAlign'
  >

  function renderLegend(placement: Placement = {}) {
    return render(
      <ChartContainer config={CONFIG}>
        <BarChart data={[{ name: 'Jan', alpha: 1 }]}>
          <XAxis dataKey="name" />
          <ChartLegend
            content={<ChartLegendContent />}
            itemSorter={null}
            {...placement}
          />
          <Bar dataKey="alpha" fill="var(--color-alpha)" />
        </BarChart>
      </ChartContainer>
    )
  }

  const row = (container: HTMLElement) => {
    const el = container.querySelector('.recharts-legend-wrapper > div')
    if (!el) throw new Error('no legend row rendered')
    return el
  }

  test('an unplaced legend pads on the side facing the plot', () => {
    // What all three production legends render as.
    const { container } = renderLegend()
    expect(row(container)).toHaveClass('pt-3')
    expect(row(container)).not.toHaveClass('pb-3')
  })

  test('the deprecated verticalAlign="top" still flips the padding', () => {
    // recharts honours this prop, so we have to as well: reading `position`
    // alone left a top-aligned legend with its gap on the top side.
    const { container } = renderLegend({ verticalAlign: 'top' })
    expect(row(container)).toHaveClass('pb-3')
    expect(row(container)).not.toHaveClass('pt-3')
  })

  test('verticalAlign="middle" pads on the default side', () => {
    // The third arm of `VerticalAlignmentType`, and the one a `!== "bottom"`
    // test of the fallback would get wrong: a middle-aligned legend is beside
    // the plot, not above it, so the gap stays on top exactly as "bottom" does.
    const { container } = renderLegend({ verticalAlign: 'middle' })
    expect(row(container)).toHaveClass('pt-3')
    expect(row(container)).not.toHaveClass('pb-3')
  })

  test('position="top" flips the padding to the other side', () => {
    const { container } = renderLegend({ position: 'top' })
    expect(row(container)).toHaveClass('pb-3')
    expect(row(container)).not.toHaveClass('pt-3')
  })

  test('an insideTop position counts as top too', () => {
    // Same top edge, just inside the plot area rather than above it.
    const { container } = renderLegend({ position: 'insideTop' })
    expect(row(container)).toHaveClass('pb-3')
    expect(row(container)).not.toHaveClass('pt-3')
  })

  test('an { x, y } position is not top', () => {
    // The object arm of the placement union. It cannot be compared to a string,
    // and there is no edge it is anchored to, so it takes the default side —
    // this is what `typeof position === "string"` is for, and without it the
    // component throws on `includes` narrowing or silently mis-pads.
    const { container } = renderLegend({ position: { x: 0, y: 0 } })
    expect(row(container)).toHaveClass('pt-3')
    expect(row(container)).not.toHaveClass('pb-3')
  })

  test('a set position wins over verticalAlign', () => {
    // recharts resolves the conflict this way — `getDefaultPosition`, the only
    // reader of `verticalAlign`, is skipped entirely once `position` is set —
    // so the padding has to follow `position`, not the stale prop.
    const { container } = renderLegend({
      position: 'bottom',
      verticalAlign: 'top',
    })
    expect(row(container)).toHaveClass('pt-3')
    expect(row(container)).not.toHaveClass('pb-3')
  })
})

describe('ChartContainer config context', () => {
  // The provider value is memoised (SonarQube S6481). `config` is the only
  // thing any consumer reads off it, so it is the only dependency — and an
  // empty dependency list would freeze the first config forever while
  // `ChartStyle`, which reads the prop directly rather than the context,
  // carried on updating. That divergence is exactly what this pins: the legend
  // label comes from the CONTEXT, so a stale memo shows the old label.
  const FIRST = { alpha: { label: 'Before Rename', color: 'rgb(1, 1, 1)' } } satisfies ChartConfig
  const SECOND = { alpha: { label: 'After Rename', color: 'rgb(1, 1, 1)' } } satisfies ChartConfig

  const tree = (config: ChartConfig) => (
    <ChartContainer config={config}>
      <BarChart data={[{ name: 'Jan', alpha: 1 }]}>
        <XAxis dataKey="name" />
        <ChartLegend content={<ChartLegendContent />} itemSorter={null} />
        <Bar dataKey="alpha" fill="var(--color-alpha)" />
      </BarChart>
    </ChartContainer>
  )

  test('a changed config reaches consumers instead of being cached', () => {
    const { rerender } = render(tree(FIRST))
    // Positive control: the first config really is what rendered, so the
    // assertion below is a change and not just an absence.
    expect(screen.getByText('Before Rename')).toBeInTheDocument()

    rerender(tree(SECOND))
    expect(screen.getByText('After Rename')).toBeInTheDocument()
    expect(screen.queryByText('Before Rename')).toBeNull()
  })
})

describe('ChartContainer context identity', () => {
  // The memo above pins the provider's dependency LIST; this pins the memo
  // itself. Deleting it changes no markup — only how often every consumer under
  // the container re-renders — so the only way to see it is to count renders.
  //
  // The counter has to live inside a consumer, and `ChartLegendContent` is the
  // exported one. It renders `config[key].icon` as a fresh element on each of
  // its own renders, so an icon component counts exactly those. `React.memo`
  // around it is the wall: with no props of its own the probe cannot be
  // re-rendered from above, so a re-render can only have come from the chart
  // context handing out a new value.
  const PAYLOAD = [{ value: 'Alpha', dataKey: 'alpha', color: 'rgb(1, 1, 1)' }]

  function makeProbe() {
    const counter = { renders: 0 }

    function CountingIcon() {
      counter.renders += 1
      return <span data-testid="swatch" />
    }

    const config = {
      alpha: { label: 'Alpha', color: 'rgb(1, 1, 1)', icon: CountingIcon },
    } satisfies ChartConfig

    const Probe = React.memo(function Probe() {
      return <ChartLegendContent payload={PAYLOAD} />
    })

    return { counter, config, Probe }
  }

  function Harness({
    config,
    Probe,
  }: {
    config: ChartConfig
    Probe: React.ComponentType
  }) {
    const [tick, setTick] = React.useState(0)
    return (
      <div>
        <button type="button" onClick={() => setTick((n) => n + 1)}>
          bump {tick}
        </button>
        <ChartContainer config={config}>
          <Probe />
        </ChartContainer>
      </div>
    )
  }

  test('a parent re-render does not re-render the chart consumers', () => {
    const { counter, config, Probe } = makeProbe()
    render(<Harness config={config} Probe={Probe} />)

    // Positive control: the legend really rendered the icon, so the comparison
    // below is not 0 against 0.
    const afterMount = counter.renders
    expect(afterMount).toBeGreaterThan(0)

    for (let i = 0; i < 5; i += 1) {
      fireEvent.click(screen.getByText(/^bump/))
    }
    // Positive control: the parent really re-rendered five times.
    expect(screen.getByText('bump 5')).toBeInTheDocument()

    expect(counter.renders).toBe(afterMount)
  })

  test('a changed config still reaches the chart consumers', () => {
    // Keeps the test above honest: a probe that never re-renders at all would
    // satisfy it too. A new config object has to get through.
    const { counter, config, Probe } = makeProbe()
    const { rerender } = render(<Harness config={config} Probe={Probe} />)
    const afterMount = counter.renders

    rerender(<Harness config={{ ...config }} Probe={Probe} />)
    expect(counter.renders).toBeGreaterThan(afterMount)
  })
})
