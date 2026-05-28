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
      screen.queryByRole('listbox', { name: /description suggestions/i }),
    ).not.toBeInTheDocument();
  });

  test('renders nothing when suggestions list is empty', () => {
    render(
      <QuickAddSuggestions suggestions={[]} query="lun" onPick={() => {}} />,
    );
    expect(
      screen.queryByRole('listbox', { name: /description suggestions/i }),
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
    expect(screen.getByRole('option', { name: 'lunch' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'lunchbox' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Lunar' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'lung' })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'coffee' })).not.toBeInTheDocument();
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
      screen.queryByRole('listbox', { name: /description suggestions/i }),
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
      screen.queryByRole('option', { name: 'lunch' }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('option', { name: 'lunchbox' }),
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
    // The <li role="option"> wraps a <Button> — clicking the option dispatches
    // on the <li>, which doesn't bubble to the button's onClick. Click the
    // button explicitly (the chip the user actually taps).
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
});
