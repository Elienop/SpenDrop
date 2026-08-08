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
   * Phone layout: the numbered page buttons collapse to a "Page N of M"
   * readout. Nine 32px buttons plus the rows-per-page control need ~440px and
   * a 390px viewport has 358px — unwrapped they overflow the card, and wrapped
   * they cost two extra rows both above AND below the list.
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
 * Every control is `size-11` on a phone and `md:size-8` above it — a pager is
 * mostly small icon buttons sitting next to each other, which is the worst
 * shape for a thumb, and unlike a checkbox there is nothing here whose visible
 * size has to stay put.
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
              or it announces only its value. */}
          <SelectTrigger
            className="h-11 w-[70px] md:h-8"
            aria-label="Rows per page"
          >
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
          className="hidden size-11 md:size-8 lg:flex"
          onClick={() => onPageChange(1)}
          disabled={page <= 1}
          aria-label="Go to first page"
        >
          <ChevronsLeft />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="size-11 md:size-8"
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
                  'flex size-11 items-center justify-center text-sm text-muted-foreground md:size-8',
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
                className="size-11 text-xs md:size-8"
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
          className="size-11 md:size-8"
          onClick={() => onPageChange(page + 1)}
          disabled={page >= totalPages}
          aria-label="Go to next page"
        >
          <ChevronRight />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="hidden size-11 md:size-8 lg:flex"
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
