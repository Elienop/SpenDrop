import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, cleanup, waitFor } from '@testing-library/react';
import React from 'react';
import type { TagBreakdownEntry } from '@/api/types';

// Separate file from `PatternsTab.test.tsx` because of the mock below: that
// file deliberately renders with an UNSIZED ResponsiveContainer (happy-dom
// measures its parent as 0x0 and recharts bails), so no chart body exists there
// to read bars off. Sizing it is file-scoped, so the two cannot share a file.
//
// SIZING-ONLY mock, the pattern proven in `components/ui/chart.test.tsx`:
// everything below the container is the genuine library, which is the point —
// the fills asserted here are the ones recharts actually painted.
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

// TWENTY-ONE tags, so the palette has to WRAP. The 21st row is the whole point
// of the length: the slot is `(i % 20) + 1`, so tag 21 must return to
// `--chart-1` rather than reaching for a `--chart-21` that no theme defines.
// A fixture of three would pass with the modulus deleted.
//
// Descending totals so the fixture order is the drawn order, and every total is
// distinct and non-zero so no bar is dropped for having no width.
const TAGS: TagBreakdownEntry[] = Array.from({ length: 21 }, (_, i) => ({
  tag: `tag-${i + 1}`,
  total: 1000 - i * 10,
  count: i + 1,
}));

const ok = <T,>(data: T) => ({ data, loading: false, fetching: false, error: '' });

vi.mock('@/hooks/useReports', () => ({
  useTagBreakdown: () => ok(TAGS),
  useSpendingHeatmap: () => ok([]),
  useRecurring: () => ({ ...ok([]), refetch: vi.fn() }),
  dismissRecurring: vi.fn(),
}));

vi.mock('@/hooks/useBaseCurrency', () => ({ useBaseCurrency: () => 'USD' }));

vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => ({
    years: [2026],
    currentYear: 2026,
    hasTransactions: true,
    outOfRangeYears: [],
    futureYears: [],
    loading: false,
  }),
}));

import { PatternsTab } from './PatternsTab';

afterEach(cleanup);

/** The Tag Analysis card, located by the heading it is labelled by. */
function tagCard(container: HTMLElement): HTMLElement {
  const el = container.querySelector<HTMLElement>(
    '[aria-labelledby="tag-analysis-heading"]',
  );
  if (!el) throw new Error('Tag Analysis card did not render');
  return el;
}

describe('the tag chart paints one colour per tag', () => {
  test('every bar carries its own palette slot, wrapping after twenty', async () => {
    const { container } = render(<PatternsTab />);
    const card = tagCard(container);

    // `<Bar>` mounts its rectangle groups synchronously but EMPTY — the
    // `<path>` arrives on the first animation frame. Wait for the exact count,
    // so a chart that silently stops drawing fails here rather than passing on
    // an empty list.
    await waitFor(
      () => {
        expect(
          card.querySelectorAll('.recharts-bar .recharts-rectangle'),
        ).toHaveLength(TAGS.length);
      },
      { timeout: 5000 },
    );

    // Real <Rectangle> paths from recharts' Bar, not a stub div. This replaced
    // a `<Cell fill=… />` child per row (deprecated in recharts 3.10, removed
    // in 4.0) with the `shape` prop recharts documents; the assertion is
    // unchanged in substance across that migration, which is what makes it the
    // evidence that every bar kept its colour.
    expect(
      Array.from(
        card.querySelectorAll('.recharts-bar .recharts-rectangle'),
      ).map((el) => el.getAttribute('fill')),
    ).toEqual(
      TAGS.map((_, i) => `hsl(var(--chart-${(i % 20) + 1}))`),
    );
  });

  test('the shape prop forwards the bar geometry it was handed', async () => {
    // `shape` REPLACES recharts' own renderer, so a shape that forwards only
    // the fill would drop the rect entirely — and a chart of invisible bars is
    // exactly what the fill assertion above cannot see (it reads attributes off
    // paths that would not exist). This pins the two things the custom shape
    // has to pass through: the rounded corners `radius={4}` asks for, and a
    // width that came from the data rather than a default.
    const { container } = render(<PatternsTab />);
    const card = tagCard(container);
    await waitFor(
      () => {
        expect(
          card.querySelectorAll('.recharts-bar .recharts-rectangle'),
        ).toHaveLength(TAGS.length);
      },
      { timeout: 5000 },
    );

    const bars = Array.from(
      card.querySelectorAll('.recharts-bar .recharts-rectangle'),
    );
    for (const bar of bars) {
      // `radius={4}` reaches the rect two ways, and both are asserted: as the
      // prop recharts echoes onto the path, and as the elliptical arcs it
      // draws with. A squared-off rectangle is a straight `h`/`v` path with no
      // `A` command in it at all.
      //
      // The arc's RADII are not pinned: recharts clamps the corner to half the
      // shorter side and interpolates the whole rect over the 400ms enter
      // animation, so the numbers there are a function of when the assertion
      // ran, not of the prop.
      expect(bar.getAttribute('radius')).toBe('4');
      expect(bar.getAttribute('d')).toContain('A ');
      expect(Number(bar.getAttribute('width'))).toBeGreaterThan(0);
    }

    // Widths track the totals, descending with the fixture: proof the geometry
    // is the scale's, not a constant the shape invented.
    const widths = bars.map((b) => Number(b.getAttribute('width')));
    expect(widths).toEqual([...widths].sort((a, b) => b - a));
    expect(new Set(widths).size).toBeGreaterThan(1);
  });
});
