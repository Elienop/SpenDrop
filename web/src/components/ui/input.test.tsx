import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Input } from './input';
import { PasswordInput } from './password-input';
import tailwindConfigSource from '../../../tailwind.config.ts?raw';

/**
 * WHAT THESE CAN AND CANNOT PROVE — same register as `button.test.tsx`.
 * Nothing here measures a pixel: happy-dom runs no layout and the Tailwind
 * stylesheet is never loaded in tests, so a class token is in the DOM at every
 * pointer state and `toHaveClass('coarse:min-h-11')` is true on a mouse and on
 * a finger alike. These are WIRING pins, not pixel proofs — the pixels are the
 * browser check's job.
 *
 * What they DO pin: that the floor lives on the shared primitive rather than
 * at N call sites (the base `h-10` is 40px, so before this every one of the
 * ~48 fields was 4px under the floor on a coarse pointer), that it is the
 * pointer-gated token rather than either rejected variant, that tailwind-merge
 * lets a caller's own height coexist with it and lets a caller deliberately
 * drop it, and that the variant it depends on is still registered.
 */
describe('Input touch floor', () => {
  test('carries the pointer-gated 44px floor, not either rejected variant', () => {
    render(<Input aria-label="field" />);
    const input = screen.getByRole('textbox', { name: 'field' });

    expect(input).toHaveClass('coarse:min-h-11');
    // Ungated was rejected: `min-h` and `h` are separate tailwind-merge
    // groups, so an ungated floor would survive every call site on a mouse
    // desktop too and inflate every form there. Desktop form density is a
    // stated owner constraint.
    expect(input).not.toHaveClass('min-h-11');
    // Width-gated is the bug the pointer gate fixes: the household's tablet
    // is ~1130px in landscape, above `md`, and would keep the 40px fields.
    expect(input).not.toHaveClass('md:min-h-11');
  });

  test('the floor survives a call site fixing its own height', () => {
    // `min-h` and `h` are separate tailwind-merge conflict groups, so the
    // primitive's floor is not merged away by a caller's density class and
    // CSS clamps the used height up on a coarse pointer.
    render(<Input aria-label="dense" className="h-8" />);
    const input = screen.getByRole('textbox', { name: 'dense' });

    expect(input).toHaveClass('h-8', 'coarse:min-h-11');
    // ...and the caller's height really did replace the primitive's, so the
    // assertion above is not passing on a field that simply kept both.
    expect(input).not.toHaveClass('h-10');
  });

  test('a call site can drop the floor, and only the same-modifier token does it', () => {
    // No call site does this today, but the opt-out has to be reachable or
    // the primitive is a wall. It must be the `coarse:`-prefixed form:
    // tailwind-merge keys on group AND modifier, so a bare `min-h-0` leaves
    // both rules standing at equal specificity (a media query adds none) and
    // lets stylesheet order pick the winner.
    render(
      <>
        <Input aria-label="opted out" className="coarse:min-h-0" />
        <Input aria-label="bare" className="min-h-0" />
      </>,
    );

    expect(
      screen.getByRole('textbox', { name: 'opted out' }),
    ).not.toHaveClass('coarse:min-h-11');
    expect(screen.getByRole('textbox', { name: 'bare' })).toHaveClass(
      'coarse:min-h-11',
      'min-h-0',
    );
  });

  test('it is a floor, not a cap', () => {
    render(<Input aria-label="field" />);
    const tokens = Array.from(
      screen.getByRole('textbox', { name: 'field' }).classList,
    );
    expect(tokens.some((t) => /^(coarse:)?max-h-/.test(t))).toBe(false);
  });

  test('the field inside PasswordInput takes the floor; only its eye opts out', () => {
    // The one opt-out in the app is the EYE BUTTON (its 32px box is
    // load-bearing inside the field — see `password-input.tsx`), and it must
    // not drag the field itself out with it: the input keeps the floor and
    // the eye centers on the field's midline via `top-1/2`, so both heights
    // lay out.
    render(<PasswordInput aria-label="secret" toggleLabel="secret" />);

    expect(screen.getByLabelText('secret')).toHaveClass('coarse:min-h-11');
    expect(
      screen.getByRole('button', { name: 'Show secret' }),
    ).toHaveClass('coarse:min-h-0');
  });

  test('still registers the coarse variant the floor depends on', () => {
    // The wiring seam no render test can see: remove the plugin from
    // `tailwind.config.ts` and every token asserted above is still in the
    // DOM while the utility compiles to no CSS at all. Duplicated from
    // `button.test.tsx` on purpose — Input's floor is dead without this line
    // whether or not anyone keeps Button's test.
    expect(tailwindConfigSource).toContain(
      "addVariant('coarse', '@media (pointer: coarse)')",
    );
  });
});
