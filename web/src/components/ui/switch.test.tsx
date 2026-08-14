import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Switch } from './switch';
import tailwindConfigSource from '../../../tailwind.config.ts?raw';

/**
 * WHAT THESE CAN AND CANNOT PROVE — same register as `button.test.tsx`.
 * happy-dom runs no layout and never loads the Tailwind stylesheet, so
 * nothing here measures the 44px, and the pseudo-element cannot be clicked in
 * a test at all (a `::before` has no DOM node). These are WIRING pins; the
 * pixel proof — that a tap 8px above the pill actually toggles it — is the
 * browser pass's job, on the technique PasswordInput's eye already proved
 * there (its 32px box measures a 44px hit).
 *
 * What they DO pin: that Switch takes the "grow only the hit area" lever
 * rather than the box-growing floor (the 24px pill and its thumb travel are
 * the control's visual grammar — see the comment in `switch.tsx`), that every
 * piece of the pseudo-element is present and coarse-gated (drop any one of
 * anchor/extent/content and the hit area silently collapses while every
 * render stays identical), and that the variant the gate depends on is still
 * registered.
 */
describe('Switch touch target', () => {
  test('keeps the 24px visual box and takes the target from the hit area instead', () => {
    render(<Switch aria-label="Push notifications" />);
    const control = screen.getByRole('switch', { name: 'Push notifications' });

    // The visual stays the design system's 24x44 pill at every pointer...
    expect(control).toHaveClass('h-6', 'w-11');
    // ...so the box-growing lever must be absent: a `coarse:min-h-11` here
    // would stretch the pill and misalign it against its label rows.
    expect(control).not.toHaveClass('coarse:min-h-11');
    expect(control).not.toHaveClass('min-h-11');
  });

  test('the coarse-gated pseudo-element carries the 44px, every piece present', () => {
    render(<Switch aria-label="Push notifications" />);
    const control = screen.getByRole('switch', { name: 'Push notifications' });

    // `relative` anchors the pseudo to the pill, not to some ancestor.
    expect(control).toHaveClass('relative');
    // 24 + 2x10 = 44 on the y-axis; `inset-x-0` spans the already-44px width
    // — without it left/right stay `auto` and an empty absolute pseudo
    // collapses to zero width, a sliver that catches no tap.
    expect(control).toHaveClass('coarse:before:absolute');
    expect(control).toHaveClass('coarse:before:inset-x-0');
    expect(control).toHaveClass('coarse:before:-inset-y-2.5');
    expect(control).toHaveClass("coarse:before:content-['']");
  });

  test('the thumb geometry is untouched', () => {
    // The travel distance is derived from the h-6 w-11 box; these tokens are
    // what "keep the visual" means in practice. If a future change grows the
    // box instead of the hit area, this is the pin that names the contract.
    render(<Switch aria-label="Push notifications" />);
    const thumb = screen
      .getByRole('switch', { name: 'Push notifications' })
      .querySelector('span');

    expect(thumb).not.toBeNull();
    expect(thumb).toHaveClass(
      'h-5',
      'w-5',
      'data-[state=checked]:translate-x-5',
    );
  });

  test('still registers the coarse variant the hit area depends on', () => {
    // Duplicated from `button.test.tsx` on purpose — Switch's hit area is
    // dead without this line whether or not anyone keeps the other pins.
    expect(tailwindConfigSource).toContain(
      "addVariant('coarse', '@media (pointer: coarse)')",
    );
  });
});
