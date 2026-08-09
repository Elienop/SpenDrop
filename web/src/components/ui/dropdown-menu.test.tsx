import { useRef } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './dropdown-menu';

/**
 * Two properties of the shared menu primitive, both of which were broken the
 * same way `ui/select.tsx` was, and for the same reason: this file is a
 * VENDORED FORK, and both defects lived in the fork rather than in Radix.
 *
 * 1. Closing the menu returns focus to the trigger. The fork defaulted
 *    `onCloseAutoFocus` to `e.preventDefault()`; Radix composes that with its
 *    own restore via `composeEventHandlers`, which stops on `defaultPrevented`,
 *    so `context.triggerRef.current?.focus()` never ran. Reproduced on the
 *    built app before the fix: focus a row-actions trigger, Enter, Escape,
 *    `document.activeElement` is `<body>`.
 *
 * 2. The item — the row you actually tap to perform the action — carries the
 *    44px touch floor. The trigger was already 44px; the item was 32px.
 *
 * The floor is a `coarse:` CSS gate, so it cannot be measured here: happy-dom
 * runs no layout and the Tailwind stylesheet is never loaded in tests. The
 * token assertion below is a WIRING pin, not a pixel proof, and the docblock
 * says so rather than letting the test look like more than it is. The pixels
 * are proven in the browser.
 */

function Basic({ onSelect = vi.fn() }: { onSelect?: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger aria-label="Row actions">Open</DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem onSelect={onSelect}>Edit</DropdownMenuItem>
        <DropdownMenuItem>Delete</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

describe('focus after the menu closes', () => {
  it('returns to the trigger on Escape, not to <body>', async () => {
    const user = userEvent.setup();
    render(<Basic />);
    const trigger = screen.getByRole('button', { name: 'Row actions' });

    await user.click(trigger);
    // Positive control: the menu really opened, so the focus assertion below
    // is not passing because focus never left in the first place.
    expect(await screen.findByRole('menuitem', { name: 'Edit' })).toBeVisible();

    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('menuitem')).toBeNull());

    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('returns to the trigger after choosing an item', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Basic onSelect={onSelect} />);
    const trigger = screen.getByRole('button', { name: 'Row actions' });

    await user.click(trigger);
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));

    // Positive control again: the item really was chosen.
    expect(onSelect).toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole('menuitem')).toBeNull());

    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('still lets a call site redirect focus somewhere else', async () => {
    // The central fix must not take the choice away: a surface that wants to
    // move focus onward can still do it, and its handler still suppresses the
    // default restore. This is what would break if the fix were written as an
    // unconditional restore instead of a pass-through.
    function Redirecting() {
      const next = useRef<HTMLButtonElement>(null);
      return (
        <>
          <DropdownMenu>
            <DropdownMenuTrigger aria-label="Row actions">
              Open
            </DropdownMenuTrigger>
            <DropdownMenuContent
              onCloseAutoFocus={(event) => {
                event.preventDefault();
                next.current?.focus();
              }}
            >
              <DropdownMenuItem>Edit</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <button ref={next} type="button">
            Next
          </button>
        </>
      );
    }

    const user = userEvent.setup();
    render(<Redirecting />);
    await user.click(screen.getByRole('button', { name: 'Row actions' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Next' })).toHaveFocus(),
    );
    expect(screen.getByRole('button', { name: 'Row actions' })).not.toHaveFocus();
  });
});

describe('the item carries the touch floor', () => {
  it('gives every menu item the pointer-gated floor', async () => {
    // `classList.contains`, not `toContain` on the className string: the
    // substring form matches `md:coarse:min-h-11` and would pass on an item
    // that is 32px at the width that matters.
    const user = userEvent.setup();
    render(<Basic />);
    await user.click(screen.getByRole('button', { name: 'Row actions' }));

    const items = screen.getAllByRole('menuitem');
    expect(items).toHaveLength(2);
    for (const item of items) {
      expect(item.classList.contains('coarse:min-h-11')).toBe(true);
      // Gated, never unconditional: an ungated floor survives every call site
      // through tailwind-merge and would inflate these menus on a mouse
      // desktop, which is a stated constraint.
      expect(item.classList.contains('min-h-11')).toBe(false);
    }
  });

  it('is a floor, not a height, so a two-line item still grows', async () => {
    const user = userEvent.setup();
    render(<Basic />);
    await user.click(screen.getByRole('button', { name: 'Row actions' }));

    const tokens = Array.from(screen.getAllByRole('menuitem')[0].classList);
    expect(tokens).not.toContain('coarse:h-11');
    expect(tokens.some((t) => /^max-h-/.test(t))).toBe(false);
  });
});
