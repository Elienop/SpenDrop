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

describe('PaginationBar touch targets', () => {
  // A pager is mostly small icon buttons packed side by side, which is the
  // worst shape for a thumb — and unlike a checkbox nothing here has a visible
  // size that must stay put, so the box grows rather than just the hit area.
  // Both halves of the swap are pinned: `md:size-8` alone would leave the
  // phone at 32px, and `size-11` alone would inflate the desktop pager.
  it('sizes prev/next for a thumb on a phone and for density above md', () => {
    renderBar({ page: 2, totalPages: 5 });
    for (const name of ['Go to previous page', 'Go to next page']) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveClass('size-11', 'md:size-8');
      expect(button).not.toHaveClass('size-8');
    }
  });

  it('sizes the numbered buttons the same way', () => {
    renderBar({ page: 2, totalPages: 5 });
    const button = screen.getByRole('button', { name: '3' });
    expect(button).toHaveClass('size-11', 'md:size-8');
    expect(button).not.toHaveClass('size-8');
  });

  it('sizes the first/last jumps the same way, while keeping them lg-only', () => {
    renderBar({ page: 2, totalPages: 5 });
    for (const name of ['Go to first page', 'Go to last page']) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveClass('size-11', 'md:size-8', 'hidden', 'lg:flex');
    }
  });

  it('sizes the rows-per-page trigger the same way', () => {
    renderBar();
    expect(screen.getByRole('combobox', { name: 'Rows per page' })).toHaveClass(
      'h-11',
      'md:h-8',
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
