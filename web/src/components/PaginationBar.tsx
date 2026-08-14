import type { ReactNode } from 'react';
import {
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { TRANSACTION_PAGE_SIZES } from '@/lib/constants';
import { TOUCH_TARGET_SQUARE } from '@/lib/touch-target';

export interface PaginationBarProps {
  page: number;
  totalPages: number;
  perPage: number;
  onPageChange: (page: number) => void;
  onPerPageChange: (perPage: number) => void;
  /**
   * Optional content rendered on the leading (left) side of the bar,
   * immediately after the "Rows per page" cluster. The Trash page uses this to
   * surface whole-trash bulk actions (Restore all / Purge all) in the table
   * toolbar rather than above the card — callers that omit it get the plain
   * two-cluster layout.
   */
  leadingActions?: ReactNode;
  /**
   * Narrow layout: the numbered page buttons collapse to a "Page N of M"
   * readout. Nine 32px buttons plus the rows-per-page control need ~440px and
   * a 390px viewport has 358px — unwrapped they overflow the card, and wrapped
   * they cost two extra rows both above AND below the list. (Unlike the touch
   * floor below, this one really is about available room, so the caller
   * decides it from the viewport width.)
   */
  compact?: boolean;
  /** Overridable so a page with a different page-size menu can reuse this. */
  pageSizes?: readonly number[];
}

/**
 * Compute which page numbers to show between prev/next arrows.
 * Always shows first, last, and up to 2 pages around the current page,
 * with -1 as a sentinel for ellipsis gaps.
 */
function getPageNumbers(page: number, totalPages: number): number[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const pages = new Set<number>();
  pages.add(1);
  pages.add(totalPages);
  for (let i = Math.max(2, page - 1); i <= Math.min(totalPages - 1, page + 1); i++) {
    pages.add(i);
  }
  const sorted = [...pages].sort((a, b) => a - b);
  const result: number[] = [];
  for (let i = 0; i < sorted.length; i++) {
    if (i > 0 && sorted[i] - sorted[i - 1] > 1) {
      result.push(-1); // ellipsis sentinel
    }
    result.push(sorted[i]);
  }
  return result;
}

/**
 * The pager shared by the Transactions and Trash tables.
 *
 * One copy, because the two pages had drifted into two: both grew a phone card
 * list in the same slice and both then needed the same compact readout and the
 * same 44px controls, which is exactly the point at which a duplicated
 * component stops being cheap.
 *
 * Every control is 32px by default and floored at 44px when the pointer is
 * coarse. A pager is mostly small icon buttons sitting next to each other,
 * which is the worst shape for a thumb, and unlike a checkbox there is nothing
 * here whose visible size has to stay put — so the box grows, not just the hit
 * area.
 *
 * The gate is the POINTER, not the viewport width, and this bar is the reason
 * why. It used to read `size-11 md:size-8`, which hands a ~1130px touch tablet
 * in landscape the 32px desktop sizes. Worse, the trigger's floor comes from
 * `SelectTrigger` itself and is pointer-gated: leaving the buttons on a width
 * gate would put a 44px Select beside six 32px buttons in one flex row on
 * exactly that tablet — a 12px step, which is uglier than the coherent-if-small
 * 32px it replaced. The trigger and its neighbours have to share one gate.
 *
 * Not one class here says so any more. Both floors now come from the primitives
 * — `SelectTrigger` for the trigger, `Button` (`size="icon"`, so both axes) for
 * the six buttons — and the `size-8`/`h-8` left behind is density only. The
 * ellipsis is the exception and keeps `TOUCH_TARGET_SQUARE`: it is a `<span>`
 * holding a slot in the same flex row, so no primitive can size it, and sized
 * differently it staggers the numbers on one side of the gap.
 */
export function PaginationBar({
  page,
  totalPages,
  perPage,
  onPageChange,
  onPerPageChange,
  leadingActions,
  compact = false,
  pageSizes = TRANSACTION_PAGE_SIZES,
}: PaginationBarProps) {
  const pageNumbers = getPageNumbers(page, totalPages);

  return (
    // `flex-wrap` + row-gap keeps the toolbar legible once `leadingActions`
    // adds buttons on narrow viewports — without it the page-nav cluster
    // would spill past the card edge instead of wrapping to a new line.
    <div className="flex flex-wrap items-center justify-between gap-y-2 px-4 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <p className="text-sm font-medium">Rows per page</p>
        <Select
          value={String(perPage)}
          onValueChange={(v) => onPerPageChange(Number(v))}
        >
          {/* The visible "Rows per page" text is a <p>, not a <label>, so it
              names nothing programmatically — the trigger needs its own name
              or it announces only its value.

              `h-8` is the density only; the 44px touch floor arrives from
              `SelectTrigger`'s own `coarse:min-h-11`, which outranks this
              because `min-h` and `h` are separate tailwind-merge groups and
              CSS clamps the used height up. Nothing to add here. */}
          <SelectTrigger className="h-8 w-[70px]" aria-label="Rows per page">
            <SelectValue />
          </SelectTrigger>
          <SelectContent side="top">
            <SelectGroup>
              {pageSizes.map((opt) => (
                <SelectItem key={opt} value={String(opt)}>
                  {opt}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        {leadingActions}
      </div>

      <div className="flex items-center gap-1">
        <Button
          variant="outline"
          size="icon"
          className="hidden size-8 lg:flex"
          onClick={() => onPageChange(1)}
          disabled={page <= 1}
          aria-label="Go to first page"
        >
          <ChevronsLeft />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="size-8"
          onClick={() => onPageChange(page - 1)}
          disabled={page <= 1}
          aria-label="Go to previous page"
        >
          <ChevronLeft />
        </Button>

        {compact ? (
          // tabular-nums so the readout does not reflow as the page number
          // gains a digit, which on a phone shifts the arrows under the thumb.
          <span className="px-2 text-sm font-medium tabular-nums text-muted-foreground">
            Page {page} of {totalPages}
          </span>
        ) : (
          pageNumbers.map((p, i) =>
            p === -1 ? (
              <span
                key={`ellipsis-${i}`}
                className={cn(
                  'flex size-8 items-center justify-center text-sm text-muted-foreground',
                  TOUCH_TARGET_SQUARE,
                )}
                aria-hidden
              >
                ...
              </span>
            ) : (
              <Button
                key={p}
                variant={p === page ? 'outline' : 'ghost'}
                size="icon"
                className="size-8 text-xs"
                onClick={() => onPageChange(p)}
                aria-current={p === page ? 'page' : undefined}
              >
                {p}
              </Button>
            ),
          )
        )}

        <Button
          variant="outline"
          size="icon"
          className="size-8"
          onClick={() => onPageChange(page + 1)}
          disabled={page >= totalPages}
          aria-label="Go to next page"
        >
          <ChevronRight />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="hidden size-8 lg:flex"
          onClick={() => onPageChange(totalPages)}
          disabled={page >= totalPages}
          aria-label="Go to last page"
        >
          <ChevronsRight />
        </Button>
      </div>
    </div>
  );
}
