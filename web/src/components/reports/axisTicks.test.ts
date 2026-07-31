import { describe, expect, it } from 'vitest';

import { MAX_AXIS_TICKS, axisTickInterval } from './utils';

/**
 * How many labels Recharts renders for `interval={n}` over `bucketCount`
 * points: the first, then every (n+1)th.
 */
function renderedTickCount(bucketCount: number, interval: number): number {
  if (bucketCount === 0) return 0;
  return Math.ceil(bucketCount / (interval + 1));
}

describe('axisTickInterval', () => {
  // The regression this exists for. `interval="preserveStartEnd"` pins the
  // first and last tick and thins between them; on the Net Cash Flow
  // AreaChart's point scale the last tick sits against the right edge, so its
  // neighbour lost the minTickGap contest. Measured on a real 12-month window
  // at 598px: 11 ticks, jumping May'26 -> Jul'26. The June bar was present and
  // hoverable — only its label was missing, so nothing looked broken.
  it('shows every bucket for the windows the period control offers', () => {
    for (const buckets of [6, 12, 24]) {
      expect(axisTickInterval(buckets)).toBe(0);
      expect(renderedTickCount(buckets, axisTickInterval(buckets))).toBe(buckets);
    }
  });

  // The other end: `interval={0}` renders one rotated <text> node per bucket,
  // which is the dominant render cost of the tab at All-time width.
  it('bounds the label count at every scale, including All time', () => {
    // 516 = an All-time window over a 1984 ledger; 2412 = MAX_REPORT_MONTHS.
    for (let buckets = 1; buckets <= 2412; buckets++) {
      const interval = axisTickInterval(buckets);
      const rendered = renderedTickCount(buckets, interval);
      expect(rendered).toBeLessThanOrEqual(MAX_AXIS_TICKS);
      expect(interval).toBeGreaterThanOrEqual(0);
      expect(Number.isInteger(interval)).toBe(true);
    }
  });

  // A thinned axis that drops so many labels the chart is unreadable is its own
  // failure. Anything past the threshold must still keep a useful number.
  it('keeps a readable number of labels once it starts thinning', () => {
    for (const buckets of [25, 36, 48, 120, 516, 2412]) {
      const rendered = renderedTickCount(buckets, axisTickInterval(buckets));
      expect(rendered).toBeGreaterThanOrEqual(MAX_AXIS_TICKS / 2);
    }
  });

  it('handles an empty series without producing a negative interval', () => {
    expect(axisTickInterval(0)).toBe(0);
  });
});
