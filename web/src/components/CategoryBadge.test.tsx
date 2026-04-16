import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { CategoryBadge } from './CategoryBadge';

describe('CategoryBadge', () => {
  it('sets the chart-color CSS variable based on the category id', () => {
    render(<CategoryBadge category={{ id: 3, name: 'Groceries' }} />);
    const badge = screen.getByText('Groceries');
    // id 3 → chart-3 (see getCategoryColorVar: ((id-1) % 20) + 1)
    expect(badge.style.getPropertyValue('--badge-color')).toBe(
      'hsl(var(--chart-3))',
    );
  });

  it('maps id 12 to chart-12 (no collision with id 1)', () => {
    render(<CategoryBadge category={{ id: 12, name: 'Wrap' }} />);
    const badge = screen.getByText('Wrap');
    expect(badge.style.getPropertyValue('--badge-color')).toBe(
      'hsl(var(--chart-12))',
    );
  });
});
