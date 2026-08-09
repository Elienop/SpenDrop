import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { PaginationBar, type PaginationBarProps } from './PaginationBar';

/**
 * The pager is shared infrastructure — Transactions and Trash both render it —
 * so a regression here is a two-page regression. `getPageNumbers` is not
 * exported (a component module that also exports helpers breaks Fast Refresh),
 * which is fine: driving it through the rendered output tests the thing the
 * user actually gets rather than the function's return value.
 */
function renderBar(props: Partial<PaginationBarProps> = {}) {
  return render(
    <PaginationBar
      page={1}
      totalPages={1}
      perPage={20}
      onPageChange={vi.fn()}
      onPerPageChange={vi.fn()}
      {...props}
    />,
  );
}

/**
 * The page-nav cluster in DOM order, with ellipses as '…'.
 *
 * Reading the sequence rather than a set: the whole point of the ellipsis
 * logic is WHERE the gaps fall, and a set-based assertion would pass on a
 * pager that rendered the right numbers in the wrong order or dropped a gap.
 */
function pageSequence(): string[] {
  const prev = screen.getByRole('button', { name: 'Go to previous page' });
  const cluster = prev.parentElement!;
  return Array.from(cluster.children)
    .map((el) => el.textContent?.trim() ?? '')
    .filter((t) => /^\d+$/.test(t) || t === '...')
    .map((t) => (t === '...' ? '…' : t));
}

describe('PaginationBar page numbers', () => {
  // At or below 7 pages every page is listed — no ellipsis logic runs at all.
  it('lists every page up to the 7-page threshold', () => {
    renderBar({ page: 4, totalPages: 7 });
    expect(pageSequence()).toEqual(['1', '2', '3', '4', '5', '6', '7']);
  });

  // 8 is the first total that elides, and the boundary is where an off-by-one
  // would live.
  it('starts eliding at 8 pages', () => {
    const { unmount } = renderBar({ page: 1, totalPages: 8 });
    expect(pageSequence()).toEqual(['1', '2', '…', '8']);
    unmount();

    renderBar({ page: 8, totalPages: 8 });
    expect(pageSequence()).toEqual(['1', '…', '7', '8']);
  });

  it('keeps first, last and a window around the current page', () => {
    renderBar({ page: 5, totalPages: 10 });
    expect(pageSequence()).toEqual(['1', '…', '4', '5', '6', '…', '10']);
  });

  it('drops the leading gap near the start and the trailing gap near the end', () => {
    const { unmount } = renderBar({ page: 3, totalPages: 10 });
    expect(pageSequence()).toEqual(['1', '2', '3', '4', '…', '10']);
    unmount();

    renderBar({ page: 8, totalPages: 10 });
    expect(pageSequence()).toEqual(['1', '…', '7', '8', '9', '10']);
  });

  // Both ends are mirror images. Verified exhaustively out-of-band across
  // totals 8, 10 and 25 — zero asymmetric pages — and pinned here at the two
  // ends plus the middle, which is where a one-sided edit would show up first.
  it('treats the two ends symmetrically', () => {
    const total = 25;
    for (const [near, far] of [
      [1, 25],
      [2, 24],
      [4, 22],
      [12, 14],
    ] as const) {
      const a = render(
        <PaginationBar
          page={near}
          totalPages={total}
          perPage={20}
          onPageChange={vi.fn()}
          onPerPageChange={vi.fn()}
        />,
      );
      const nearSeq = pageSequence();
      a.unmount();

      const b = render(
        <PaginationBar
          page={far}
          totalPages={total}
          perPage={20}
          onPageChange={vi.fn()}
          onPerPageChange={vi.fn()}
        />,
      );
      const farSeq = pageSequence();
      b.unmount();

      const mirrored = [...farSeq]
        .reverse()
        .map((t) => (t === '…' ? '…' : String(total + 1 - Number(t))));
      expect(nearSeq).toEqual(mirrored);
    }
  });

  // The one-page gap. At page 4 the window starts at 3, so the leading gap
  // spans exactly one hidden page (2) and is still drawn as an ellipsis — a
  // "…" occupying the same width as the "2" it replaces. That is a documented
  // cosmetic wart, NOT a bug (no page is unreachable; prev/next always
  // render), and it is symmetric, so the mirror test above cannot see it: a
  // change to the gap threshold drops the ellipsis at BOTH ends and symmetry
  // still holds. Pinned explicitly for that reason — found by mutation.
  it('elides even when the gap is a single page', () => {
    const { unmount } = renderBar({ page: 4, totalPages: 10 });
    expect(pageSequence()).toEqual(['1', '…', '3', '4', '5', '…', '10']);
    unmount();

    // ...and its mirror, page 7, where the trailing gap hides only 9.
    renderBar({ page: 7, totalPages: 10 });
    expect(pageSequence()).toEqual(['1', '…', '6', '7', '8', '…', '10']);
  });

  it('marks the current page for assistive tech', () => {
    renderBar({ page: 5, totalPages: 10 });
    expect(screen.getByRole('button', { name: '5' })).toHaveAttribute(
      'aria-current',
      'page',
    );
    expect(screen.getByRole('button', { name: '4' })).not.toHaveAttribute(
      'aria-current',
    );
  });

  it('navigates to the page that was clicked', async () => {
    const onPageChange = vi.fn();
    const user = userEvent.setup();
    renderBar({ page: 5, totalPages: 10, onPageChange });

    await user.click(screen.getByRole('button', { name: '6' }));
    expect(onPageChange).toHaveBeenCalledWith(6);
  });
});

describe('PaginationBar compact mode', () => {
  // Nine 32px buttons plus the rows-per-page control need ~440px; a 390px
  // viewport has 358px.
  it('replaces the numbered buttons with a readout', () => {
    renderBar({ page: 3, totalPages: 10, compact: true });

    expect(screen.getByText('Page 3 of 10')).toBeInTheDocument();
    expect(pageSequence()).toEqual([]);
    expect(screen.queryByRole('button', { name: '3' })).not.toBeInTheDocument();
  });

  it('keeps prev and next, which are the only way to move in compact mode', () => {
    renderBar({ page: 3, totalPages: 10, compact: true });
    expect(
      screen.getByRole('button', { name: 'Go to previous page' }),
    ).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Go to next page' })).toBeEnabled();
  });

  // tabular-nums so the readout does not reflow as the page number gains a
  // digit, which on a phone shifts the arrows out from under the thumb.
  it('sets the readout in tabular figures', () => {
    renderBar({ page: 3, totalPages: 10, compact: true });
    expect(screen.getByText('Page 3 of 10')).toHaveClass(
      'tabular-nums',
      'font-medium',
    );
  });

  it('renders the numbered buttons when not compact', () => {
    renderBar({ page: 3, totalPages: 10 });
    expect(screen.queryByText(/^Page \d+ of \d+$/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '3' })).toBeInTheDocument();
  });
});

/**
 * WHAT THESE CAN AND CANNOT PROVE. No test in this repo measures a pixel for
 * the touch floor: happy-dom runs no layout, and the Tailwind stylesheet is
 * never loaded in tests (`globals.css` is imported only by `main.tsx`, which
 * no test renders). A class token is in the DOM at every pointer state, so
 * asserting that `coarse:min-h-11` is present proves the wiring, never the
 * 44px. The pixels are the browser check's job.
 *
 * The wiring is worth pinning anyway, because the bar's failure mode is
 * RAGGEDNESS rather than smallness — one control on a different gate from its
 * neighbours. It used to read `size-11 md:size-8`, a WIDTH gate, while the
 * trigger's floor comes from `SelectTrigger` and is a POINTER gate: on the
 * household's ~1130px tablet in landscape that mix would put one 44px Select
 * beside six 32px buttons in a single flex row. So each assertion below
 * carries the negative half too — the retired width tokens must be gone, not
 * merely joined by the new ones.
 *
 * WHERE THE TOKENS COME FROM, since the answer changed and the file reads
 * wrongly otherwise: not one of these controls sets a floor of its own any
 * more. The trigger's is `SelectTrigger`'s, and the six buttons' is `Button`'s
 * (`size="icon"`, which is what adds the width half) — the local
 * `TOUCH_TARGET_SQUARE` they used to carry was exactly redundant and is gone.
 * That weakens "the token is present" here, because it is now present on every
 * button in the app; it does NOT weaken the two assertions that matter at this
 * level, which are that the retired width idiom is absent and that the trigger
 * and its neighbours end up on the SAME gate. The ellipsis is the one control
 * that still floors itself: a `<span>` no primitive can size.
 */
describe('PaginationBar touch targets', () => {
  // Every square control in the bar, by accessible name. The ellipsis is not
  // in here (it is aria-hidden and unnamed) and is covered separately.
  const SQUARE_CONTROLS = [
    'Go to previous page',
    'Go to next page',
    'Go to first page',
    'Go to last page',
    '3',
  ];

  it('floors every square control on both axes when the pointer is coarse', () => {
    renderBar({ page: 2, totalPages: 5 });
    for (const name of SQUARE_CONTROLS) {
      const button = screen.getByRole('button', { name });
      // Both axes: a height-only floor leaves a 32px-wide button that is
      // merely taller, which is still a miss for a thumb. The width half is
      // the load-bearing one to assert here — it comes from `size="icon"`, so
      // this fails if the pager stops declaring these as icon buttons, whereas
      // the height half arrives from Button's base on any Button at all.
      expect(button).toHaveClass('coarse:min-h-11', 'coarse:min-w-11');
    }
  });

  it('leaves the fine-pointer density at 32px, unchanged at every width', () => {
    renderBar({ page: 2, totalPages: 5 });
    for (const name of SQUARE_CONTROLS) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveClass('size-8');
      // The retired width idiom. `size-11` is what made the desktop pager
      // inflate below `md`; `md:size-8` is what left the touch tablet at 32px.
      expect(button).not.toHaveClass('size-11');
      expect(button).not.toHaveClass('md:size-8');
    }
  });

  it('keeps the first/last jumps lg-only while flooring them', () => {
    renderBar({ page: 2, totalPages: 5 });
    for (const name of ['Go to first page', 'Go to last page']) {
      const button = screen.getByRole('button', { name });
      // `lg:` here is a genuine room question — two extra buttons do not fit —
      // and is deliberately NOT converted to a pointer gate.
      expect(button).toHaveClass('hidden', 'lg:flex');
    }
  });

  it('gives the ellipsis spacer the same box as the buttons it sits between', () => {
    // It is aria-hidden decoration, but it holds a slot in the same flex row:
    // sized differently it staggers the numbers on one side of the gap.
    renderBar({ page: 4, totalPages: 10 });
    // Two gaps at page 4 of 10 — both spacers, so assert on both rather than
    // picking one and hoping the other matches.
    const spacers = screen.getAllByText('...');
    expect(spacers).toHaveLength(2);
    for (const spacer of spacers) {
      expect(spacer).toHaveClass(
        'size-8',
        'coarse:min-h-11',
        'coarse:min-w-11',
      );
      expect(spacer).not.toHaveClass('md:size-8');
    }
  });

  it('takes both floors from the primitives and adds only density', () => {
    // Neither call site sets a floor of its own — `SelectTrigger` and `Button`
    // carry `coarse:min-h-11`, and it survives the `h-8`/`size-8` here because
    // `min-h`/`min-w` and `h`/`w` are separate tailwind-merge conflict groups.
    // If a future edit re-fixes the height with a gated `coarse:h-*` the token
    // vanishes and this fails.
    renderBar();
    const trigger = screen.getByRole('combobox', { name: 'Rows per page' });
    expect(trigger).toHaveClass('h-8', 'coarse:min-h-11');
    expect(trigger).not.toHaveClass('h-11');
    expect(trigger).not.toHaveClass('md:h-8');

    const button = screen.getByRole('button', { name: 'Go to next page' });
    expect(button).toHaveClass('size-8', 'coarse:min-h-11');
    // The density class really did replace the icon variant's 40px box, so the
    // line above is not passing on a button that simply kept both.
    expect(button).not.toHaveClass('h-10', 'w-10');
  });

  it('puts the trigger and its neighbours on the same gate', () => {
    // The point of the migration, stated as one assertion: on a coarse pointer
    // both reach 44px, and on a fine one both stay 32px. A mixed pair is the
    // 12px step this replaced, and it is invisible to the per-control tests
    // above because each of them passes on its own.
    renderBar({ page: 2, totalPages: 5 });
    const trigger = screen.getByRole('combobox', { name: 'Rows per page' });
    const button = screen.getByRole('button', { name: 'Go to next page' });

    expect(trigger).toHaveClass('coarse:min-h-11');
    expect(button).toHaveClass('coarse:min-h-11');
    expect(Array.from(trigger.classList).some((t) => t.startsWith('md:'))).toBe(
      false,
    );
    expect(Array.from(button.classList).some((t) => t.startsWith('md:'))).toBe(
      false,
    );
  });
});

describe('PaginationBar rows per page', () => {
  // The visible "Rows per page" text is a <p>, not a <label>, so it names
  // nothing programmatically — without the aria-label the trigger announces
  // only its value.
  it('gives the trigger an accessible name of its own', () => {
    renderBar();
    const trigger = screen.getByRole('combobox', { name: 'Rows per page' });
    expect(trigger).toHaveAccessibleName('Rows per page');
    expect(trigger).toHaveTextContent('20');
  });

  it('reports the chosen size', async () => {
    const onPerPageChange = vi.fn();
    const user = userEvent.setup();
    renderBar({ onPerPageChange });

    await user.click(screen.getByRole('combobox', { name: 'Rows per page' }));
    await user.click(screen.getByRole('option', { name: '50' }));

    expect(onPerPageChange).toHaveBeenCalledWith(50);
  });

  it('accepts a caller-supplied page-size menu', async () => {
    const user = userEvent.setup();
    renderBar({ perPage: 5, pageSizes: [5, 15] });

    await user.click(screen.getByRole('combobox', { name: 'Rows per page' }));
    expect(screen.getByRole('option', { name: '5' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '15' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: '100' })).not.toBeInTheDocument();
  });
});

describe('PaginationBar leadingActions', () => {
  // Trash hangs its whole-trash bulk actions here rather than above the card.
  it('renders caller content beside the rows-per-page cluster', () => {
    renderBar({
      leadingActions: <button type="button">Purge all</button>,
    });

    const action = screen.getByRole('button', { name: 'Purge all' });
    expect(action).toBeInTheDocument();
    // Beside the rows-per-page control, not adrift in the nav cluster.
    const cluster = screen.getByRole('combobox', {
      name: 'Rows per page',
    }).parentElement!.parentElement!;
    expect(within(cluster).getByRole('button', { name: 'Purge all' })).toBe(
      action,
    );
  });

  it('omits the slot entirely when the caller passes nothing', () => {
    renderBar();
    expect(
      screen.queryByRole('button', { name: 'Purge all' }),
    ).not.toBeInTheDocument();
  });
});

describe('PaginationBar boundaries', () => {
  it('disables backwards navigation on the first page', () => {
    renderBar({ page: 1, totalPages: 10 });
    expect(screen.getByRole('button', { name: 'Go to first page' })).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Go to previous page' }),
    ).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Go to next page' })).toBeEnabled();
  });

  it('disables forwards navigation on the last page', () => {
    renderBar({ page: 10, totalPages: 10 });
    expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Go to last page' })).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Go to previous page' }),
    ).toBeEnabled();
  });

  it('disables both directions when there is a single page', () => {
    renderBar({ page: 1, totalPages: 1 });
    expect(
      screen.getByRole('button', { name: 'Go to previous page' }),
    ).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled();
  });

  it('disables the boundaries in compact mode too', () => {
    renderBar({ page: 1, totalPages: 5, compact: true });
    expect(
      screen.getByRole('button', { name: 'Go to previous page' }),
    ).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Go to next page' })).toBeEnabled();
  });
});
