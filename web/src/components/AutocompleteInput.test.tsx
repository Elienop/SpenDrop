import { describe, test, expect, vi, afterEach } from 'vitest';
import { useState } from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
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

  test('desktop combobox does not set aria-controls (inline ghost has no popup)', () => {
    render(<ControlledAutocomplete suggestions={['groceries']} />);
    const input = screen.getByRole('combobox');
    expect(input).not.toHaveAttribute('aria-controls');
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
      // cmdk already provides the listbox role on CommandList — document it.
      expect(screen.getByRole('listbox')).toBeInTheDocument();
    } finally {
      restore();
    }
  });

  test('aria-controls is absent until the popover opens, then references the popup', async () => {
    const restore = mockCoarsePointer(true);
    try {
      const user = userEvent.setup();
      render(<ControlledAutocomplete suggestions={['groceries', 'gas']} />);
      const input = screen.getByRole('combobox');

      // Closed: no controlled popup yet.
      expect(input).not.toHaveAttribute('aria-controls');

      await user.type(input, 'g');
      await waitFor(() => {
        expect(screen.getByRole('option', { name: 'groceries' })).toBeInTheDocument();
      });

      // Open: aria-controls now points at the popover container that wraps the
      // cmdk listbox, and aria-expanded is true.
      expect(input).toHaveAttribute('aria-expanded', 'true');
      const controls = input.getAttribute('aria-controls');
      expect(controls).toBeTruthy();
      expect(document.getElementById(controls as string)).not.toBeNull();
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

// ---------------------------------------------------------------------------
// Key propagation contract (see docs/superpowers/specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md)
// ---------------------------------------------------------------------------
describe('AutocompleteInput — key propagation contract', () => {
  function renderWithParentKeydown(
    props: { suggestions: string[]; initial?: string; onAccept?: (v: string) => void },
  ) {
    const parentKeyDown = vi.fn();
    render(
      <div onKeyDown={parentKeyDown}>
        <ControlledAutocomplete {...props} />
      </div>,
    );
    return { parentKeyDown };
  }

  test('Enter with ghost visible: consumed (preventDefault + stopPropagation), accepts ghost', async () => {
    const user = userEvent.setup();
    const onAccept = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'], onAccept });
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    // Character keystrokes during type() bubble to the parent (by contract —
    // the widget does not act on them). Clear the mock so the assertion only
    // measures the key under test.
    parentKeyDown.mockClear();
    await user.keyboard('{Enter}');

    expect(onAccept).toHaveBeenCalledWith('groceries');
    expect(parentKeyDown).not.toHaveBeenCalled();
  });

  test('Enter without ghost: bubbles to parent, does not call onAccept', async () => {
    const user = userEvent.setup();
    const onAccept = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'], onAccept });
    const input = screen.getByRole('combobox');

    await user.type(input, 'xyz');
    parentKeyDown.mockClear();
    await user.keyboard('{Enter}');

    expect(onAccept).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].key).toBe('Enter');
    expect(parentKeyDown.mock.calls[0][0].defaultPrevented).toBe(false);
  });

  test('Ctrl+Enter bubbles even when ghost visible (modifier bypass)', async () => {
    const onAccept = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'], onAccept });
    const input = screen.getByRole('combobox');

    // Type prefix so ghost appears, then dispatch modifier-Enter directly.
    const user = userEvent.setup();
    await user.type(input, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries');
    parentKeyDown.mockClear();

    fireEvent.keyDown(input, { key: 'Enter', ctrlKey: true });

    expect(onAccept).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].ctrlKey).toBe(true);
    expect(parentKeyDown.mock.calls[0][0].defaultPrevented).toBe(false);
  });

  test('Meta+Enter bubbles even when ghost visible (Mac bypass)', async () => {
    const onAccept = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'], onAccept });
    const input = screen.getByRole('combobox');

    const user = userEvent.setup();
    await user.type(input, 'gro');
    parentKeyDown.mockClear();

    fireEvent.keyDown(input, { key: 'Enter', metaKey: true });

    expect(onAccept).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].metaKey).toBe(true);
    expect(parentKeyDown.mock.calls[0][0].defaultPrevented).toBe(false);
  });

  test('Escape with ghost visible: consumed (does NOT bubble)', async () => {
    const user = userEvent.setup();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'] });
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    parentKeyDown.mockClear();
    await user.keyboard('{Escape}');

    expect(parentKeyDown).not.toHaveBeenCalled();
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('');
  });

  test('Escape without ghost: bubbles to parent', async () => {
    const user = userEvent.setup();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'] });
    const input = screen.getByRole('combobox');

    await user.type(input, 'xyz');
    parentKeyDown.mockClear();
    await user.keyboard('{Escape}');

    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].key).toBe('Escape');
  });
});
