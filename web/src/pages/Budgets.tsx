import {
  useState,
  useEffect,
  useCallback,
  useMemo,
  useRef,
} from 'react';
import type { FormEvent, RefObject } from 'react';
import { useNavigate } from 'react-router-dom';
import { toast } from 'sonner';
import { api } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type { Budget, Category, CategoryBudget } from '../api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { selectAllOnFocus } from '@/lib/utils';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Badge } from '@/components/ui/badge';
import { MIN_YEAR, MAX_YEAR, MONTH_NAMES_FULL } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { isAdmin } from '@/lib/roles';
import { Skeleton } from '@/components/ui/skeleton';
import { TYPE_EXPENSE } from '@/lib/transaction-types';

/* ---------- Module-scope constants ---------- */

// User-facing label for each top-level route the sidebar links to. Used
// in the discard-edits dialog when a route change is intercepted — keeps
// the description friendly ("Leaving for Transactions") instead of
// dumping the raw pathname. Falls back to the pathname if a new route
// ever shows up without a label here.
const ROUTE_LABELS: Record<string, string> = {
  '/': 'Dashboard',
  '/quick': 'Quick add',
  '/transactions': 'Transactions',
  '/budgets': 'Budgets',
  '/savings': 'Savings',
  '/reports': 'Reports',
  '/categories': 'Categories',
  '/trash': 'Trash',
  '/settings': 'Settings',
};

/* ---------- Shared: discard-edits confirm dialog ---------- */

interface DiscardEditsDialogProps {
  open: boolean;
  count: number;
  destinationLabel: string;
  onCancel: () => void;
  onConfirm: () => void;
}

/**
 * Shadcn-styled replacement for the native `window.confirm` we used to
 * pop when a user tried to leave the Budgets page (year dropdown, month
 * dropdown, sidebar navigation, or browser-back) with unsaved budget
 * edits. One component for all call sites; the body reads "Discard
 * <N> unsaved change(s)? Leaving for <destinationLabel> will lose
 * them."
 *
 * Uses AlertDialog (not Dialog) so Radix defaults focus to the Cancel
 * button — Enter dismisses safely. A destructive autoFocus would
 * destroy edits on a stray keystroke, which is the canonical wrong
 * default for blocking confirms.
 */
function DiscardEditsDialog({
  open,
  count,
  destinationLabel,
  onCancel,
  onConfirm,
}: DiscardEditsDialogProps) {
  return (
    <AlertDialog
      open={open}
      onOpenChange={(isOpen) => {
        if (!isOpen) onCancel();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Discard unsaved changes?</AlertDialogTitle>
          <AlertDialogDescription>
            You have{' '}
            <span className="font-semibold text-foreground">
              {count} unsaved change{count === 1 ? '' : 's'}
            </span>
            . Leaving for{' '}
            <span className="font-semibold text-foreground">
              {destinationLabel}
            </span>{' '}
            will lose them. Save your budgets first if you want to keep
            them.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={onCancel}>
            Keep editing
          </AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive"
            onClick={onConfirm}
          >
            Discard changes
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

/* ---------- Monthly Budgets section ---------- */

// How far ahead of the current year the budget picker lets users plan.
// Keep small enough that the dropdown stays scannable but large enough
// to cover realistic forward budgeting (mortgage amortization, yearly
// goal tracking). Lower bound is `MIN_YEAR` so historical xlsx imports
// that predate the current year still have a landing spot.
const BUDGET_YEARS_AHEAD = 5;

interface MonthlyBudgetsSectionProps {
  // Parent-held mirror of `dirtyCount`. Lets the Budgets page's
  // click-listener consult the current dirty state *before* committing
  // a route change (and before the page unmounts and wipes the
  // in-progress edits with it). Passed as a ref rather than a callback
  // so keystrokes don't re-render the parent.
  dirtyCountRef?: RefObject<number>;
  // Reactive companion to `dirtyCountRef` — the page needs an
  // `isDirty` *state* (not just a ref) to (a) gate the popstate
  // sentinel effect and (b) pass the live count into the discard
  // dialog without reading refs during render. The cost is one extra
  // setState per dirtyCount transition, which only fires when count
  // crosses an integer boundary, not per keystroke.
  onDirtyChange?: (count: number) => void;
}

function MonthlyBudgetsSection({
  dirtyCountRef,
  onDirtyChange,
}: MonthlyBudgetsSectionProps) {
  const baseCurrency = useBaseCurrency();
  const initialYear = new Date().getFullYear();
  const [year, setYear] = useState(initialYear);
  // Per-month input strings, keyed by 1-12. Strings (not numbers) so a
  // cleared field stays empty rather than collapsing to "0", which the
  // backend would reject and which is ambiguous with "not yet set".
  const [editAmounts, setEditAmounts] = useState<Record<number, string>>({});
  // Raw text for the "Set all months" quick-fill input. Kept separate
  // from `editAmounts` so applying it is an explicit user action; typing
  // here must never mutate the 12 rows.
  const [bulkInput, setBulkInput] = useState('');
  // Non-null iff at least one bulk write (Apply or Copy-from-previous)
  // has happened since the last fetch/Undo. Holds the `editAmounts`
  // captured *before the first* bulk write so Undo restores the fully
  // pre-bulk state — not an intermediate one. Subsequent bulk writes do
  // not overwrite it; otherwise `Apply → Copy → Undo` would strand the
  // user at the Apply-filled state instead of the original baseline.
  // Cleared on Save success (via refetch), year change, and Undo.
  const [preBulkSnapshot, setPreBulkSnapshot] = useState<
    Record<number, string> | null
  >(null);
  const [saving, setSaving] = useState(false);
  // Snapshot of `editAmounts` at the moment of the last successful fetch,
  // keyed by month. We compare the user's current input against the
  // *string* baseline, not the numeric one, because SQLite REAL round-trips
  // can produce `3000.0999999999999` from an original `3000.10` — a direct
  // float compare (`Number('3000.10') === 3000.0999999999999`) returns
  // `true` and silently drops a genuine edit. Stored in a ref so updating
  // it doesn't trigger a re-render.
  const baselineRef = useRef<Record<number, string>>({});

  // `currentYear + N … MIN_YEAR`, descending. Memoized not for perf
  // (32 items) but to keep `new Date()` out of render — two renders
  // crossing a New Year midnight would otherwise produce different
  // option arrays and reset Radix's Select focus state.
  const yearSelectOptions = useMemo(() => {
    const opts: number[] = [];
    for (let y = initialYear + BUDGET_YEARS_AHEAD; y >= MIN_YEAR; y--) {
      opts.push(y);
    }
    return opts;
  }, [initialYear]);

  const fetchBudgets = useCallback(async () => {
    const data = await api.get<Budget[]>(`budgets?year=${year}`);
    const amounts: Record<number, string> = {};
    for (const b of data) amounts[b.month] = String(b.amount);
    baselineRef.current = { ...amounts };
    setEditAmounts(amounts);
    setPreBulkSnapshot(null);
  }, [year]);

  useEffect(() => {
    fetchBudgets().catch((err) => {
      // Surface rather than swallow: the previous silent catch would leave
      // stale rows on screen after a failed year-change fetch, and any
      // subsequent save would target the *last successfully loaded* year.
      baselineRef.current = {};
      setEditAmounts({});
      setPreBulkSnapshot(null);
      toast.error(
        'Failed to load budgets: ' +
          (err instanceof Error ? err.message : 'unknown'),
      );
    });
  }, [fetchBudgets]);

  function handleApplyBulk() {
    const raw = bulkInput.trim();
    const n = Number(raw);
    // Mirror the per-row validation: empty / non-finite / ≤0 are all
    // rejected. Keyed off the same Number.isFinite check as `handleSave`
    // so "Apply" can never stage a row that the save loop would refuse.
    if (raw === '' || !Number.isFinite(n) || n <= 0) {
      toast.error('Amount must be greater than 0');
      return;
    }
    setPreBulkSnapshot((prev) => prev ?? editAmounts);
    const next: Record<number, string> = {};
    for (let m = 1; m <= 12; m++) next[m] = raw;
    setEditAmounts(next);
    setBulkInput('');
  }

  function handleUndoBulk() {
    if (preBulkSnapshot === null) return;
    setEditAmounts(preBulkSnapshot);
    setPreBulkSnapshot(null);
  }

  async function handleCopyFromPrev() {
    const prev = year - 1;
    if (prev < MIN_YEAR) return;
    try {
      const data = await api.get<Budget[]>(`budgets?year=${prev}`);
      if (data.length === 0) {
        toast.info(`No budgets found for ${prev}`);
        return;
      }
      setPreBulkSnapshot((snap) => snap ?? editAmounts);
      const next: Record<number, string> = {};
      for (const b of data) next[b.month] = String(b.amount);
      setEditAmounts(next);
      setBulkInput('');
    } catch (err) {
      toast.error(
        `Failed to load ${prev}: ` +
          (err instanceof Error ? err.message : 'unknown'),
      );
    }
  }

  async function handleSave(e: FormEvent) {
    e.preventDefault();

    // Bucket the 12 rows into pending (save), invalid (block), unchanged
    // (skip). O(1) baseline lookups via the ref — comparison is on
    // strings, see the `baselineRef` comment for rationale.
    const pending: { month: number; amount: number }[] = [];
    const invalidMonths: number[] = [];
    for (let m = 1; m <= 12; m++) {
      const raw = editAmounts[m] ?? '';
      if (raw === '') continue;
      const n = Number(raw);
      // Backend rejects amount <= 0; surface it client-side with a
      // concrete per-row error instead of silently dropping.
      if (!Number.isFinite(n) || n <= 0) {
        invalidMonths.push(m);
        continue;
      }
      const baseline = baselineRef.current[m] ?? '';
      if (raw === baseline) continue;
      pending.push({ month: m, amount: n });
    }

    if (invalidMonths.length > 0) {
      const names = invalidMonths
        .map((m) => MONTH_NAMES_FULL[m - 1])
        .join(', ');
      toast.error(`Amount must be greater than 0: ${names}`);
      return;
    }
    if (pending.length === 0) {
      toast.info('No changes to save');
      return;
    }

    setSaving(true);
    // Track the month we're currently PUTting so a mid-loop failure can
    // point the user at the row that broke, rather than a generic error.
    let failedMonth: number | null = null;
    try {
      for (const { month, amount } of pending) {
        failedMonth = month;
        await api.put(`budgets/${year}/${month}`, { amount });
      }
      failedMonth = null;
      toast.success(
        `Saved ${pending.length} budget${pending.length === 1 ? '' : 's'}`,
      );
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to save';
      if (failedMonth !== null) {
        toast.error(`${MONTH_NAMES_FULL[failedMonth - 1]}: ${msg}`);
      } else {
        toast.error(msg);
      }
    } finally {
      // Await the refetch so `saving` stays true (and inputs stay
      // disabled) until local state is consistent with server truth.
      // Without this await, a user edit between `setSaving(false)` and
      // the fetch completing would be clobbered by the refetch.
      try {
        await fetchBudgets();
      } catch (err) {
        toast.error(
          'Refresh failed: ' +
            (err instanceof Error ? err.message : 'unknown'),
        );
      }
      setSaving(false);
    }
  }

  // Sum of positive, finite per-row values. Empty or invalid rows
  // contribute 0 so the total never flashes NaN while the user is mid-edit.
  const annualTotal = useMemo(() => {
    let sum = 0;
    for (let m = 1; m <= 12; m++) {
      const n = Number(editAmounts[m] ?? '');
      if (Number.isFinite(n) && n > 0) sum += n;
    }
    return sum;
  }, [editAmounts]);

  // Count of rows whose `editAmounts` value differs from `baselineRef`
  // AND would produce a valid PUT at save time — mirroring the bucketing
  // logic in `handleSave`. The count drives the "(N)" badge on the Save
  // button and the year-change / beforeunload confirms, so it has to
  // match "what would actually save" rather than "what's visually
  // different" (a row whose value was cleared is visually different but
  // we don't issue a DELETE for it, so it shouldn't block navigation).
  //
  // `baselineRef.current` is read on every render; React re-renders when
  // `editAmounts` changes, and every write to `baselineRef.current` in
  // `fetchBudgets` is paired with a `setEditAmounts`, so this memo always
  // sees the latest ref value.
  const dirtyCount = useMemo(() => {
    let count = 0;
    for (let m = 1; m <= 12; m++) {
      const raw = editAmounts[m] ?? '';
      if (raw === '') continue;
      const n = Number(raw);
      if (!Number.isFinite(n) || n <= 0) continue;
      const baseline = baselineRef.current[m] ?? '';
      if (raw === baseline) continue;
      count++;
    }
    return count;
  }, [editAmounts]);

  // Mirror `dirtyCount` into the parent's ref AND into a parent
  // setState callback. The ref is read by the click-listener (cheap
  // per-click read), the state by the popstate-sentinel effect (which
  // needs reactivity to install/uninstall the history.pushState
  // sentinel based on dirtiness). The cleanup resets both to 0 on
  // unmount so a stale value can't block a future nav after this
  // component is already gone.
  useEffect(() => {
    if (dirtyCountRef) dirtyCountRef.current = dirtyCount;
    onDirtyChange?.(dirtyCount);
    return () => {
      if (dirtyCountRef) dirtyCountRef.current = 0;
      onDirtyChange?.(0);
    };
  }, [dirtyCount, dirtyCountRef, onDirtyChange]);

  // Block accidental browser close / reload while changes are unsaved.
  // The browser always shows its own generic prompt; the `returnValue`
  // assignment is the legacy handshake that triggers it on Chromium.
  // Note: this does NOT fire on in-app navigation (route changes) —
  // those are guarded separately.
  useEffect(() => {
    if (dirtyCount === 0) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirtyCount]);

  // When a year switch is pending (dirty + user picked a different year),
  // we park the target year here and open <DiscardEditsDialog>. Clearing
  // this closes the dialog. Storing the target year (not just a flag) lets
  // us name the destination in the prompt body.
  const [pendingYear, setPendingYear] = useState<number | null>(null);

  function handleYearSelect(value: string) {
    const next = Number(value);
    // Defensive: Radix `SelectItem` values are always stringified years
    // here, but `Number('')` is 0 and `Number('custom')` is NaN — either
    // would slip past the `next === year` guard and yield a garbage
    // `setYear` call. Reject anything that isn't a valid in-range year.
    if (!Number.isInteger(next) || next < MIN_YEAR || next > MAX_YEAR) {
      return;
    }
    if (next === year) return;
    if (dirtyCount > 0) {
      setPendingYear(next);
      return;
    }
    setYear(next);
  }

  const copyPrevDisabled = saving || year <= MIN_YEAR;
  // `Apply` stays disabled until the text parses as a positive finite
  // number — matches the toast-error branch in `handleApplyBulk` so the
  // user sees the button react before they click.
  const bulkParsed = Number(bulkInput.trim());
  const applyDisabled =
    saving ||
    bulkInput.trim() === '' ||
    !Number.isFinite(bulkParsed) ||
    bulkParsed <= 0;

  return (
    <Card>
      <CardHeader className="flex flex-col gap-3">
        <CardTitle className="text-base">Monthly Budgets</CardTitle>
        <CardDescription>
          Set how much you plan to spend in total each month. Limits per
          category are below.
        </CardDescription>
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex flex-col gap-1">
            <Label
              htmlFor="budget-year"
              className="text-sm text-muted-foreground"
            >
              Year
            </Label>
            <Select
              value={String(year)}
              onValueChange={handleYearSelect}
              disabled={saving}
            >
              <SelectTrigger
                id="budget-year"
                className="w-28"
                aria-label="Budget year"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {yearSelectOptions.map((y) => (
                  <SelectItem key={y} value={String(y)}>
                    {y}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1">
            <Label
              htmlFor="budget-set-all"
              className="text-sm text-muted-foreground"
            >
              Set all months ({baseCurrency})
            </Label>
            <div className="flex gap-2">
              <Input
                id="budget-set-all"
                type="number"
                step="0.01"
                min="0"
                placeholder="0.00"
                value={bulkInput}
                onChange={(e) => setBulkInput(e.target.value)}
                onFocus={selectAllOnFocus}
                disabled={saving}
                aria-label={`Apply amount to all months of ${year}`}
                className="w-32"
              />
              <Button
                type="button"
                variant="secondary"
                onClick={handleApplyBulk}
                disabled={applyDisabled}
              >
                Apply
              </Button>
            </div>
          </div>
          <Button
            type="button"
            variant="outline"
            onClick={() => void handleCopyFromPrev()}
            disabled={copyPrevDisabled}
            className="self-end"
          >
            Copy from {year - 1}
          </Button>
          <div className="flex flex-col gap-1 sm:ml-auto sm:items-end">
            <span className="text-sm text-muted-foreground">Annual total</span>
            <span
              className="text-sm font-medium tabular-nums"
              aria-label="Annual total"
            >
              {formatCurrency(annualTotal, baseCurrency)}
            </span>
          </div>
        </div>
        {(preBulkSnapshot !== null || dirtyCount > 0) && (
          <div className="flex items-center gap-2">
            {preBulkSnapshot !== null && (
              <>
                <Badge variant="secondary">Bulk change applied</Badge>
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  onClick={handleUndoBulk}
                  disabled={saving}
                  className="h-auto p-0"
                >
                  Undo
                </Button>
              </>
            )}
            {dirtyCount > 0 && (
              <span
                className="text-sm text-amber-600 dark:text-amber-500"
                aria-live="polite"
                data-testid="budget-dirty-indicator"
              >
                {dirtyCount} unsaved change{dirtyCount === 1 ? '' : 's'}
              </span>
            )}
          </div>
        )}
      </CardHeader>
      <CardContent>
        <form
          onSubmit={(e) => void handleSave(e)}
          className="flex flex-col gap-4"
          noValidate
        >
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Month</TableHead>
                <TableHead className="text-right">
                  Amount ({baseCurrency})
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {MONTH_NAMES_FULL.map((name, idx) => {
                const month = idx + 1;
                return (
                  <TableRow key={month}>
                    <TableCell>{name}</TableCell>
                    <TableCell className="text-right">
                      <Input
                        type="number"
                        step="0.01"
                        min="0"
                        placeholder="0.00"
                        value={editAmounts[month] ?? ''}
                        onChange={(e) =>
                          setEditAmounts((prev) => ({
                            ...prev,
                            [month]: e.target.value,
                          }))
                        }
                        onFocus={selectAllOnFocus}
                        disabled={saving}
                        aria-label={`Budget for ${name} ${year} in ${baseCurrency}`}
                        className="ml-auto max-w-[160px] text-right"
                      />
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
          <Button
            type="submit"
            className={`w-fit ${dirtyCount > 0 && !saving ? 'font-semibold' : ''}`}
            disabled={saving}
          >
            {saving
              ? 'Saving...'
              : dirtyCount > 0
                ? `Save Budgets (${dirtyCount})`
                : 'Save Budgets'}
          </Button>
        </form>
      </CardContent>
      <DiscardEditsDialog
        open={pendingYear !== null}
        count={dirtyCount}
        destinationLabel={pendingYear === null ? '' : String(pendingYear)}
        onCancel={() => setPendingYear(null)}
        onConfirm={() => {
          if (pendingYear !== null) setYear(pendingYear);
          setPendingYear(null);
        }}
      />
    </Card>
  );
}

/* ---------- Category Limits panel ---------- */

interface CategoryLimitsSectionProps {
  // Editing/saving is admin-only because the backend PUT/DELETE for
  // category-budgets reject non-admins. Non-admins still get the read
  // view (the limits matter to everyone who plans against them), but the
  // dollar fields render as static text and the Save button is hidden so
  // they can't trigger a request that would 403.
  admin: boolean;
  // Parent-held mirror of `dirtyCount` — see <MonthlyBudgetsSectionProps>
  // for the ref/callback rationale. Lets the Budgets-page click-listener
  // consult this section's dirty state before the page unmounts and
  // silently wipes in-progress edits.
  dirtyCountRef?: RefObject<number>;
  // Reactive companion — see <MonthlyBudgetsSectionProps>.
  onDirtyChange?: (count: number) => void;
}

/**
 * A single-month per-category spending-limit editor. Mirrors the
 * <MonthlyBudgetsSection> monthly-budgets editor's mechanics (year picker,
 * string-keyed dirty tracking, per-item save loop, toasts) but pivots on
 * categories instead of months and adds a month dropdown.
 *
 * Limits are keyed by category id. A blank field means "no limit": a
 * row that started blank and stays blank is skipped; a row that had a
 * limit and is cleared issues a DELETE; a row set to a positive value
 * issues a PUT. Values are compared as strings (not numbers) for the
 * same SQLite-REAL round-trip reason documented on `baselineRef` in
 * <MonthlyBudgetsSection>.
 */
function CategoryLimitsSection({
  admin,
  dirtyCountRef,
  onDirtyChange,
}: CategoryLimitsSectionProps) {
  const baseCurrency = useBaseCurrency();
  const now = useMemo(() => new Date(), []);
  const initialYear = now.getFullYear();
  const [year, setYear] = useState(initialYear);
  const [month, setMonth] = useState(now.getMonth() + 1);
  const [categories, setCategories] = useState<Category[]>([]);
  const [loading, setLoading] = useState(true);
  // Per-category input strings keyed by category id. Strings (not
  // numbers) so a cleared field stays empty rather than collapsing to
  // "0", which the backend would reject.
  const [editAmounts, setEditAmounts] = useState<Record<number, string>>({});
  const [saving, setSaving] = useState(false);
  // String baseline captured at the last successful fetch — see the
  // <MonthlyBudgetsSection> baselineRef comment for the string-compare
  // rationale.
  const baselineRef = useRef<Record<number, string>>({});

  const yearSelectOptions = useMemo(() => {
    const opts: number[] = [];
    for (let y = initialYear + BUDGET_YEARS_AHEAD; y >= MIN_YEAR; y--) {
      opts.push(y);
    }
    return opts;
  }, [initialYear]);

  // Active expense categories only, expense-sorted by sort_order — the
  // same ordering the Categories page uses within the expense group.
  const expenseCategories = useMemo(
    () =>
      categories
        .filter((c) => c.type === TYPE_EXPENSE && c.is_active)
        .sort((a, b) => a.sort_order - b.sort_order),
    [categories],
  );

  const fetchLimits = useCallback(async () => {
    setLoading(true);
    // Fetch categories and limits in parallel; both feed the editor.
    const [cats, limits] = await Promise.all([
      api.get<Category[]>('categories'),
      api.get<CategoryBudget[]>(
        `category-budgets?year=${year}&month=${month}`,
      ),
    ]);
    const amounts: Record<number, string> = {};
    for (const l of limits) amounts[l.category_id] = String(l.amount);
    baselineRef.current = { ...amounts };
    setCategories(cats);
    setEditAmounts(amounts);
    setLoading(false);
  }, [year, month]);

  useEffect(() => {
    fetchLimits().catch((err) => {
      baselineRef.current = {};
      setCategories([]);
      setEditAmounts({});
      setLoading(false);
      toast.error(
        'Failed to load category limits: ' +
          (err instanceof Error ? err.message : 'unknown'),
      );
    });
  }, [fetchLimits]);

  // When the user picks a different year/month while there are unsaved
  // edits, we park the target here and open <DiscardEditsDialog> rather
  // than letting the fetch effect overwrite `editAmounts`/`baselineRef`
  // and silently discard the edits. Storing the target (not just a flag)
  // lets us apply exactly what was picked on confirm and name the
  // destination in the prompt body. Only one can be pending at a time —
  // the pickers are disabled while `saving`, and a pending dialog blocks
  // further interaction.
  const [pendingYear, setPendingYear] = useState<number | null>(null);
  const [pendingMonth, setPendingMonth] = useState<number | null>(null);

  function handleYearSelect(value: string) {
    const next = Number(value);
    if (!Number.isInteger(next) || next < MIN_YEAR || next > MAX_YEAR) return;
    if (next === year) return;
    if (dirtyCount > 0) {
      setPendingYear(next);
      return;
    }
    setYear(next);
  }

  function handleMonthSelect(value: string) {
    const next = Number(value);
    if (!Number.isInteger(next) || next < 1 || next > 12) return;
    if (next === month) return;
    if (dirtyCount > 0) {
      setPendingMonth(next);
      return;
    }
    setMonth(next);
  }

  async function handleSave(e: FormEvent) {
    e.preventDefault();

    // Bucket each category row into puts (positive change), deletes
    // (cleared from a previous value), invalid (block), or unchanged.
    const puts: { id: number; amount: number }[] = [];
    const deletes: number[] = [];
    const invalidIds: number[] = [];
    for (const cat of expenseCategories) {
      const raw = (editAmounts[cat.id] ?? '').trim();
      const baseline = (baselineRef.current[cat.id] ?? '').trim();
      if (raw === baseline) continue;
      if (raw === '') {
        // Was set, now cleared → delete. (baseline must be non-empty
        // here since raw !== baseline.)
        deletes.push(cat.id);
        continue;
      }
      const n = Number(raw);
      if (!Number.isFinite(n) || n <= 0) {
        invalidIds.push(cat.id);
        continue;
      }
      puts.push({ id: cat.id, amount: n });
    }

    if (invalidIds.length > 0) {
      const names = invalidIds
        .map((id) => expenseCategories.find((c) => c.id === id)?.name ?? id)
        .join(', ');
      toast.error(`Amount must be greater than 0: ${names}`);
      return;
    }
    if (puts.length === 0 && deletes.length === 0) {
      toast.info('No changes to save');
      return;
    }

    setSaving(true);
    let failedName: string | null = null;
    try {
      for (const { id, amount } of puts) {
        failedName = expenseCategories.find((c) => c.id === id)?.name ?? null;
        await api.put(`category-budgets/${year}/${month}/${id}`, { amount });
      }
      for (const id of deletes) {
        failedName = expenseCategories.find((c) => c.id === id)?.name ?? null;
        await api.del(`category-budgets/${year}/${month}/${id}`);
      }
      failedName = null;
      const total = puts.length + deletes.length;
      toast.success(`Saved ${total} category limit${total === 1 ? '' : 's'}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to save';
      toast.error(failedName !== null ? `${failedName}: ${msg}` : msg);
    } finally {
      try {
        await fetchLimits();
      } catch (err) {
        toast.error(
          'Refresh failed: ' +
            (err instanceof Error ? err.message : 'unknown'),
        );
      }
      setSaving(false);
    }
  }

  // Count of rows whose value differs from baseline AND would produce a
  // valid PUT/DELETE at save time — drives the "(N)" badge on Save and the
  // month/year-change / nav-guard / beforeunload confirms, so it has to
  // match "what would actually save" rather than "what's visually
  // different".
  //
  // `baselineRef.current` is read on every render; React re-renders when
  // `editAmounts` (or `expenseCategories`) changes, and every write to
  // `baselineRef.current` in `fetchLimits` is paired with a
  // `setEditAmounts`/`setCategories`, so this memo always sees the latest
  // ref value. Do not add `baselineRef` to the deps — a ref is not a
  // reactive dependency and listing it would mislead future readers.
  const dirtyCount = useMemo(() => {
    let count = 0;
    for (const cat of expenseCategories) {
      const raw = (editAmounts[cat.id] ?? '').trim();
      const baseline = (baselineRef.current[cat.id] ?? '').trim();
      if (raw === baseline) continue;
      if (raw === '') {
        count++;
        continue;
      }
      const n = Number(raw);
      if (!Number.isFinite(n) || n <= 0) continue;
      count++;
    }
    return count;
  }, [editAmounts, expenseCategories]);

  // Mirror `dirtyCount` into the parent's ref AND state callback —
  // see the parallel comment in MonthlyBudgetsSection for the
  // ref-and-callback rationale.
  useEffect(() => {
    if (dirtyCountRef) dirtyCountRef.current = dirtyCount;
    onDirtyChange?.(dirtyCount);
    return () => {
      if (dirtyCountRef) dirtyCountRef.current = 0;
      onDirtyChange?.(0);
    };
  }, [dirtyCount, dirtyCountRef, onDirtyChange]);

  // Block accidental browser close / reload while changes are unsaved.
  // Mirrors <MonthlyBudgetsSection>'s handler; both sections live on
  // the Budgets page, so either being dirty must arm the prompt.
  useEffect(() => {
    if (dirtyCount === 0) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirtyCount]);

  const monthLabel = MONTH_NAMES_FULL[month - 1];

  return (
    <Card>
      <CardHeader className="flex flex-col gap-3">
        <CardTitle className="text-base">Category Limits</CardTitle>
        <CardDescription>
          Set an optional monthly spending limit per expense category.
          Leave a field blank for no limit.
        </CardDescription>
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex flex-col gap-1">
            <Label
              htmlFor="limits-month"
              className="text-sm text-muted-foreground"
            >
              Month
            </Label>
            <Select
              value={String(month)}
              onValueChange={handleMonthSelect}
              disabled={saving}
            >
              <SelectTrigger
                id="limits-month"
                className="w-36"
                aria-label="Limits month"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {MONTH_NAMES_FULL.map((name, idx) => (
                  <SelectItem key={idx + 1} value={String(idx + 1)}>
                    {name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1">
            <Label
              htmlFor="limits-year"
              className="text-sm text-muted-foreground"
            >
              Year
            </Label>
            <Select
              value={String(year)}
              onValueChange={handleYearSelect}
              disabled={saving}
            >
              <SelectTrigger
                id="limits-year"
                className="w-28"
                aria-label="Limits year"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {yearSelectOptions.map((y) => (
                  <SelectItem key={y} value={String(y)}>
                    {y}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {admin && dirtyCount > 0 && (
            <span
              className="self-end text-sm text-amber-600 dark:text-amber-500"
              aria-live="polite"
              data-testid="category-limits-dirty-indicator"
            >
              {dirtyCount} unsaved change{dirtyCount === 1 ? '' : 's'}
            </span>
          )}
        </div>
      </CardHeader>
      <CardContent>
        {loading ? (
          <div className="flex flex-col gap-2">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : expenseCategories.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No active expense categories.
          </p>
        ) : (
          <form
            onSubmit={(e) => void handleSave(e)}
            className="flex flex-col gap-4"
            noValidate
          >
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Category</TableHead>
                  <TableHead className="text-right">
                    Limit ({baseCurrency})
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {expenseCategories.map((cat) => (
                  <TableRow key={cat.id}>
                    <TableCell className="font-medium">
                      {cat.icon && (
                        <span className="mr-2" aria-hidden="true">
                          {cat.icon}
                        </span>
                      )}
                      {cat.name}
                    </TableCell>
                    <TableCell className="text-right">
                      {admin ? (
                        <Input
                          type="number"
                          step="0.01"
                          min="0"
                          placeholder="No limit"
                          value={editAmounts[cat.id] ?? ''}
                          onChange={(e) =>
                            setEditAmounts((prev) => ({
                              ...prev,
                              [cat.id]: e.target.value,
                            }))
                          }
                          onFocus={selectAllOnFocus}
                          disabled={saving}
                          aria-label={`Limit for ${cat.name} ${monthLabel} ${year} in ${baseCurrency}`}
                          className="ml-auto max-w-[160px] text-right"
                        />
                      ) : (
                        <span className="font-mono tabular-nums">
                          {editAmounts[cat.id] &&
                          Number.isFinite(Number(editAmounts[cat.id]))
                            ? formatCurrency(
                                Number(editAmounts[cat.id]),
                                baseCurrency,
                              )
                            : '—'}
                        </span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {admin && (
              <Button
                type="submit"
                className={`w-fit ${dirtyCount > 0 && !saving ? 'font-semibold' : ''}`}
                disabled={saving}
              >
                {saving
                  ? 'Saving...'
                  : dirtyCount > 0
                    ? `Save Category Limits (${dirtyCount})`
                    : 'Save Category Limits'}
              </Button>
            )}
          </form>
        )}
      </CardContent>
      <DiscardEditsDialog
        open={pendingMonth !== null}
        count={dirtyCount}
        destinationLabel={
          pendingMonth === null ? '' : MONTH_NAMES_FULL[pendingMonth - 1]
        }
        onCancel={() => setPendingMonth(null)}
        onConfirm={() => {
          if (pendingMonth !== null) setMonth(pendingMonth);
          setPendingMonth(null);
        }}
      />
      <DiscardEditsDialog
        open={pendingYear !== null}
        count={dirtyCount}
        destinationLabel={pendingYear === null ? '' : String(pendingYear)}
        onCancel={() => setPendingYear(null)}
        onConfirm={() => {
          if (pendingYear !== null) setYear(pendingYear);
          setPendingYear(null);
        }}
      />
    </Card>
  );
}

/* ---------- Budgets page ---------- */

export function Budgets() {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const navigate = useNavigate();

  // Per-section dirtyCount mirrored two ways:
  //  * `…Ref` is read by the capture-phase anchor-click listener
  //    (cheap, no re-render per keystroke).
  //  * `…Dirty` state drives the popstate-sentinel effect (needs
  //    reactivity to install/uninstall on dirty/clean transitions)
  //    and the discard-dialog's count display (reading refs at
  //    render trips react-hooks/refs lint).
  // Sections call onDirtyChange(count) and write dirtyCountRef.current
  // from the same effect.
  const monthlyDirtyCountRef = useRef(0);
  const categoryLimitsDirtyCountRef = useRef(0);
  const [monthlyDirty, setMonthlyDirty] = useState(0);
  const [catLimitsDirty, setCatLimitsDirty] = useState(0);
  const dirtyCount = monthlyDirty + catLimitsDirty;
  const isDirty = dirtyCount > 0;

  // Parked target while the discard-edits dialog is open. Storing the
  // target href (rather than a boolean) lets the same dialog name the
  // destination and commit the change on "Discard".
  const [pendingHref, setPendingHref] = useState<string | null>(null);
  // Separate trigger for the browser-back / forward / swipe-back
  // discard prompt. Distinct from `pendingHref` because we can't
  // determine the *href* the user navigated to from a popstate event
  // (history.state may be ours, the target URL is gone) — we just know
  // they tried to leave. The two dialogs are siblings and never open
  // simultaneously (different trigger paths).
  const [pendingBack, setPendingBack] = useState(false);
  // True while we're programmatically calling history.go(-2) on
  // Discard. The popstate handler bails on the inbound event so we
  // don't re-prompt ourselves into a loop.
  const selfNavRef = useRef(false);

  // Guard in-app navigation (e.g. sidebar NavLinks) when the page has
  // unsaved budget edits. The app uses <BrowserRouter> — not the data
  // router — so react-router v7's `useBlocker` isn't available. We
  // intercept at the DOM level instead: a capture-phase document
  // listener inspects anchor clicks before React Router's own onClick
  // handler runs, which is early enough to preventDefault and park the
  // target in `pendingHref`.
  //
  // Scope / filters:
  //   - Always active while this page is mounted (the page IS what was
  //     the old Settings General tab — no tab-scope check needed).
  //   - Only primary-button, unmodified clicks — ctrl/cmd/shift/middle
  //     are "open in new tab/window" and the user has not asked to
  //     leave the current view.
  //   - Only same-origin anchors whose pathname differs from current.
  //     Hash links, external links, and `target="_blank"` downloads
  //     pass through unguarded.
  useEffect(() => {
    function handler(e: MouseEvent) {
      if (e.defaultPrevented || e.button !== 0) return;
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const target = e.target as Element | null;
      if (!target || typeof target.closest !== 'function') return;
      const anchor = target.closest('a[href]') as HTMLAnchorElement | null;
      if (!anchor) return;
      if (anchor.target === '_blank') return;
      const rawHref = anchor.getAttribute('href');
      if (!rawHref || rawHref.startsWith('#') || rawHref.startsWith('mailto:')) {
        return;
      }
      const url = new URL(anchor.href, window.location.href);
      if (url.origin !== window.location.origin) return;
      if (url.pathname === window.location.pathname) return;
      if (
        monthlyDirtyCountRef.current <= 0 &&
        categoryLimitsDirtyCountRef.current <= 0
      )
        return;
      e.preventDefault();
      e.stopPropagation();
      setPendingHref(url.pathname + url.search + url.hash);
    }
    document.addEventListener('click', handler, true);
    return () => document.removeEventListener('click', handler, true);
  }, []);

  // Friendly label for the intercepted route. Falls back to the raw
  // pathname when a route we don't know about shows up, so the dialog
  // is still readable — just less polished.
  const pendingHrefLabel = useMemo(() => {
    if (pendingHref === null) return '';
    const pathOnly = pendingHref.split(/[?#]/)[0];
    return ROUTE_LABELS[pathOnly] ?? pathOnly;
  }, [pendingHref]);

  // Popstate sentinel: only active while editors are dirty so a fresh
  // visit to /budgets doesn't pollute history for users who never
  // edit. The duplicate history entry catches browser Back / Forward
  // / mobile swipe-back; on Discard we go(-2) through both the
  // sentinel and the original /budgets entry. The capture-phase click
  // listener above handles in-app sidebar/NavLink navigation;
  // together they cover sidebar + browser toolbar + mobile gestures.
  // Edge cases (Forward through Discard, history.go to an arbitrary
  // index) are not handled — Discard always sends back exactly two
  // steps from the current position.
  useEffect(() => {
    if (!isDirty) return;
    window.history.pushState({ spendropBudgetsGuard: true }, '');

    function handler() {
      if (selfNavRef.current) {
        selfNavRef.current = false;
        return;
      }
      // Re-push so a subsequent Back also prompts (otherwise the user
      // could escape after the first Cancel).
      window.history.pushState({ spendropBudgetsGuard: true }, '');
      setPendingBack(true);
    }

    window.addEventListener('popstate', handler);
    return () => {
      window.removeEventListener('popstate', handler);
      // Clean up the sentinel we pushed so the user's next Back leaves
      // /budgets in one press, not two. The listener is already detached
      // above so the popstate this fires has no handler — no recursion.
      // Guarded on `spendropBudgetsGuard` so we only pop when WE own the
      // current state entry (a programmatic navigate between effect runs
      // shouldn't have its history touched).
      if (
        typeof window !== 'undefined' &&
        (window.history.state as { spendropBudgetsGuard?: boolean } | null)
          ?.spendropBudgetsGuard
      ) {
        window.history.back();
      }
    };
  }, [isDirty]);

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Budgets</h1>
      <MonthlyBudgetsSection
        dirtyCountRef={monthlyDirtyCountRef}
        onDirtyChange={setMonthlyDirty}
      />
      <CategoryLimitsSection
        admin={admin}
        dirtyCountRef={categoryLimitsDirtyCountRef}
        onDirtyChange={setCatLimitsDirty}
      />
      <DiscardEditsDialog
        open={pendingHref !== null}
        count={dirtyCount}
        destinationLabel={pendingHrefLabel}
        onCancel={() => setPendingHref(null)}
        onConfirm={() => {
          const href = pendingHref;
          // Clear *before* navigating: navigate() unmounts this
          // component and fires the listener cleanup, so there's no
          // re-render after this point to flush the "null" state.
          setPendingHref(null);
          if (href !== null) navigate(href);
        }}
      />
      <DiscardEditsDialog
        open={pendingBack}
        count={dirtyCount}
        destinationLabel="the previous page"
        onCancel={() => setPendingBack(false)}
        onConfirm={() => {
          setPendingBack(false);
          // Flag the next popstate as self-issued so the handler
          // doesn't re-prompt before the navigation completes.
          selfNavRef.current = true;
          // -2: through the sentinel we pushed AND the original
          // /budgets history entry, landing where the user actually
          // wanted to go (one step back from where Budgets opened).
          window.history.go(-2);
        }}
      />
    </div>
  );
}
