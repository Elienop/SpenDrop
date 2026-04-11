import { describe, it, expect } from 'vitest';
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
