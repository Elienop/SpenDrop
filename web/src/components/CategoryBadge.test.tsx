import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { CategoryBadge } from './CategoryBadge';

describe('CategoryBadge', () => {
  test('renders category name', () => {
    render(<CategoryBadge name="Food" color="#ff0000" />);
    expect(screen.getByText('Food')).toBeInTheDocument();
  });

  test('applies the category color as background', () => {
    render(<CategoryBadge name="Rent" color="#00ff00" />);
    const badge = screen.getByText('Rent');
    expect(badge.style.backgroundColor).toBe('#00ff00');
  });
});
