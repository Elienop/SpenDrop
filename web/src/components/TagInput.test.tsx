import { describe, test, expect, vi } from 'vitest';
import type { ReactNode } from 'react';
import { useState } from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TagInput } from './TagInput';

// --- Test harness: controlled wrapper --------------------------------------
function ControlledTagInput(props: {
  suggestions?: string[];
  initial?: string;
  onChange?: (v: string) => void;
}) {
  const [value, setValue] = useState(props.initial ?? '');
  return (
    <TagInput
      value={value}
      onChange={(v) => {
        setValue(v);
        props.onChange?.(v);
      }}
      suggestions={props.suggestions}
    />
  );
}

// --- matchMedia coarse-pointer mock ----------------------------------------
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

describe('TagInput', () => {
  test('renders empty input with placeholder when no tags', () => {
    render(<TagInput value="" onChange={() => {}} />);
    expect(screen.getByPlaceholderText('Add tag...')).toBeInTheDocument();
  });

  test('renders custom placeholder', () => {
    render(<TagInput value="" onChange={() => {}} placeholder="Type here..." />);
    expect(screen.getByPlaceholderText('Type here...')).toBeInTheDocument();
  });

  test('displays existing tags as pills', () => {
    render(<TagInput value="food,rent,travel" onChange={() => {}} />);
    expect(screen.getByText('food')).toBeInTheDocument();
    expect(screen.getByText('rent')).toBeInTheDocument();
    expect(screen.getByText('travel')).toBeInTheDocument();
  });

  test('hides placeholder when tags exist', () => {
    render(<TagInput value="food" onChange={() => {}} />);
    const input = screen.getByRole('combobox');
    expect(input).not.toHaveAttribute('placeholder', 'Add tag...');
  });

  test('adds tag on Enter key', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="" onChange={onChange} />);

    const input = screen.getByRole('combobox');
    await user.type(input, 'groceries{Enter}');

    expect(onChange).toHaveBeenCalledWith('groceries');
  });

  test('adds tag on comma key', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="" onChange={onChange} />);

    const input = screen.getByRole('combobox');
    await user.type(input, 'groceries,');

    expect(onChange).toHaveBeenCalledWith('groceries');
  });

  test('appends tag to existing tags', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="food,rent" onChange={onChange} />);

    const input = screen.getByRole('combobox');
    await user.type(input, 'travel{Enter}');

    expect(onChange).toHaveBeenCalledWith('food,rent,travel');
  });

  test('does not add duplicate tags', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="food,rent" onChange={onChange} />);

    const input = screen.getByRole('combobox');
    await user.type(input, 'food{Enter}');

    expect(onChange).not.toHaveBeenCalled();
  });

  test('does not add empty/whitespace tags', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="" onChange={onChange} />);

    const input = screen.getByRole('combobox');
    await user.type(input, '   {Enter}');

    expect(onChange).not.toHaveBeenCalled();
  });

  test('removes tag when remove button clicked', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="food,rent,travel" onChange={onChange} />);

    const removeButton = screen.getByRole('button', { name: /remove tag rent/i });
    await user.click(removeButton);

    expect(onChange).toHaveBeenCalledWith('food,travel');
  });

  test('removes last tag on Backspace when input is empty', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="food,rent" onChange={onChange} />);

    const input = screen.getByRole('combobox');
    await user.click(input);
    await user.keyboard('{Backspace}');

    expect(onChange).toHaveBeenCalledWith('food');
  });

  test('adds tag on blur', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <div>
        <TagInput value="" onChange={onChange} />
        <button>other</button>
      </div>
    );

    const input = screen.getByRole('combobox');
    await user.type(input, 'groceries');
    await user.click(screen.getByRole('button', { name: 'other' }));

    expect(onChange).toHaveBeenCalledWith('groceries');
  });

  test('clears input after adding a tag', async () => {
    const user = userEvent.setup();
    render(<TagInput value="" onChange={() => {}} />);

    const input = screen.getByRole('combobox') as HTMLInputElement;
    await user.type(input, 'groceries{Enter}');

    expect(input.value).toBe('');
  });

  test('trims whitespace from tags', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="" onChange={onChange} />);

    const input = screen.getByRole('combobox');
    await user.type(input, '  groceries  {Enter}');

    expect(onChange).toHaveBeenCalledWith('groceries');
  });

  test('each tag pill has an accessible remove button', () => {
    render(<TagInput value="food,rent" onChange={() => {}} />);
    expect(screen.getByRole('button', { name: /remove tag food/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /remove tag rent/i })).toBeInTheDocument();
  });

  test('handles value with extra whitespace around commas', () => {
    render(<TagInput value=" food , rent , travel " onChange={() => {}} />);
    expect(screen.getByText('food')).toBeInTheDocument();
    expect(screen.getByText('rent')).toBeInTheDocument();
    expect(screen.getByText('travel')).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// New: ghost autocomplete tests (desktop)
// ---------------------------------------------------------------------------
describe('TagInput — desktop ghost autocomplete', () => {
  test('role="combobox" and autocomplete="off" on inner input', () => {
    render(<TagInput value="" onChange={() => {}} />);
    const input = screen.getByRole('combobox');
    expect(input).toHaveAttribute('autocomplete', 'off');
  });

  test('aria-autocomplete is "inline" on desktop', () => {
    render(<TagInput value="" onChange={() => {}} />);
    const input = screen.getByRole('combobox');
    expect(input).toHaveAttribute('aria-autocomplete', 'inline');
  });

  test('shows ghost tail when typed prefix matches a suggestion', async () => {
    const user = userEvent.setup();
    render(<ControlledTagInput suggestions={['groceries', 'gas']} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');

    const ghost = screen.getByTestId('taginput-ghost');
    expect(ghost).toHaveTextContent('ceries');
    expect(input).toHaveAttribute('aria-expanded', 'true');
  });

  test('announces full match via aria-live region', async () => {
    const user = userEvent.setup();
    render(<ControlledTagInput suggestions={['groceries']} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');

    expect(screen.getByTestId('taginput-live')).toHaveTextContent('groceries');
  });

  test('Tab accepts the ghost as a new tag', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<ControlledTagInput suggestions={['groceries']} onChange={onChange} />);
    const input = screen.getByRole('combobox') as HTMLInputElement;

    await user.type(input, 'gro');
    await user.keyboard('{Tab}');

    expect(onChange).toHaveBeenCalledWith('groceries');
    expect(input.value).toBe('');
  });

  test('ArrowRight at end of buffer accepts the ghost as a tag', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<ControlledTagInput suggestions={['groceries']} onChange={onChange} />);
    const input = screen.getByRole('combobox') as HTMLInputElement;

    await user.type(input, 'gro');
    await user.keyboard('{ArrowRight}');

    expect(onChange).toHaveBeenCalledWith('groceries');
    expect(input.value).toBe('');
  });

  test('End accepts the ghost as a tag', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<ControlledTagInput suggestions={['groceries']} onChange={onChange} />);
    const input = screen.getByRole('combobox') as HTMLInputElement;

    await user.type(input, 'gro');
    await user.keyboard('{End}');

    expect(onChange).toHaveBeenCalledWith('groceries');
    expect(input.value).toBe('');
  });

  test('Escape dismisses the ghost; typing more restores suggestions', async () => {
    const user = userEvent.setup();
    render(<ControlledTagInput suggestions={['groceries']} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    expect(screen.getByTestId('taginput-ghost')).toHaveTextContent('ceries');

    await user.keyboard('{Escape}');
    expect(screen.queryByTestId('taginput-ghost')).not.toBeInTheDocument();
    expect(input).toHaveAttribute('aria-expanded', 'false');

    await user.type(input, 'c');
    expect(screen.getByTestId('taginput-ghost')).toHaveTextContent('eries');
  });

  test('does not suggest a tag that already exists in tags', async () => {
    const user = userEvent.setup();
    render(
      <ControlledTagInput suggestions={['groceries']} initial="groceries" />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');

    expect(screen.queryByTestId('taginput-ghost')).not.toBeInTheDocument();
    expect(input).toHaveAttribute('aria-expanded', 'false');
  });

  test('Enter still commits the typed buffer when no ghost', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<ControlledTagInput suggestions={['groceries']} onChange={onChange} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'xyz{Enter}');

    expect(onChange).toHaveBeenCalledWith('xyz');
  });

  test('Enter commits the GHOST when ghost is visible', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<ControlledTagInput suggestions={['groceries']} onChange={onChange} />);
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro{Enter}');

    expect(onChange).toHaveBeenCalledWith('groceries');
  });
});

// ---------------------------------------------------------------------------
// New: touch popover tests
// ---------------------------------------------------------------------------
describe('TagInput — touch (coarse pointer)', () => {
  test('aria-autocomplete is "list" on touch', () => {
    const restore = mockCoarsePointer(true);
    try {
      render(<TagInput value="" onChange={() => {}} suggestions={['groceries']} />);
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
        <ControlledTagInput
          suggestions={['groceries', 'gas', 'gym', 'zinc']}
        />,
      );
      const input = screen.getByRole('combobox');

      await user.type(input, 'g');

      await waitFor(() => {
        expect(
          screen.getByRole('option', { name: 'groceries' }),
        ).toBeInTheDocument();
      });
      expect(screen.getByRole('option', { name: 'gas' })).toBeInTheDocument();
      expect(screen.getByRole('option', { name: 'gym' })).toBeInTheDocument();
      expect(screen.queryByRole('option', { name: 'zinc' })).not.toBeInTheDocument();
    } finally {
      restore();
    }
  });

  test('tapping an item adds the tag and closes popover', async () => {
    const restore = mockCoarsePointer(true);
    try {
      const user = userEvent.setup();
      const onChange = vi.fn();
      render(
        <ControlledTagInput
          suggestions={['groceries', 'gas']}
          onChange={onChange}
        />,
      );
      const input = screen.getByRole('combobox') as HTMLInputElement;

      await user.type(input, 'g');
      const option = await screen.findByRole('option', { name: 'groceries' });
      await user.click(option);

      expect(onChange).toHaveBeenCalledWith('groceries');
      expect(input.value).toBe('');
      await waitFor(() => {
        expect(
          screen.queryByRole('option', { name: 'groceries' }),
        ).not.toBeInTheDocument();
      });
    } finally {
      restore();
    }
  });
});

// ---------------------------------------------------------------------------
// Key propagation contract (see docs/superpowers/specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md)
// ---------------------------------------------------------------------------
describe('TagInput — key propagation contract', () => {
  function renderWithParentKeydown(node: ReactNode) {
    const parentKeyDown = vi.fn();
    render(<div onKeyDown={parentKeyDown}>{node}</div>);
    return { parentKeyDown };
  }

  test('Enter with typed buffer: consumed, addTag fires, does not bubble', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    // Character keystrokes during type() bubble to the parent (by contract —
    // the widget does not act on them). Clear the mock so the assertion only
    // measures the key under test.
    parentKeyDown.mockClear();
    await user.keyboard('{Enter}');

    expect(onChange).toHaveBeenCalledWith('gro');
    expect(parentKeyDown).not.toHaveBeenCalled();
  });

  test('Enter with empty buffer: NOT consumed, bubbles to parent', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');

    await user.click(input);
    await user.keyboard('{Enter}');

    expect(onChange).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].key).toBe('Enter');
  });

  test('Ctrl+Enter with typed buffer: NOT consumed (modifier bypass), bubbles', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    parentKeyDown.mockClear();
    fireEvent.keyDown(input, { key: 'Enter', ctrlKey: true });

    expect(onChange).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].ctrlKey).toBe(true);
  });

  test('Meta+Enter with typed buffer: NOT consumed, bubbles', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    parentKeyDown.mockClear();
    fireEvent.keyDown(input, { key: 'Enter', metaKey: true });

    expect(onChange).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].metaKey).toBe(true);
  });

  test('Escape with ghost visible: consumed, does not bubble', async () => {
    const user = userEvent.setup();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput suggestions={['groceries']} />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    expect(screen.getByTestId('taginput-ghost')).toHaveTextContent('ceries');
    parentKeyDown.mockClear();

    await user.keyboard('{Escape}');

    expect(parentKeyDown).not.toHaveBeenCalled();
  });

  test('Escape with no ghost and no popover: bubbles to parent', async () => {
    const user = userEvent.setup();
    const { parentKeyDown } = renderWithParentKeydown(<ControlledTagInput />);
    const input = screen.getByRole('combobox');

    await user.click(input);
    await user.keyboard('{Escape}');

    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].key).toBe('Escape');
  });

  test("',' with empty buffer: preventDefault (no literal comma inserted), does not addTag", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox') as HTMLInputElement;

    await user.click(input);
    parentKeyDown.mockClear();
    await user.keyboard(',');

    expect(onChange).not.toHaveBeenCalled();
    // Literal ',' was swallowed — buffer must stay empty.
    expect(input.value).toBe('');
  });
});
