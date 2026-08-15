import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ModeToggle } from './ModeToggle';
import { ThemeProviderContext } from '@/hooks/useTheme';

// B47: the Moon glyph is `absolute`, so its containing block is the nearest
// POSITIONED ancestor. `ui/button.tsx` establishes none, so before the fix that
// ancestor was whatever the placement happened to supply — in the mobile drawer,
// `SheetContent` — and the glyph escaped its own button's box (visible as an
// icon that does not scroll with the button it belongs to).
//
// happy-dom applies no stylesheets, so these assertions are derived from the
// rendered class lists rather than from layout: the question "which element is
// this icon positioned against?" is answered entirely by which ancestor carries
// a Tailwind position token, and that is observable in the DOM.

const POSITION_TOKENS = new Set([
  'fixed',
  'absolute',
  'relative',
  'sticky',
]);

/** Class tokens of any element — `.className` is an SVGAnimatedString on <svg>. */
function classTokens(el: Element): string[] {
  return (el.getAttribute('class') ?? '').split(/\s+/).filter(Boolean);
}

/** The element an `absolute` descendant of `el` would actually resolve against. */
function containingBlockOf(el: Element): Element | null {
  let cur = el.parentElement;
  while (cur) {
    if (classTokens(cur).some((t) => POSITION_TOKENS.has(t))) return cur;
    cur = cur.parentElement;
  }
  return null;
}

function renderToggle(props: { className?: string } = {}, wrapper?: string) {
  const toggle = (
    <ThemeProviderContext.Provider
      value={{
        theme: 'dark',
        setTheme: () => {},
        colorTheme: null,
        setColorTheme: () => {},
      }}
    >
      <ModeToggle {...props} />
    </ThemeProviderContext.Provider>
  );
  return render(
    wrapper === undefined ? toggle : <div className={wrapper}>{toggle}</div>,
  );
}

function trigger(): HTMLElement {
  return screen.getByRole('button', { name: 'Toggle theme' });
}

/** The absolutely-positioned glyph, found BY its position class. */
function absoluteGlyph(within: HTMLElement): Element {
  const el = within.querySelector('svg.absolute');
  // Not a mere null-check for convenience: if the Moon ever loses `absolute`,
  // every "it is positioned against its own button" assertion below would be
  // trivially satisfiable, so the suite must fail here instead of going quiet.
  expect(el, 'the Moon glyph should still be absolutely positioned').not.toBeNull();
  return el as Element;
}

describe('ModeToggle glyph positioning (B47)', () => {
  test('the trigger establishes a containing block', () => {
    renderToggle();
    expect(classTokens(trigger())).toContain('relative');
  });

  test('the absolute glyph resolves against its own button', () => {
    renderToggle();
    const button = trigger();
    expect(containingBlockOf(absoluteGlyph(button))).toBe(button);
  });

  // The bug as reported: a positioned ancestor supplied by the PLACEMENT
  // (SheetContent in the mobile drawer) must not capture the glyph.
  test('a positioned ancestor outside the button does not capture the glyph', () => {
    renderToggle({}, 'relative overflow-y-auto');
    const button = trigger();
    expect(containingBlockOf(absoluteGlyph(button))).toBe(button);
  });

  // MobileNav passes `size-11` for the 44px touch floor. `relative` belongs to
  // the position group and `size-*` to the width/height groups, so tailwind-merge
  // must keep both — a pin, because losing either is silent.
  test('keeps a call-site size class alongside the position class', () => {
    renderToggle({ className: 'size-11' });
    const tokens = classTokens(trigger());
    expect(tokens).toContain('relative');
    expect(tokens).toContain('size-11');
  });
});
