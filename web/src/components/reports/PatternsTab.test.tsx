import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, within } from '@testing-library/react';

/**
 * The two things this tab owns for the heatmap that the heatmap itself cannot
 * assert: whether the Card is actually NAMED, and whether the loading
 * placeholder is sized for the branch that is about to render.
 */

const heatmapResult = {
  data: [] as { date: string; total: number }[],
  loading: false,
  fetching: false,
  error: '',
};
const useSpendingHeatmap = vi.fn(() => heatmapResult);

const idle = <T,>(data: T) => ({ data, loading: false, fetching: false, error: '' });

vi.mock('@/hooks/useReports', () => ({
  useSpendingHeatmap: () => useSpendingHeatmap(),
  useTagBreakdown: () => idle([]),
  useRecurring: () => ({ ...idle([]), refetch: vi.fn() }),
  dismissRecurring: vi.fn(),
}));

vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => ({
    years: [2026],
    currentYear: 2026,
    hasTransactions: true,
    outOfRangeYears: [] as number[],
    futureYears: [] as number[],
    loading: false,
  }),
}));

vi.mock('@/hooks/useBaseCurrency', () => ({ useBaseCurrency: () => 'USD' }));

import { PatternsTab } from './PatternsTab';

interface HappyDomWindow {
  happyDOM?: {
    setViewport: (viewport: { width?: number; height?: number }) => void;
  };
}

const DESKTOP_WIDTH = 1024;
const PHONE_WIDTH = 360;

function setViewportWidth(width: number): void {
  const controllable = window as unknown as HappyDomWindow;
  if (!controllable.happyDOM) {
    throw new Error(
      'happy-dom viewport control is unavailable — mobile tests cannot run',
    );
  }
  controllable.happyDOM.setViewport({ width });
}

/** The heatmap card's placeholder is the first one on the tab. */
function skeletonHeightToken(container: HTMLElement): string {
  const skeletons = container.querySelectorAll('.animate-pulse');
  expect(skeletons.length).toBeGreaterThan(0);
  const token = Array.from(skeletons[0].classList).find((c) =>
    /^h-\[\d+px\]$/.test(c),
  );
  if (token === undefined) {
    throw new Error(
      `no explicit height on the heatmap skeleton: ${skeletons[0].getAttribute('class')}`,
    );
  }
  return token;
}

beforeEach(() => {
  useSpendingHeatmap.mockReturnValue(heatmapResult);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  setViewportWidth(DESKTOP_WIDTH);
});

describe('the heatmap card', () => {
  test('is a named region, so its aria-labelledby is not discarded', () => {
    setViewportWidth(DESKTOP_WIDTH);
    render(<PatternsTab />);
    // `Card` is a bare <div> whose implicit `generic` role is on ARIA's
    // name-prohibited list — without an explicit role the label is dropped
    // and the card reads as unnamed. Querying BY ROLE AND NAME is what makes
    // that failure visible; querying by text would pass either way.
    const region = screen.getByRole('region', { name: 'Spending Heatmap' });
    expect(within(region).getByRole('grid')).toBeInTheDocument();
  });

  test('all three cards on the tab are named regions, not just this one', () => {
    // M5: one landmark among three identical cards is worse than none — a
    // landmark list would show a single arbitrary card and hide its siblings.
    setViewportWidth(DESKTOP_WIDTH);
    render(<PatternsTab />);
    expect(
      screen.getAllByRole('region').map((r) => r.getAttribute('aria-labelledby')),
    ).toEqual([
      'spending-heatmap-heading',
      'recurring-heading',
      'tag-analysis-heading',
    ]);
    for (const name of ['Spending Heatmap', 'Recurring Expenses', 'Tag Analysis']) {
      expect(screen.getByRole('region', { name })).toBeInTheDocument();
    }
  });

  test('reserves a different placeholder height per branch', () => {
    // One number cannot be right for a 7-row year grid and a phone month grid
    // plus its month picker. Asserting the two DIFFER (rather than pinning two
    // literals) is what fails if the gate is dropped and one size is used for
    // both — which is exactly how the old flat 120px went unnoticed.
    useSpendingHeatmap.mockReturnValue({ ...heatmapResult, loading: true });

    setViewportWidth(DESKTOP_WIDTH);
    const desktop = render(<PatternsTab />);
    const desktopToken = skeletonHeightToken(desktop.container);
    cleanup();

    setViewportWidth(PHONE_WIDTH);
    const phone = render(<PatternsTab />);
    const phoneToken = skeletonHeightToken(phone.container);

    expect(phoneToken).not.toBe(desktopToken);
    expect(Number(/\d+/.exec(phoneToken)![0])).toBeGreaterThan(
      Number(/\d+/.exec(desktopToken)![0]),
    );
  });

  test('the placeholder reserves more than the old flat 120px on desktop', () => {
    // The pre-existing defect: the real desktop grid plus legend is closer to
    // 200px, so the card grew taller the moment data landed.
    useSpendingHeatmap.mockReturnValue({ ...heatmapResult, loading: true });
    setViewportWidth(DESKTOP_WIDTH);
    const { container } = render(<PatternsTab />);
    expect(
      Number(/\d+/.exec(skeletonHeightToken(container))![0]),
    ).toBeGreaterThan(120);
  });
});
