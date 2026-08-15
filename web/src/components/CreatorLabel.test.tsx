import { describe, expect, test } from 'vitest';
import { render, screen } from '@testing-library/react';
import { CreatorLabel } from './CreatorLabel';

function line(): HTMLElement {
  const label = screen.getByText('Entered by');
  const p = label.closest('p');
  expect(p).not.toBeNull();
  return p as HTMLElement;
}

// B36. `created_by` is a display name read through a live JOIN, and a member
// can PATCH their own display name to the admin's exact string — which
// retroactively relabels every row they have ever entered. The handle is the
// half of the attribution they cannot collide, so the two travel together.
//
// These are the shared component's own tests. Every surface that names a
// creator renders THIS, so the suppression rules are proved once here; the
// per-surface tests prove only that the surface passes both halves through.
describe('CreatorLabel', () => {
  test('names the creator by display name AND login handle', () => {
    render(<CreatorLabel createdBy="Elie" createdByUsername="elienop" />);

    expect(screen.getByText('Elie')).toBeInTheDocument();
    expect(screen.getByText('@elienop')).toBeInTheDocument();
  });

  test('separates the two with a real space, not a margin', () => {
    // Exact, not a substring: margins do not separate words for a screen
    // reader, so an `ml-*` gap in place of this space would announce
    // "Elie@elienop" — one token, and one that reads as an email address.
    // Only an exact match can tell the two apart.
    render(<CreatorLabel createdBy="Elie" createdByUsername="elienop" />);

    expect(line().textContent).toBe('Entered by Elie @elienop');
  });

  test('renders no handle at all when the username is empty', () => {
    // "" is the wire's orphaned-creator value on this half. A bare `@` with
    // nothing after it is a bug, so the whole span is suppressed — asserted on
    // the line's full text, because "no `@` anywhere" is the actual rule.
    render(<CreatorLabel createdBy="Elie" createdByUsername="" />);

    expect(screen.getByText('Elie')).toBeInTheDocument();
    expect(line().textContent).toBe('Entered by Elie');
    expect(line().textContent).not.toContain('@');
  });

  test('falls back to Unknown, with no handle, when the display name is empty', () => {
    // The handle is deliberately NON-empty here. On the wire both halves come
    // from the same LEFT JOIN and empty together, so this pair cannot occur —
    // which is exactly why it is worth pinning: "Unknown @elienop" names a
    // person the line has just said it cannot name. The suppression has to be
    // gated on BOTH halves, not on the username alone.
    render(<CreatorLabel createdBy="" createdByUsername="elienop" />);

    expect(screen.getByText('Unknown')).toBeInTheDocument();
    expect(line().textContent).toBe('Entered by Unknown');
    expect(line().textContent).not.toContain('@');
  });

  test('falls back to Unknown, with no handle, when the whole creator row is gone', () => {
    // The real wire shape of an orphaned row: the LEFT JOIN found nothing, so
    // both halves are "". Never a blank line, never a bare `@`.
    render(<CreatorLabel createdBy="" createdByUsername="" />);

    expect(screen.getByText('Unknown')).toBeInTheDocument();
    expect(line().textContent).toBe('Entered by Unknown');
    expect(line().textContent).not.toContain('@');
  });

  // Off-type, on purpose. The wire contract makes both fields REQUIRED, so
  // `undefined` cannot arrive from a well-typed producer — but it reaches this
  // component the moment one is badly typed: an un-annotated test fixture, a
  // hand-built mock, an endpoint that has not been regenerated. TypeScript
  // stops none of those at THIS boundary, because the hole is upstream.
  //
  // `@ts-expect-error` rather than a cast: a cast would quietly launder the
  // violation, whereas this asserts the call does not type-check and FAILS the
  // build if it ever starts to — which would mean somebody made the field
  // optional, the one thing `types.ts` says not to do. It is the honest way to
  // say "this input is illegal and the component survives it anyway".
  test('suppresses the handle when the username is undefined, not just empty', () => {
    render(
      <CreatorLabel
        createdBy="Elie"
        // @ts-expect-error deliberately off-type: the wire contract forbids
        // undefined here, and this pins that a producer violating it still
        // cannot render the string "@undefined" at a household member.
        createdByUsername={undefined}
      />,
    );

    expect(line().textContent).toBe('Entered by Elie');
    expect(line().textContent).not.toContain('@');
    expect(line().textContent).not.toContain('undefined');
  });

  test('suppresses the handle when the display name is undefined, not just empty', () => {
    render(
      <CreatorLabel
        // @ts-expect-error deliberately off-type: pins that the Unknown
        // fallback and the handle gate agree, so this cannot render
        // "Unknown @elienop" — a line naming who it just said it cannot name.
        createdBy={undefined}
        createdByUsername="elienop"
      />,
    );

    expect(screen.getByText('Unknown')).toBeInTheDocument();
    expect(line().textContent).toBe('Entered by Unknown');
    expect(line().textContent).not.toContain('@');
  });

  test('keeps the metadata register the six surfaces relied on', () => {
    // Byte-exact class pins, because this component was extracted from six
    // copies of this exact markup and the extraction is only lossless if the
    // classes came across unchanged. `toHaveClass` would pass on a superset
    // and hide an added utility.
    render(<CreatorLabel createdBy="Elie" createdByUsername="elienop" />);

    expect(screen.getByText('Entered by')).toHaveClass('sr-only');
    expect(line()).toHaveAttribute(
      'class',
      'mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground',
    );

    const icon = line().querySelector('svg');
    expect(icon).toHaveAttribute('aria-hidden', 'true');
    expect(icon).toHaveClass('size-3.5', 'shrink-0');
  });

  // The clipping ORDER is the reason this component has two spans instead of
  // one, and it is the opposite of what the obvious markup does. With both
  // strings inside a single truncating span, the tail ellipsis eats `@elienop`
  // FIRST and leaves the spoofable display name standing alone — which is the
  // precise failure the handle exists to prevent. MaxDisplayNameLength is 64
  // and the phone card is 360px, so that is the common case, not a corner.
  //
  // jsdom does no layout, so none of this can be asserted by measuring. It is
  // derived from the rendered classes instead: which element carries the
  // shrink, which carries the floor, and which contains which.
  test('gives the display name the shrink and keeps the handle whole', () => {
    render(<CreatorLabel createdBy="Elie" createdByUsername="elienop" />);

    const nameSpan = screen.getByText('Entered by').parentElement;
    const handleSpan = screen.getByText('@elienop');

    // The NAME is the flex item that absorbs the shortfall.
    expect(nameSpan).toHaveAttribute('class', 'min-w-0 truncate');

    // The handle is a SIBLING, not a descendant. Inside the name's span it
    // would share the name's ellipsis and disappear first — this containment
    // assertion is the one that fails if the two are merged back.
    expect(nameSpan).not.toContainElement(handleSpan);
    expect(handleSpan.parentElement).toBe(nameSpan!.parentElement);

    // Byte-exact, because three of these four utilities are invisible to every
    // other assertion in this file:
    //   - shrink-0        the handle keeps its width while the name gives ground
    //   - max-w-[50%]     a 32-char username cannot starve the name entirely
    //   - whitespace-pre  renders the leading space; without it textContent is
    //                     IDENTICAL and only this pin notices
    //   - overflow-hidden + text-ellipsis rather than `truncate`, which would
    //     also set white-space and leave the pair resolved by Tailwind's
    //     utility source order
    expect(handleSpan).toHaveAttribute(
      'class',
      'max-w-[50%] shrink-0 overflow-hidden text-ellipsis whitespace-pre',
    );

    // The percentage cap needs a definite basis to resolve against; a
    // content-sized container makes it cyclic and clips the handle even when
    // there is room. `flex-1` is what makes it definite.
    expect(nameSpan!.parentElement).toHaveAttribute(
      'class',
      'flex min-w-0 flex-1 items-center',
    );
  });

  test('offers the untruncated pair as a tooltip', () => {
    // Mirrors the description's own `title` one line up in the desktop
    // max-w-md cells. Dead on touch, which is why it is a bonus and not the
    // fix — the clipping order above is the fix.
    const { unmount } = render(
      <CreatorLabel createdBy="Elie" createdByUsername="elienop" />,
    );
    expect(line()).toHaveAttribute('title', 'Elie @elienop');
    unmount();

    // No dangling `@` in the tooltip either, on either suppression branch.
    const { unmount: unmount2 } = render(
      <CreatorLabel createdBy="Elie" createdByUsername="" />,
    );
    expect(line()).toHaveAttribute('title', 'Elie');
    unmount2();

    render(<CreatorLabel createdBy="" createdByUsername="elienop" />);
    expect(line()).toHaveAttribute('title', 'Unknown');
  });
});
