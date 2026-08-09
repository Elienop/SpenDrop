import { useRef } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from './select';

/**
 * These pin two properties of the SHARED primitive, because both defects they
 * cover were previously patched at one call site while the other twelve stayed
 * broken (`Settings.tsx` carried a local `onCloseAutoFocus` and a per-option
 * `min-h-11`; both are gone now that this file provides them).
 *
 * 1. The option — the element you actually tap to CHOOSE — carries the 44px
 *    touch floor. Measured 32px in Chrome at 360px before this: stock
 *    `py-1.5` around 20px of `text-sm`.
 * 2. Closing the menu puts focus back on the trigger. The wrapper used to
 *    default `onCloseAutoFocus` to `e.preventDefault()`; Radix composes that
 *    with its own restore via `composeEventHandlers`, which stops on
 *    `defaultPrevented`, so the restore never ran and `document.activeElement`
 *    was `<body>` — verified in Chrome with real key events, still `<body>`
 *    two seconds later.
 */

function Basic({ onValueChange = vi.fn() }: { onValueChange?: (v: string) => void }) {
  return (
    <Select onValueChange={onValueChange}>
      <SelectTrigger aria-label="Fruit">
        <SelectValue placeholder="Pick one" />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="apple">Apple</SelectItem>
        <SelectItem value="pear">Pear</SelectItem>
      </SelectContent>
    </Select>
  );
}

describe('SelectItem touch floor', () => {
  it('gives every option the 44px floor', async () => {
    // `classList.contains`, not `toContain` on the className string: the
    // substring form matches `md:min-h-11` and would pass on an option that
    // is 32px at the width that matters.
    const user = userEvent.setup();
    render(<Basic />);
    await user.click(screen.getByRole('combobox', { name: 'Fruit' }));

    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(2);
    for (const option of options) {
      expect(option.classList.contains('min-h-11')).toBe(true);
    }
  });

  it('is a floor, not a height, so it never caps a two-line option', async () => {
    const user = userEvent.setup();
    render(<Basic />);
    await user.click(screen.getByRole('combobox', { name: 'Fruit' }));

    const tokens = Array.from(screen.getAllByRole('option')[0].classList);
    expect(tokens).not.toContain('h-11');
    expect(tokens.some((t) => /^max-h-/.test(t))).toBe(false);
  });

  it('still lets a call site override the floor', async () => {
    // `cn()` is tailwind-merge, so a caller's own min-height must win rather
    // than fight the base class. Without this the primitive would be a wall.
    const user = userEvent.setup();
    render(
      <Select>
        <SelectTrigger aria-label="Fruit">
          <SelectValue placeholder="Pick one" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="apple" className="min-h-14">
            Apple
          </SelectItem>
        </SelectContent>
      </Select>,
    );
    await user.click(screen.getByRole('combobox', { name: 'Fruit' }));

    const option = screen.getByRole('option', { name: 'Apple' });
    expect(option.classList.contains('min-h-14')).toBe(true);
    expect(option.classList.contains('min-h-11')).toBe(false);
  });
});

describe('focus after the menu closes', () => {
  it('lands on the trigger, not on <body>', async () => {
    const user = userEvent.setup();
    const onValueChange = vi.fn();
    render(<Basic onValueChange={onValueChange} />);
    const trigger = screen.getByRole('combobox', { name: 'Fruit' });

    await user.click(trigger);
    await user.click(screen.getByRole('option', { name: 'Pear' }));

    // Positive control: the menu really opened and really committed a value,
    // so `toHaveFocus` below is not passing because focus never left in the
    // first place.
    expect(onValueChange).toHaveBeenCalledWith('pear');
    await waitFor(() => expect(screen.queryByRole('option')).toBeNull());

    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('lets a call site redirect focus somewhere else', async () => {
    // The central fix must not take the choice away: a surface that wants to
    // move focus onward (the next field in a form, say) still can, and its
    // handler still suppresses the default restore.
    function Redirecting() {
      const next = useRef<HTMLButtonElement>(null);
      return (
        <>
          <Select>
            <SelectTrigger aria-label="Fruit">
              <SelectValue placeholder="Pick one" />
            </SelectTrigger>
            <SelectContent
              onCloseAutoFocus={(event) => {
                event.preventDefault();
                next.current?.focus();
              }}
            >
              <SelectItem value="apple">Apple</SelectItem>
            </SelectContent>
          </Select>
          <button ref={next} type="button">
            Next
          </button>
        </>
      );
    }

    const user = userEvent.setup();
    render(<Redirecting />);
    await user.click(screen.getByRole('combobox', { name: 'Fruit' }));
    await user.click(screen.getByRole('option', { name: 'Apple' }));

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Next' })).toHaveFocus(),
    );
    expect(screen.getByRole('combobox', { name: 'Fruit' })).not.toHaveFocus();
  });
});
