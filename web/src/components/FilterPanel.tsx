import type { CSSProperties } from 'react';
import {
  startOfMonth,
  endOfMonth,
  subMonths,
  startOfYear,
  format,
} from 'date-fns';
import { X } from 'lucide-react';
import type { TransactionFilters } from '../hooks/useTransactions';
import type { Category, SavedFilter } from '../api/types';
import { getCategoryColorVar } from '../lib/chart-colors';
import { FORMAT_ISO_DATE } from '@/lib/dates';
import { selectAllOnFocus } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';

interface FilterPanelProps {
  filters: TransactionFilters;
  setFilter: (key: keyof TransactionFilters, value: string) => void;
  clearPanelFilters: () => void;
  categories: Category[];
  savedFilters: SavedFilter[];
  onSaveFilter: (name: string) => void;
  onLoadFilter: (filter: SavedFilter) => void;
  onDeleteFilter: (id: number) => void;
}

export function FilterPanel({
  filters,
  setFilter,
  clearPanelFilters,
  categories,
  savedFilters,
  onSaveFilter,
  onLoadFilter,
  onDeleteFilter,
}: FilterPanelProps) {
  const now = new Date();

  function setDatePreset(preset: 'thisMonth' | 'lastMonth' | 'thisYear') {
    let from: Date;
    let to: Date;
    switch (preset) {
      case 'thisMonth':
        from = startOfMonth(now);
        to = endOfMonth(now);
        break;
      case 'lastMonth': {
        const last = subMonths(now, 1);
        from = startOfMonth(last);
        to = endOfMonth(last);
        break;
      }
      case 'thisYear':
        from = startOfYear(now);
        to = now;
        break;
    }
    setFilter('dateFrom', format(from, FORMAT_ISO_DATE));
    setFilter('dateTo', format(to, FORMAT_ISO_DATE));
  }

  function handleSaveFilter() {
    const name = window.prompt('Name this filter:');
    if (name) {
      onSaveFilter(name);
    }
  }

  const selectedCategoryIds = filters.categoryIds
    ? filters.categoryIds.split(',').filter(Boolean)
    : [];

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-end">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={clearPanelFilters}
        >
          Clear all
        </Button>
      </div>

      <Tabs defaultValue="date" className="w-full">
        <TabsList className="grid w-full grid-cols-4">
          <TabsTrigger value="date">Date</TabsTrigger>
          <TabsTrigger value="category">Category</TabsTrigger>
          <TabsTrigger value="amount">Amount</TabsTrigger>
          <TabsTrigger value="saved">Saved</TabsTrigger>
        </TabsList>

        <TabsContent value="date" className="flex flex-col gap-3 pt-4">
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setDatePreset('thisMonth')}
            >
              This Month
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setDatePreset('lastMonth')}
            >
              Last Month
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setDatePreset('thisYear')}
            >
              This Year
            </Button>
          </div>
          <div className="flex items-center gap-2">
            <Input
              type="date"
              value={filters.dateFrom}
              onChange={(e) => setFilter('dateFrom', e.target.value)}
              aria-label="Date from"
            />
            <span className="text-xs text-muted-foreground">to</span>
            <Input
              type="date"
              value={filters.dateTo}
              onChange={(e) => setFilter('dateTo', e.target.value)}
              aria-label="Date to"
            />
          </div>
        </TabsContent>

        <TabsContent value="category" className="flex flex-col gap-3 pt-4">
          <div className="flex flex-wrap gap-2">
            {categories.map((cat) => {
              const selected = selectedCategoryIds.includes(String(cat.id));
              const style = {
                '--chip-color': getCategoryColorVar(cat),
              } as CSSProperties;
              return (
                <Button
                  key={cat.id}
                  type="button"
                  variant={selected ? 'default' : 'outline'}
                  size="sm"
                  style={
                    selected
                      ? {
                          ...style,
                          backgroundColor: 'var(--chip-color)',
                          borderColor: 'var(--chip-color)',
                        }
                      : style
                  }
                  onClick={() => {
                    const next = selected
                      ? selectedCategoryIds.filter(
                          (id) => id !== String(cat.id),
                        )
                      : [...selectedCategoryIds, String(cat.id)];
                    setFilter('categoryIds', next.join(','));
                  }}
                >
                  {cat.name}
                </Button>
              );
            })}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="filter-tags">Tags</Label>
            <Input
              id="filter-tags"
              type="text"
              placeholder="Filter by tags..."
              value={filters.tags}
              onChange={(e) => setFilter('tags', e.target.value)}
              aria-label="Filter by tags"
            />
          </div>
        </TabsContent>

        <TabsContent value="amount" className="flex flex-col gap-3 pt-4">
          {/* NO `min="0"` on either bound, and this is the one place in the app
              where a typed minus is the user saying what they mean rather than
              a slipped key. Amounts are signed — a refund is a negative
              expense — and the backend compares `amount_min`/`amount_max`
              against the signed value, so hunting for refunds means entering a
              negative bound. The amount ENTRY inputs keep their `min="0"`:
              there the sign comes from the Refund toggle and a minus is always
              a typo. Two different jobs, deliberately different markup.

              Known consequence, no migration: a saved filter created before
              this now also matches refunds, because a `max` of 50 always did
              mean "<= 50" and -20 satisfies it.

              `inputMode="decimal"` opens the phone's decimal keypad — see
              `<MonthlyBudgetCard>` in `pages/Budgets.tsx` for the reasoning and
              the S24 evidence. Caveat unique to this pair, since these two
              bounds are the only place in the app where a typed minus is
              meant: the spec leaves a minus key up to the device for BOTH
              `decimal` and `numeric`, so neither hint guarantees one.

              WHAT TO DO IF THE S24 KEYPAD HIDES THE MINUS — the reflex ("drop
              the hint") is probably wrong, so the order matters:

              1. FIRST compare against a bare `type="number"` field with no
                 `inputMode` on the same device. On Android/Chromium the two
                 are reported to map to the same unsigned IME class, in which
                 case removing the hint restores nothing and only costs the
                 decimal keypad. VERIFY THAT BEFORE RELYING ON IT — it is a
                 report, not something measured here, and the one platform
                 where the hint genuinely does remove a minus is iOS (bare
                 `type=number` gives the numbers-and-punctuation pad, which has
                 `-`; `decimal` gives one without). This household is Android.
              2. The durable fix is a sign affordance in this tab — a "refunds
                 only" / sign chip beside the bounds — NOT a markup change.
                 Worth doing regardless: nothing on screen currently tells a
                 user that a negative bound is how you find refunds.
              3. NOT `inputMode="text"`. That serves the rare negative bound by
                 handing every positive one a QWERTY keyboard. */}
          <div className="flex items-center gap-2">
            <Input
              type="number"
              inputMode="decimal"
              placeholder="Min $"
              value={filters.amountMin}
              onChange={(e) => setFilter('amountMin', e.target.value)}
              onFocus={selectAllOnFocus}
              step="0.01"
              aria-label="Minimum amount"
            />
            <span className="text-xs text-muted-foreground">to</span>
            <Input
              type="number"
              inputMode="decimal"
              placeholder="Max $"
              value={filters.amountMax}
              onChange={(e) => setFilter('amountMax', e.target.value)}
              onFocus={selectAllOnFocus}
              step="0.01"
              aria-label="Maximum amount"
            />
          </div>
        </TabsContent>

        <TabsContent value="saved" className="flex flex-col gap-3 pt-4">
          <div className="flex flex-col gap-2">
            {savedFilters.map((sf) => (
              <div
                key={sf.id}
                className="flex items-center justify-between rounded-md border px-3 py-2"
              >
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  className="font-medium"
                  onClick={() => onLoadFilter(sf)}
                >
                  {sf.name}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-6 w-6"
                  onClick={() => onDeleteFilter(sf.id)}
                  aria-label={`Delete saved filter ${sf.name}`}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={handleSaveFilter}
            >
              + Save Filter
            </Button>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}
