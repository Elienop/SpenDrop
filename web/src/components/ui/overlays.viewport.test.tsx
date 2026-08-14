import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';

import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from './alert-dialog';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from './dialog';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from './sheet';

/**
 * A phone in landscape is ~390px tall, and these three overlays used to have
 * no height bound at all: content taller than the viewport overflowed a
 * vertically centred box, putting the title AND the primary button off-screen
 * at the same time. happy-dom does not lay anything out, so what is pinned
 * here is the structure that produces the bound — the height cap, the fact
 * that the body (not the whole box) is the scroll container, and which side of
 * that container the close affordance sits on. The rendered result at 390px is
 * a browser check.
 *
 * The last block covers the other half of a phone-sized overlay: whether the
 * close affordance can actually be hit with a thumb.
 */

/** The scroll container a rendered child sits inside, if any. */
function scrollerAround(el: HTMLElement): HTMLElement | null {
  return el.closest<HTMLElement>('.overflow-y-auto');
}

/** Tailwind spacing token to px — 0.25rem steps against a 16px root. */
function spacingPx(token: string): number {
  const steps = Number(token.replace(/^-?(?:p|m|h|w|size)-/, ''));
  if (!Number.isFinite(steps)) {
    throw new Error(`unparseable spacing token: ${token}`);
  }
  return steps * 4;
}

function tokenMatching(el: Element, pattern: RegExp): string {
  const found = Array.from(el.classList).find((c) => pattern.test(c));
  if (!found) {
    throw new Error(
      `no class matching ${pattern} on: ${el.getAttribute('class')}`,
    );
  }
  return found;
}

/**
 * Touch-target size of an icon-only close button, derived from its own class
 * tokens. happy-dom runs no layout, so the pixels have to come from the
 * classes — but deriving them beats asserting a literal token: shrink the
 * padding and this fails with the size it computed, rather than passing
 * because some other token still matched a substring.
 */
function hitAreaPx(button: HTMLElement): number {
  const icon = button.querySelector('svg');
  if (!icon) throw new Error('close button renders no icon');
  const iconPx = spacingPx(tokenMatching(icon, /^(?:size|h)-[\d.]+$/));
  return iconPx + 2 * spacingPx(tokenMatching(button, /^p-[\d.]+$/));
}

describe('overlay viewport bounds', () => {
  describe('DialogContent', () => {
    function renderDialog(className?: string) {
      render(
        <Dialog defaultOpen>
          <DialogContent className={className}>
            <DialogTitle>Edit transaction</DialogTitle>
            <DialogDescription>Change the amount.</DialogDescription>
            <p>Body row</p>
          </DialogContent>
        </Dialog>,
      );
      return screen.getByRole('dialog');
    }

    it('caps its height against the dynamic viewport', () => {
      expect(renderDialog()).toHaveClass('max-h-[calc(100dvh-2rem)]');
    });

    it('scrolls the body, keeping the title inside the scroll container', () => {
      renderDialog();
      const scroller = scrollerAround(screen.getByText('Edit transaction'));
      expect(scroller).not.toBeNull();
      expect(scroller).toContainElement(screen.getByText('Body row'));
    });

    it('keeps the Close button out of the scroll container', () => {
      const content = renderDialog();
      const close = screen.getByRole('button', { name: 'Close' });
      expect(content).toContainElement(close);
      expect(scrollerAround(close)).toBeNull();
    });

    it('still merges a per-site className last', () => {
      const content = renderDialog('md:max-w-3xl');
      expect(content).toHaveClass('md:max-w-3xl');
      expect(content).toHaveClass('max-h-[calc(100dvh-2rem)]');
    });
  });

  describe('AlertDialogContent', () => {
    function renderAlert() {
      render(
        <AlertDialog defaultOpen>
          <AlertDialogContent>
            <AlertDialogTitle>Delete 12 transactions?</AlertDialogTitle>
            <AlertDialogDescription>This cannot be undone.</AlertDialogDescription>
            <p>Body row</p>
          </AlertDialogContent>
        </AlertDialog>,
      );
      return screen.getByRole('alertdialog');
    }

    it('caps its height against the dynamic viewport', () => {
      expect(renderAlert()).toHaveClass('max-h-[calc(100dvh-2rem)]');
    });

    it('scrolls the body, keeping the title inside the scroll container', () => {
      renderAlert();
      const scroller = scrollerAround(
        screen.getByText('Delete 12 transactions?'),
      );
      expect(scroller).not.toBeNull();
      expect(scroller).toContainElement(screen.getByText('Body row'));
    });
  });

  describe('SheetContent', () => {
    function renderSheet(side: 'right' | 'bottom', className?: string) {
      render(
        <Sheet defaultOpen>
          <SheetContent side={side} className={className}>
            <SheetTitle>Filters</SheetTitle>
            <SheetDescription>Narrow the list.</SheetDescription>
            <p>Body row</p>
          </SheetContent>
        </Sheet>,
      );
      return screen.getByRole('dialog');
    }

    it('caps a bottom sheet against the dynamic viewport', () => {
      expect(renderSheet('bottom')).toHaveClass('max-h-[100dvh]');
    });

    // The cap only bites because Content is a flex column and the scroller is
    // its flex item. Make Content a block box and the scroller sizes to its
    // content instead, spilling past the sheet's edge with no way to reach it —
    // while every other assertion in this file stays green, which is why this
    // one exists.
    it.each(['right', 'bottom'] as const)(
      'makes a %s sheet a flex column so the body scroller is bounded',
      (side) => {
        expect(renderSheet(side)).toHaveClass('flex', 'flex-col');
      },
    );

    it('scrolls the body of a side sheet', () => {
      renderSheet('right');
      const scroller = scrollerAround(screen.getByText('Filters'));
      expect(scroller).not.toBeNull();
      expect(scroller).toContainElement(screen.getByText('Body row'));
    });

    it('keeps the Close button out of the scroll container', () => {
      const content = renderSheet('right');
      const close = screen.getByRole('button', { name: 'Close' });
      expect(content).toContainElement(close);
      expect(scrollerAround(close)).toBeNull();
    });

    it('still merges a per-site width override last', () => {
      const content = renderSheet('right', 'w-full sm:max-w-md');
      expect(content).toHaveClass('w-full', 'sm:max-w-md');
      expect(content).toHaveClass('max-h-[100dvh]');
    });

    // A sheet whose title names WHICH thing you are looking at loses that
    // title the moment the body is long enough to scroll, because the scroller
    // wraps every child. Measured on the heatmap's day sheet at 360px with 20
    // rows: the summary total left the box after 60px of scroll while 8 rows
    // were still visible. `header` is the opt-in that puts it above the
    // scroller instead.
    it('renders `header` outside the body scroller, children inside', () => {
      render(
        <Sheet defaultOpen>
          <SheetContent
            side="bottom"
            header={
              <div>
                <SheetTitle>Tuesday</SheetTitle>
                <SheetDescription>$42.50 spent</SheetDescription>
              </div>
            }
          >
            <p>Body row</p>
          </SheetContent>
        </Sheet>,
      );
      // Both halves in one test: an "is outside the scroller" assertion passes
      // just as well when there is no scroller at all, or when nothing
      // rendered.
      expect(scrollerAround(screen.getByText('Tuesday'))).toBeNull();
      expect(scrollerAround(screen.getByText('$42.50 spent'))).toBeNull();
      expect(scrollerAround(screen.getByText('Body row'))).not.toBeNull();
    });

    it('adds no node at all when `header` is omitted', () => {
      // The opt-in has to be free for any sheet that does not use it (as of
      // B28 every shipped consumer passes `header=`, so this now guards future
      // sheets): `{undefined}` must render nothing, or such a sheet silently
      // gains a flex child and a `gap-4`.
      const content = renderSheet('bottom');
      expect(content.firstElementChild).toBe(
        scrollerAround(screen.getByText('Filters')),
      );
    });
  });

  /**
   * The vendored close button is a bare 16px icon, which is a miss on a phone
   * — and this app is phone-first. AlertDialog is absent on purpose: it has no
   * close affordance at all, only Cancel/Action, which are full-size buttons.
   */
  describe('close button touch target', () => {
    function dialogClose() {
      render(
        <Dialog defaultOpen>
          <DialogContent>
            <DialogTitle>Edit transaction</DialogTitle>
            <DialogDescription>Change the amount.</DialogDescription>
          </DialogContent>
        </Dialog>,
      );
      return screen.getByRole('button', { name: 'Close' });
    }

    function sheetClose() {
      render(
        <Sheet defaultOpen>
          <SheetContent side="right">
            <SheetTitle>Filters</SheetTitle>
            <SheetDescription>Narrow the list.</SheetDescription>
          </SheetContent>
        </Sheet>,
      );
      return screen.getByRole('button', { name: 'Close' });
    }

    const overlays: Array<[string, () => HTMLElement]> = [
      ['dialog', dialogClose],
      ['sheet', sheetClose],
    ];

    it.each(overlays)('gives the %s close a 44px hit area', (_name, close) => {
      expect(hitAreaPx(close())).toBeGreaterThanOrEqual(44);
    });

    // The padding grows the button in every direction, so an equal negative
    // margin has to pull the border box back out or the icon slides 14px in
    // and down — a desktop regression no test here can see.
    it.each(overlays)(
      'offsets the %s padding so the icon does not move',
      (_name, close) => {
        const button = close();
        const pad = tokenMatching(button, /^p-[\d.]+$/);
        expect(button).toHaveClass(`-m-${pad.slice('p-'.length)}`);
      },
    );
  });
});
