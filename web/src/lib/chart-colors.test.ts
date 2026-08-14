/// <reference types="node" />
// ^ tsconfig.app.json deliberately restricts `types` to vite/client, and both
//   Vite escapes for reading a file without node:fs (`.css?raw`, `.css?inline`)
//   return an empty string under vitest's css pipeline — so this file opts into
//   @types/node explicitly to read globals.css from disk.
import { describe, it, expect } from 'vitest';
import { readFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { getCategoryColorVar } from './chart-colors';

describe('getCategoryColorVar', () => {
  it('maps category id 1 to --chart-1', () => {
    expect(getCategoryColorVar({ id: 1 })).toBe('hsl(var(--chart-1))');
  });
  it('maps category id 11 to --chart-11', () => {
    expect(getCategoryColorVar({ id: 11 })).toBe('hsl(var(--chart-11))');
  });
  it('maps category id 12 to --chart-12 (no collision with id 1)', () => {
    expect(getCategoryColorVar({ id: 12 })).toBe('hsl(var(--chart-12))');
  });
  it('maps category id 19 to --chart-19 (last seed category)', () => {
    expect(getCategoryColorVar({ id: 19 })).toBe('hsl(var(--chart-19))');
  });
  it('maps category id 20 to --chart-20 (boundary)', () => {
    expect(getCategoryColorVar({ id: 20 })).toBe('hsl(var(--chart-20))');
  });
  it('wraps id 21 to --chart-1 (modulo wrap)', () => {
    expect(getCategoryColorVar({ id: 21 })).toBe('hsl(var(--chart-1))');
  });
  it('wraps id 40 to --chart-20', () => {
    expect(getCategoryColorVar({ id: 40 })).toBe('hsl(var(--chart-20))');
  });
  it('wraps id 41 to --chart-1', () => {
    expect(getCategoryColorVar({ id: 41 })).toBe('hsl(var(--chart-1))');
  });
});

// ── light-mode palette invariants (globals.css) ──────────────────────────────
// CategoryChips/CategoryBadge render each slot's raw color as TEXT on a white
// card (over a 12-15% self-tinted wash, which costs ~5-8% of the ratio — the
// 5.0 floor absorbs that). Slot hues must also stay in the dark slot's hue
// family: a category's color identity may not shift between themes.

const SLOTS = 20;

function parseChartVars(block: string): Map<number, [number, number, number]> {
  const out = new Map<number, [number, number, number]>();
  const re = /--chart-(\d+):\s*([\d.]+)\s+([\d.]+)%\s+([\d.]+)%/g;
  for (const m of block.matchAll(re)) {
    out.set(Number(m[1]), [Number(m[2]), Number(m[3]), Number(m[4])]);
  }
  return out;
}

function hslToRgb(h: number, s: number, l: number): [number, number, number] {
  s /= 100;
  l /= 100;
  const k = (n: number) => (n + h / 30) % 12;
  const a = s * Math.min(l, 1 - l);
  const f = (n: number) =>
    l - a * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1)));
  return [f(0), f(8), f(4)];
}

// WCAG 2.x relative luminance -> contrast ratio against white.
function contrastOnWhite(rgb: [number, number, number]): number {
  const [r, g, b] = rgb.map((v) =>
    v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4,
  );
  const y = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return 1.05 / (y + 0.05);
}

describe('light-mode chart palette (globals.css)', () => {
  // fs, not `?raw`: vitest's css pipeline intercepts `.css?raw` and returns
  // an empty string, which would make every assertion below vacuous.
  const cssPath = ['src/globals.css', 'web/src/globals.css']
    .map((p) => resolve(process.cwd(), p))
    .find(existsSync);
  if (!cssPath) throw new Error('globals.css not found from ' + process.cwd());
  const globalsCss = readFileSync(cssPath, 'utf8');
  const lightBlock = /:root\s*\{([^}]*)\}/.exec(globalsCss)?.[1] ?? '';
  const darkBlock = /\.dark\s*\{([^}]*)\}/.exec(globalsCss)?.[1] ?? '';
  const light = parseChartVars(lightBlock);
  const dark = parseChartVars(darkBlock);

  it('defines all 20 slots in both theme blocks', () => {
    expect(light.size).toBe(SLOTS);
    expect(dark.size).toBe(SLOTS);
  });

  it.each([...light.entries()])(
    'slot %i reaches >= 5.0:1 as text on white',
    (_slot, [h, s, l]) => {
      expect(contrastOnWhite(hslToRgb(h, s, l))).toBeGreaterThanOrEqual(5.0);
    },
  );

  it.each([...light.keys()])(
    'slot %i keeps the dark slot’s hue family (Δhue <= 12)',
    (slot) => {
      const lightHue = light.get(slot)![0];
      const darkHue = dark.get(slot)![0];
      const d = Math.abs(lightHue - darkHue) % 360;
      expect(Math.min(d, 360 - d)).toBeLessThanOrEqual(12);
    },
  );
});
