import { describe, it, expect } from 'vitest';
import { getCategoryColorVar } from './chart-colors';

describe('getCategoryColorVar', () => {
  it('maps category id 1 to --chart-1', () => {
    expect(getCategoryColorVar({ id: 1 })).toBe('hsl(var(--chart-1))');
  });
  it('maps category id 11 to --chart-11 (boundary)', () => {
    expect(getCategoryColorVar({ id: 11 })).toBe('hsl(var(--chart-11))');
  });
  it('wraps id 12 to --chart-1 (modulo wrap)', () => {
    expect(getCategoryColorVar({ id: 12 })).toBe('hsl(var(--chart-1))');
  });
  it('wraps id 22 to --chart-11', () => {
    expect(getCategoryColorVar({ id: 22 })).toBe('hsl(var(--chart-11))');
  });
  it('wraps id 23 to --chart-1', () => {
    expect(getCategoryColorVar({ id: 23 })).toBe('hsl(var(--chart-1))');
  });
});
