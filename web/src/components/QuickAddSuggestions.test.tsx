import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QuickAddSuggestions } from './QuickAddSuggestions';

describe('QuickAddSuggestions', () => {
  test('renders nothing when no suggestion matches the query prefix', () => {
    render(
      <QuickAddSuggestions
        suggestions={['lunch', 'coffee']}
        query="xyz"
        onPick={() => {}}
      />,
    );
    expect(
      screen.queryByRole('group', { name: /description suggestions/i }),
    ).not.toBeInTheDocument();
  });

  test('renders nothing when suggestions list is empty', () => {
    render(
      <QuickAddSuggestions suggestions={[]} query="lun" onPick={() => {}} />,
    );
    expect(
      screen.queryByRole('group', { name: /description suggestions/i }),
    ).not.toBeInTheDocument();
  });

  test('renders top 3 chips matching the prefix (case-insensitive)', () => {
    render(
      <QuickAddSuggestions
        suggestions={['lunch', 'lunchbox', 'Lunar', 'lung', 'coffee']}
        query="lun"
        onPick={() => {}}
      />,
    );
    // Top 3 matches only.
    expect(screen.getByRole('button', { name: 'lunch' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'lunchbox' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Lunar' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'lung' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'coffee' })).not.toBeInTheDocument();
  });

  test('skips an exact-match-only entry (no point suggesting what is already typed)', () => {
    render(
      <QuickAddSuggestions
        suggestions={['lunch']}
        query="lunch"
        onPick={() => {}}
      />,
    );
    expect(
      screen.queryByRole('group', { name: /description suggestions/i }),
    ).not.toBeInTheDocument();
  });

  test('still suggests an extension when the query equals one suggestion exactly', () => {
    render(
      <QuickAddSuggestions
        suggestions={['lunch', 'lunchbox']}
        query="lunch"
        onPick={() => {}}
      />,
    );
    // 'lunch' (exact, no extension) is skipped, but 'lunchbox' (extends it) stays.
    expect(
      screen.queryByRole('button', { name: 'lunch' }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'lunchbox' }),
    ).toBeInTheDocument();
  });

  test('clicking a chip fires onPick with the suggestion text', async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(
      <QuickAddSuggestions
        suggestions={['lunch', 'coffee']}
        query="lu"
        onPick={onPick}
      />,
    );
    // The chips are now plain <Button>s (no <li role="option"> wrapper), so the
    // click target IS the interactive button.
    await user.click(screen.getByRole('button', { name: 'lunch' }));
    expect(onPick).toHaveBeenCalledWith('lunch');
  });

  test('renders the "Tab" hint with md:inline-block on the first chip', () => {
    render(
      <QuickAddSuggestions
        suggestions={['lunch', 'lunchbox']}
        query="lu"
        onPick={() => {}}
      />,
    );
    // The kbd is rendered (class-based viewport gating is verified visually).
    const kbd = screen.getByText('Tab');
    expect(kbd.tagName).toBe('KBD');
    expect(kbd.className).toMatch(/md:inline-block/);
  });

  test('exposes no listbox/option roles (it is a button group, not a combobox)', () => {
    render(
      <QuickAddSuggestions
        suggestions={['lunch', 'lunchbox']}
        query="lu"
        onPick={() => {}}
      />,
    );
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(screen.queryAllByRole('option')).toHaveLength(0);
    expect(screen.getAllByRole('button').length).toBeGreaterThan(0);
  });

  test('the suggestion group has an accessible name', () => {
    render(
      <QuickAddSuggestions
        suggestions={['lunch', 'lunchbox']}
        query="lu"
        onPick={() => {}}
      />,
    );
    expect(
      screen.getByRole('group', { name: /description suggestions/i }),
    ).toBeInTheDocument();
  });

  test('chip meets the 44px touch target (min-h-11)', () => {
    render(
      <QuickAddSuggestions
        suggestions={['lunch', 'coffee']}
        query="lu"
        onPick={() => {}}
      />,
    );
    expect(
      screen.getByRole('button', { name: 'lunch' }).className,
    ).toMatch(/min-h-11/);
  });
});
