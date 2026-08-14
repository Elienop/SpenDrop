import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Button } from './button';
import { PasswordInput } from './password-input';
import { TOUCH_TARGET_SQUARE } from '@/lib/touch-target';
import tailwindConfigSource from '../../../tailwind.config.ts?raw';

/**
 * WHAT THESE CAN AND CANNOT PROVE. Nothing here measures a pixel, and no
 * assertion below should be read as if it did: happy-dom runs no layout, and
 * the Tailwind stylesheet is never loaded in tests (`globals.css` is imported
 * only by `main.tsx`, which no test renders). A class token is in the DOM at
 * every pointer state, so `toHaveClass('coarse:min-h-11')` is true on a mouse
 * and on a finger alike — asserting "the gate flips" here would be asserting a
 * string. The pixels are the browser check's job, at 360/720/1130/1440 in both
 * pointer states.
 *
 * What they DO pin is the wiring, which is where this mechanism fails silently:
 * that the floor lives on the shared primitive rather than at N call sites,
 * that `size="icon"` gets the WIDTH half as well (a height-only floor is a
 * 44x32 rectangle and reads as fixed while still missing the thumb), that it is
 * the pointer-gated token rather than either of the two rejected variants, that
 * tailwind-merge lets it coexist with a caller's own height and lets a caller
 * deliberately drop it, and that the variant it depends on is still registered.
 *
 * A note on what would make these vacuous. Every `<Button>` in the app now
 * carries `coarse:min-h-11`, so ANY assertion of the shape "this button has a
 * 44px floor" passes everywhere from here on, including on a control nobody
 * floored. That is why the assertions below come in pairs — the positive token
 * plus the specific wrong answer that must be absent — and why the two
 * consumer-level tests that used to prove a real local floor
 * (`Trash.test.tsx`, `PaginationBar.test.tsx`) were rewritten rather than left
 * to keep passing.
 */
describe('Button touch floor', () => {
  test('every size carries the pointer-gated 44px height floor', () => {
    render(
      <>
        <Button>default</Button>
        <Button size="sm">sm</Button>
        <Button size="lg">lg</Button>
        <Button size="icon" aria-label="icon" />
      </>,
    );

    for (const name of ['default', 'sm', 'lg', 'icon']) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveClass('coarse:min-h-11');
      // Ungated is the variant that was rejected: `min-h` and `h` are separate
      // tailwind-merge groups, so an ungated floor survives a call site's `h-8`
      // on a mouse desktop too and inflates every dense toolbar there. Desktop
      // density is a stated owner constraint, not a preference.
      expect(button).not.toHaveClass('min-h-11');
      // Width-gated is the bug this fixes: the household's tablet is ~1130px in
      // landscape, above `md`, and would keep the 32-40px desktop sizes.
      expect(button).not.toHaveClass('md:min-h-11');
    }
  });

  test('the icon size floors the width too, and it is the only one that does', () => {
    // The asymmetry is the point. Width is meaningless on a text button (its
    // padding already exceeds 44px) and load-bearing on a square one, so it
    // rides the `icon` variant rather than the base — and a button that is
    // square by className instead of by `size` gets only the height half and
    // has to say `TOUCH_TARGET_SQUARE` itself. Sidebar's collapsed Log out is
    // the one such site; this pins WHY it needs a class that looks redundant.
    render(
      <>
        <Button size="icon" aria-label="icon" />
        <Button size="sm">sm</Button>
      </>,
    );

    expect(screen.getByRole('button', { name: 'icon' })).toHaveClass(
      'coarse:min-w-11',
    );
    expect(screen.getByRole('button', { name: 'sm' })).not.toHaveClass(
      'coarse:min-w-11',
    );
  });

  test('the floor survives a call site fixing its own height', () => {
    // The whole reason the token is `min-h` and not a gated `h`: the two are
    // separate tailwind-merge conflict groups, so the primitive's floor is not
    // merged away by the density class and CSS clamps the used height up. This
    // is what lets ~90 Button call sites stay untouched. `h-8` is the densest
    // in the app; `size-8` is the densest square.
    render(
      <>
        <Button size="sm" className="h-8">
          dense
        </Button>
        <Button size="icon" className="size-8" aria-label="square" />
      </>,
    );

    const dense = screen.getByRole('button', { name: 'dense' });
    expect(dense).toHaveClass('h-8', 'coarse:min-h-11');
    // ...and the caller's height really did replace the variant's, so the
    // assertion above is not passing on a button that simply kept both.
    expect(dense).not.toHaveClass('h-9');

    const square = screen.getByRole('button', { name: 'square' });
    expect(square).toHaveClass('size-8', 'coarse:min-h-11', 'coarse:min-w-11');
    expect(square).not.toHaveClass('h-10');
  });

  test('a call site can drop the floor, and only the same-modifier token does it', () => {
    // The opt-out has to be reachable or the primitive is a wall — PasswordInput
    // needs it. It also has to be the `coarse:`-prefixed form: tailwind-merge
    // keys on group AND modifier, so a bare `min-h-0` would leave both rules
    // standing at equal specificity (a media query adds none) and let
    // stylesheet order pick the winner.
    render(
      <>
        <Button className="coarse:min-h-0 coarse:min-w-0" size="icon" aria-label="opted out" />
        <Button className="min-h-0" size="icon" aria-label="bare" />
      </>,
    );

    const out = screen.getByRole('button', { name: 'opted out' });
    expect(out).not.toHaveClass('coarse:min-h-11');
    expect(out).not.toHaveClass('coarse:min-w-11');

    const bare = screen.getByRole('button', { name: 'bare' });
    expect(bare).toHaveClass('coarse:min-h-11', 'min-h-0');
  });

  test('it is a floor, not a cap', () => {
    // A `max-h` alongside it would make the token decorative: the control could
    // still be clamped below 44px on the pointer the floor exists for.
    render(<Button size="sm">sm</Button>);
    const tokens = Array.from(
      screen.getByRole('button', { name: 'sm' }).classList,
    );
    expect(tokens.some((t) => /^(coarse:)?max-h-/.test(t))).toBe(false);
  });

  test('still registers the coarse variant the floor depends on', () => {
    // The wiring seam, and the one mutant no render test can see: remove the
    // plugin from `tailwind.config.ts` and every token asserted above is still
    // in the DOM, every assertion still passes, and the utility compiles to no
    // CSS whatsoever — the floor would simply not exist in the built app.
    //
    // Pinned as source text rather than by importing the config for its value:
    // the config is outside `tsconfig.app.json`'s program and uses `require()`.
    // Duplicated from `select.test.tsx` on purpose — Button's floor is dead
    // without this line whether or not anyone keeps Select's test.
    expect(tailwindConfigSource).toContain(
      "addVariant('coarse', '@media (pointer: coarse)')",
    );
  });
});

describe('PasswordInput reveal toggle', () => {
  test('keeps its 32px box and takes the target from the hit area instead', () => {
    // The one opt-out in the app, and the reason is geometric rather than
    // stylistic: the button is absolutely positioned inside a 40px Input, so a
    // 44px BOX overhangs the field by 2px top and bottom and paints
    // `hover:bg-accent` and the focus ring outside its rounded border. The
    // pseudo-element carries the 44px instead — 32 + 2x6 = 44 — which is the
    // "grow only the hit area" lever from `@/lib/touch-target`.
    render(<PasswordInput toggleLabel="current password" />);
    const toggle = screen.getByRole('button', { name: 'Show current password' });

    expect(toggle).toHaveClass('h-8', 'w-8');
    expect(toggle).toHaveClass('coarse:min-h-0', 'coarse:min-w-0');
    expect(toggle).not.toHaveClass('coarse:min-h-11');
    expect(toggle).not.toHaveClass('coarse:min-w-11');
    // The inset is what makes the opt-out legitimate rather than a hole. Halve
    // it and this reports the token it found, rather than passing because some
    // other `before:` class is present.
    expect(toggle).toHaveClass("before:absolute", "before:-inset-1.5");
  });
});

describe('touch-target constants', () => {
  test('TOUCH_TARGET_SQUARE is the same pair the icon variant emits', () => {
    // The constant survives for squares no primitive can size — PaginationBar's
    // ellipsis `<span>`, a raw `<button>`, a Button square by className. If the
    // two ever drift, a row of icon buttons and the spacer between them stop
    // agreeing, which is the ragged-row failure the pager already had once.
    render(<Button size="icon" aria-label="icon" />);
    const tokens = Array.from(
      screen.getByRole('button', { name: 'icon' }).classList,
    );
    for (const token of TOUCH_TARGET_SQUARE.split(/\s+/)) {
      expect(tokens).toContain(token);
    }
  });
});
