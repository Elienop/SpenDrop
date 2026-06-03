import { describe, test, expect } from 'vitest';
import { tagChartHeightPx } from './tagChart';

// The Tag Analysis chart is a horizontal bar chart with one bar per tag. It used
// to be locked to a fixed 300px, which squeezed the bars to a few unreadable
// pixels each once a household had many tags. The height must now grow with the
// tag count so every bar stays readable.
describe('tagChartHeightPx', () => {
  test('keeps a sensible floor for a handful of tags', () => {
    expect(tagChartHeightPx(0)).toBe(224);
    expect(tagChartHeightPx(1)).toBe(224);
    expect(tagChartHeightPx(4)).toBe(224); // 4*36+48 = 192 < floor
  });

  test('grows linearly once past the floor so bars stay readable', () => {
    // 6 tags: 6*36+48 = 264; 20 tags: 20*36+48 = 768.
    expect(tagChartHeightPx(6)).toBe(264);
    expect(tagChartHeightPx(20)).toBe(768);
  });

  test('is monotonic and never shrinks as tags are added', () => {
    let prev = -1;
    for (let n = 0; n <= 50; n++) {
      const h = tagChartHeightPx(n);
      expect(h).toBeGreaterThanOrEqual(prev);
      prev = h;
    }
  });

  test('a busy ledger is taller than the old fixed 300px', () => {
    // The regression we are guarding against: 10+ tags must not be crammed
    // into the old 300px box.
    expect(tagChartHeightPx(10)).toBeGreaterThan(300);
  });
});
