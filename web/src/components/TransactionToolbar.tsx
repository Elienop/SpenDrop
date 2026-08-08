import {
  ArrowDownWideNarrow,
  ArrowUpNarrowWide,
  ListChecks,
  Search,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ButtonGroup } from '@/components/ui/button-group';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import type { SortColumn, SortDirection } from '@/hooks/useTransactions';

/**
 * Every column the list can be ordered by, with the label the phone Sort menu
 * shows for it. The table's sortable headers are the desktop equivalent; both
 * feed the same `setSort`, whose contract is "same column again flips the
 * direction, a different column starts at descending".
 */
const SORT_OPTIONS: readonly { value: SortColumn; label: string }[] = [
  { value: 'date', label: 'Date' },
  { value: 'description', label: 'Description' },
  { value: 'category', label: 'Category' },
  { value: 'amount', label: 'Amount' },
  { value: 'tags', label: 'Tags' },
];

/**
 * Controls that exist only below `md`. They replace affordances the phone
 * layout removes: the table's sortable column headers, and the per-row
 * checkboxes that are hidden until selection mode is on.
 *
 * Present-or-absent as one object rather than five optional props — the group
 * is all-or-nothing, and a half-wired mobile toolbar would type-check.
 */
export interface TransactionToolbarMobileControls {
  sortBy: SortColumn;
  sortDir: SortDirection;
  onSort: (column: SortColumn) => void;
  selectionMode: boolean;
  onToggleSelectionMode: () => void;
}

interface TransactionToolbarProps {
  search: string;
  onSearchChange: (value: string) => void;
  type: string;
  onTypeChange: (value: string) => void;
  activeFilterCount: number;
  showFilters: boolean;
  onToggleFilters: () => void;
  showEntry: boolean;
  onToggleEntry: () => void;
  /** Omitted at `md` and up, where the table header owns sorting and every row
   *  already carries a checkbox. */
  mobile?: TransactionToolbarMobileControls;
}

export function TransactionToolbar({
  search,
  onSearchChange,
  type,
  onTypeChange,
  activeFilterCount,
  showFilters,
  onToggleFilters,
  showEntry,
  onToggleEntry,
  mobile,
}: TransactionToolbarProps) {
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-card p-2">
      {/* 240px is the min width where the "Search transactions..." placeholder fits without truncation */}
      <div className="relative min-w-[240px] flex-1">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          type="text"
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search transactions..."
          aria-label="Search transactions"
          className="h-9 pl-8"
        />
      </div>

      <div className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />

      <ButtonGroup>
        {([
          { value: '', label: 'All' },
          { value: 'expense', label: 'Expenses' },
          { value: 'income', label: 'Income' },
        ] as const).map((opt) => (
          <Button
            key={opt.value || 'all'}
            type="button"
            variant={type === opt.value ? 'secondary' : 'outline'}
            size="sm"
            className="h-11 text-xs md:h-8"
            onClick={() => onTypeChange(opt.value)}
          >
            {opt.label}
          </Button>
        ))}
      </ButtonGroup>

      <div className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />

      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-11 text-xs md:h-8"
        onClick={onToggleFilters}
        aria-expanded={showFilters}
      >
        Filters
        {activeFilterCount > 0 && (
          <Badge variant="secondary" className="ml-2 h-5 px-1.5 text-xs">
            {activeFilterCount}
          </Badge>
        )}
      </Button>

      <Button
        type="button"
        size="sm"
        className="h-11 min-w-[76px] text-xs md:h-8"
        onClick={onToggleEntry}
        aria-expanded={showEntry}
      >
        {showEntry ? 'Cancel' : '+ Add'}
      </Button>

      {mobile && <MobileToolbarControls {...mobile} />}
    </div>
  );
}

function MobileToolbarControls({
  sortBy,
  sortDir,
  onSort,
  selectionMode,
  onToggleSelectionMode,
}: TransactionToolbarMobileControls) {
  const activeLabel =
    SORT_OPTIONS.find((o) => o.value === sortBy)?.label ?? 'Date';
  const directionWord = sortDir === 'asc' ? 'ascending' : 'descending';
  const DirectionIcon =
    sortDir === 'asc' ? ArrowUpNarrowWide : ArrowDownWideNarrow;

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          {/*
            The accessible name is the visible "Sort: <column>" plus the
            direction, so it CONTAINS the visible label (WCAG 2.5.3) rather
            than replacing it the way an aria-label would.
          */}
          {/* h-11, not h-8: this control renders only below `md`, so there is
              no desktop density to preserve and no `md:` half to add. */}
          <Button type="button" variant="outline" size="sm" className="h-11 text-xs">
            <DirectionIcon aria-hidden="true" />
            Sort: {activeLabel}
            <span className="sr-only">, {directionWord}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {/*
            Radio items, not plain items: they carry role="menuitemradio" +
            aria-checked, so the current column is announced rather than only
            drawn. Re-selecting the checked one still fires onValueChange
            (verified in @radix-ui/react-menu — MenuRadioItem calls it
            unconditionally on select), which is what makes "pick the same
            column again" flip the direction, matching the table headers.
          */}
          <DropdownMenuRadioGroup
            value={sortBy}
            onValueChange={(next) => {
              const opt = SORT_OPTIONS.find((o) => o.value === next);
              if (opt) onSort(opt.value);
            }}
          >
            {SORT_OPTIONS.map((opt) => (
              <DropdownMenuRadioItem
                key={opt.value}
                value={opt.value}
                className="min-h-11"
              >
                {opt.label}
                {sortBy === opt.value && (
                  <>
                    {/* The direction was announced but only drawn on the
                        closed trigger, where the icon is 16px of grey next to
                        a word. Repeating it on the checked row is where the
                        user is actually looking when they change it — and it
                        is the only place that says re-picking this row flips
                        the direction rather than doing nothing. */}
                    <DirectionIcon className="ml-auto" aria-hidden="true" />
                    <span className="sr-only">, {directionWord}</span>
                  </>
                )}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>

      {/*
        The label stays "Select" in both states — a toggle button reports its
        state through aria-pressed, and a label that flips to "Done" would make
        a screen reader announce the button as something it is not yet.
      */}
      <Button
        type="button"
        variant={selectionMode ? 'secondary' : 'outline'}
        size="sm"
        className="h-11 text-xs"
        onClick={onToggleSelectionMode}
        aria-pressed={selectionMode}
      >
        <ListChecks aria-hidden="true" />
        Select
      </Button>
    </>
  );
}
