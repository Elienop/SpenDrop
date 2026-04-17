import { describe, test, expect, vi, afterEach } from 'vitest';
import { useState } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AutocompleteInput } from './AutocompleteInput';

// --- Test harness: controlled wrapper that mimics RHF's value flow -----------
function ControlledAutocomplete(props: {
  suggestions: string[];
  onAccept?: (v: string) => void;
  initial?: string;
  afterSibling?: boolean;
}) {
  const [value, setValue] = useState(props.initial ?? '');
  return (
    <>
      <AutocompleteInput
        suggestions={props.suggestions}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onAccept={(v) => {
          setValue(v);
          props.onAccept?.(v);
        }}
        aria-label="description"
      />
      {props.afterSibling ? <button>next field</button> : null}
    </>
  );
}

// --- matchMedia coarse-pointer mock -----------------------------------------
function mockCoarsePointer(enabled: boolean) {
  const original = window.matchMedia;
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: query === '(pointer: coarse)' ? enabled : false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
  return () => {
    window.matchMedia = original;
  };
}

afterEach(() => {
  // Safety: tests that don't mock matchMedia still run fine.
});

describe('AutocompleteInput — desktop (fine pointer)', () => {
  test('renders with role="combobox" and autocomplete="off"', () => {
    render(<ControlledAutocomplete suggestions={['groceries']} />);
    const input = screen.getByRole('combobox');
    expect(input).toHaveAttribute('autocomplete', 'off');
  });

  test('aria-autocomplete is "inline" on desktop', () => {
    render(<ControlledAutocomplete suggestions={['groceries']} />);
    const input = screen.getByRole('combobox');
    expect(input).toHaveAttribute('aria-autocomplete', 'inline');
  });

  test('shows ghost tail when typed prefix matches a suggestion', async () => {
    const user = userEvent.setup();
    render(<ControlledAutocomplete suggestions={['groceries', 'gas']} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');

    const ghost = screen.getByTestId('autocomplete-ghost');
    expect(ghost).toHaveTextContent('ceries');
    expect(input).toHaveAttribute('aria-expanded', 'true');
  });

  test('announces full match via aria-live region', async () => {
    const user = userEvent.setup();
    render(<ControlledAutocomplete suggestions={['groceries']} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');

    expect(screen.getByTestId('autocomplete-live')).toHaveTextContent('groceries');
  });

  test('Tab accepts the ghost when visible', async () => {
    const user = userEvent.setup();
    const onAccept = vi.fn();
    render(<ControlledAutocomplete suggestions={['groceries']} onAccept={onAccept} />);
    const input = screen.getByRole('combobox') as HTMLInputElement;

    await user.type(input, 'gro');
    await user.keyboard('{Tab}');

    expect(onAccept).toHaveBeenCalledWith('groceries');
    expect(input.value).toBe('groceries');
  });

  test('Tab does NOT accept (and moves focus) when no suggestion matches', async () => {
    const user = userEvent.setup();
    const onAccept = vi.fn();
    render(
      <ControlledAutocomplete
        suggestions={['groceries']}
        onAccept={onAccept}
        afterSibling
      />,
    );
    const input = screen.getByRole('combobox');
    const sibling = screen.getByRole('button', { name: 'next field' });

    await user.type(input, 'xyz');
    await user.keyboard('{Tab}');

    expect(onAccept).not.toHaveBeenCalled();
    expect(sibling).toHaveFocus();
  });

  test('ArrowRight at end of prefix accepts the ghost', async () => {
    const user = userEvent.setup();
    const onAccept = vi.fn();
    render(<ControlledAutocomplete suggestions={['groceries']} onAccept={onAccept} />);
    const input = screen.getByRole('combobox') as HTMLInputElement;

    await user.type(input, 'gro');
    await user.keyboard('{ArrowRight}');

    expect(onAccept).toHaveBeenCalledWith('groceries');
    expect(input.value).toBe('groceries');
  });

  test('End accepts the ghost', async () => {
    const user = userEvent.setup();
    const onAccept = vi.fn();
    render(<ControlledAutocomplete suggestions={['groceries']} onAccept={onAccept} />);
    const input = screen.getByRole('combobox') as HTMLInputElement;

    await user.type(input, 'gro');
    await user.keyboard('{End}');

    expect(onAccept).toHaveBeenCalledWith('groceries');
    expect(input.value).toBe('groceries');
  });

  test('Escape dismisses the ghost for the current prefix', async () => {
    const user = userEvent.setup();
    render(<ControlledAutocomplete suggestions={['groceries']} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries');

    await user.keyboard('{Escape}');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('');
    expect(input).toHaveAttribute('aria-expanded', 'false');
  });

  test('Escape suppression clears when user types more characters', async () => {
    const user = userEvent.setup();
    render(<ControlledAutocomplete suggestions={['groceries']} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    await user.keyboard('{Escape}');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('');

    await user.type(input, 'c');
    // prefix "groc" → match "groceries" → ghost tail "eries"
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('eries');
  });

  test('does not match when typed value equals the full suggestion', async () => {
    const user = userEvent.setup();
    render(<ControlledAutocomplete suggestions={['gas']} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gas');

    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('');
    expect(input).toHaveAttribute('aria-expanded', 'false');
  });
});

describe('AutocompleteInput — touch (coarse pointer)', () => {
  test('aria-autocomplete is "list" on touch', () => {
    const restore = mockCoarsePointer(true);
    try {
      render(<ControlledAutocomplete suggestions={['groceries']} />);
      const input = screen.getByRole('combobox');
      expect(input).toHaveAttribute('aria-autocomplete', 'list');
    } finally {
      restore();
    }
  });

  test('typing opens a popover with matching items', async () => {
    const restore = mockCoarsePointer(true);
    try {
      const user = userEvent.setup();
      render(
        <ControlledAutocomplete suggestions={['groceries', 'gas', 'gym', 'zinc']} />,
      );
      const input = screen.getByRole('combobox');

      await user.type(input, 'g');

      // Wait for Radix Popover to portal-mount.
      await waitFor(() => {
        expect(screen.getByRole('option', { name: 'groceries' })).toBeInTheDocument();
      });
      expect(screen.getByRole('option', { name: 'gas' })).toBeInTheDocument();
      expect(screen.getByRole('option', { name: 'gym' })).toBeInTheDocument();
      expect(screen.queryByRole('option', { name: 'zinc' })).not.toBeInTheDocument();
    } finally {
      restore();
    }
  });

  test('tapping an option calls onAccept with that value', async () => {
    const restore = mockCoarsePointer(true);
    try {
      const user = userEvent.setup();
      const onAccept = vi.fn();
      render(
        <ControlledAutocomplete
          suggestions={['groceries', 'gas']}
          onAccept={onAccept}
        />,
      );
      const input = screen.getByRole('combobox') as HTMLInputElement;

      await user.type(input, 'g');
      const option = await screen.findByRole('option', { name: 'groceries' });
      await user.click(option);

      expect(onAccept).toHaveBeenCalledWith('groceries');
      expect(input.value).toBe('groceries');
    } finally {
      restore();
    }
  });

  test('popover does not open when value is empty', () => {
    const restore = mockCoarsePointer(true);
    try {
      render(<ControlledAutocomplete suggestions={['groceries']} />);
      expect(screen.queryByRole('option')).not.toBeInTheDocument();
    } finally {
      restore();
    }
  });

  test('popover does not open when there are no matches', async () => {
    const restore = mockCoarsePointer(true);
    try {
      const user = userEvent.setup();
      render(<ControlledAutocomplete suggestions={['groceries']} />);
      const input = screen.getByRole('combobox');

      await user.type(input, 'zzz');
      expect(screen.queryByRole('option')).not.toBeInTheDocument();
    } finally {
      restore();
    }
  });
});
