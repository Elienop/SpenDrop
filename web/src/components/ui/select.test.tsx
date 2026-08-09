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
import tailwindConfigSource from '../../../tailwind.config.ts?raw';

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

/**
 * WHAT THESE CAN AND CANNOT PROVE. Nothing here measures a pixel, and no
 * assertion below should be read as if it did: happy-dom runs no layout, and
 * the Tailwind stylesheet is never loaded in tests (`globals.css` is imported
 * only by `main.tsx`, which no test renders). A class token is in the DOM at
 * every pointer state, so a `classList.contains('coarse:min-h-11')` check is
 * true on a mouse and on a finger alike — testing "does the gate flip" here
 * would be testing a string.
 *
 * What they DO prove is the wiring, which is where this mechanism can silently
 * fail: that the floor lives on the shared primitive rather than at a call
 * site, that it is the pointer-gated token and not one of the two variants
 * that were rejected, that tailwind-merge lets it coexist with a caller's own
 * height, and that the variant it depends on is still registered — delete the
 * plugin and `coarse:min-h-11` compiles to no CSS at all while every render
 * test stays green.
 *
 * The pixels are the browser check's job.
 */
describe('SelectTrigger touch floor', () => {
  it('carries the pointer-gated 44px floor, not either rejected variant', () => {
    render(<Basic />);
    const tokens = Array.from(
      screen.getByRole('combobox', { name: 'Fruit' }).classList,
    );

    expect(tokens).toContain('coarse:min-h-11');
    // Ungated would inflate every dense trigger on a mouse desktop — `min-h`
    // and `h` do not conflict in tailwind-merge, so an ungated floor survives
    // a call site's `h-8` there too. Desktop density is a stated constraint.
    expect(tokens).not.toContain('min-h-11');
    // Width-gated is the bug being fixed: a ~1130px touch tablet in landscape
    // is above `md` and would keep the 40px trigger.
    expect(tokens).not.toContain('md:min-h-11');
    // A floor, not a cap: `h-10` may be overridden, but nothing here may
    // clamp the trigger below the floor once the pointer is coarse.
    expect(tokens.some((t) => /^(coarse:)?max-h-/.test(t))).toBe(false);
  });

  it('keeps the floor when a call site sets its own height', () => {
    // The whole reason the token is `min-h` rather than a gated `h`: the two
    // are separate tailwind-merge conflict groups, so the primitive's floor
    // survives the caller's density class instead of being merged away, and
    // CSS clamps the used height up. This is what lets ~19 Select call sites
    // stay untouched. `h-8` is the densest one in the app (PaginationBar).
    render(
      <Select>
        <SelectTrigger aria-label="Fruit" className="h-8 w-[70px]">
          <SelectValue placeholder="Pick one" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="apple">Apple</SelectItem>
        </SelectContent>
      </Select>,
    );

    const tokens = Array.from(
      screen.getByRole('combobox', { name: 'Fruit' }).classList,
    );
    expect(tokens).toContain('h-8');
    expect(tokens).toContain('coarse:min-h-11');
    // ...and the caller's height really did replace the primitive's, so the
    // assertion above is not passing on a trigger that simply kept both.
    expect(tokens).not.toContain('h-10');
  });

  it('still registers the coarse variant the floor depends on', () => {
    // The wiring seam, and the one mutant no render test can see: with the
    // plugin removed from `tailwind.config.ts` the token above still appears
    // in the DOM, still passes every assertion here, and generates no CSS
    // whatsoever — the floor would simply not exist in the built app.
    //
    // Pinned as source text rather than by importing the config: the config
    // is outside `tsconfig.app.json`'s program and uses `require()`, so
    // importing it for its value would drag it into the type-check.
    expect(tailwindConfigSource).toContain(
      "addVariant('coarse', '@media (pointer: coarse)')",
    );
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
