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
import { PLANNING_MIN_YEAR, PLANNING_MAX_YEAR, MONTH_NAMES_FULL } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useIsMobileViewport } from '@/hooks/useIsMobileViewport';
import { isAdmin } from '@/lib/roles';
import { Skeleton } from '@/components/ui/skeleton';
import { TYPE_EXPENSE } from '@/lib/transaction-types';
import { destructiveActionClass } from '@/lib/styles';

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

/* ---------- Shared design tokens ---------- */

// Amber is the canonical "attention without alarm" register per
// docs/DESIGN_GUIDE.md (see `Alert variant="warning"`). Centralized
// here so a future palette tweak (or migration to a future
// `--warning` semantic token) is one edit, not a grep-and-replace
// across every dirty-count indicator.
const ATTENTION_TEXT_CLASS = 'text-amber-600 dark:text-amber-500';

/**
 * What a read-only row shows for one entry of an edit buffer.
 *
 * The `raw &&` is load-bearing rather than defensive: `Number('')` is 0, which
 * is finite, so without it an unbudgeted month would render "$0.00" — a claim
 * the user never made. A non-numeric amount (a future payload bug) degrades to
 * the same em-dash instead of "$NaN".
 *
 * Shared by all FOUR read-only sites — two tables and two card lists. The two
 * presentations of one row have to agree on what "no value" looks like, and a
 * copy per site is how one of them ends up rendering an empty cell while the
 * other renders a dash.
 */
function readOnlyAmount(raw: string | undefined, currency: string): string {
  return raw && Number.isFinite(Number(raw))
    ? formatCurrency(Number(raw), currency)
    : '—';
}

/**
 * The register both card lists on this page share, written once because the
 * two of them sit on the same screen and a divergence would read as an
 * accident.
 *
 * ROW LABEL (`<Label>` for an admin, `<dt>` for a member): muted and
 * `font-medium` at `text-sm` — the same register this page's own field labels
 * already use (`Year`, `Month`, `Set all months`) and the same one the app's
 * other card `<dl>`s use for a `<dt>`. The label is not the payload; the
 * amount beside it is, and it takes the foreground.
 *
 * VALUE: `text-sm font-mono tabular-nums`, right-aligned, mirroring the
 * `text-right` the table gives both Amount columns. `text-sm` has to be
 * written out here where the table got it for free from `<table class="…
 * text-sm">`; without it the money renders a size larger than the label
 * beside it.
 *
 * WHY A `<dl>` AND NOT TWO SPANS: a card drops the column headers, so the
 * month (or category) is the only thing left naming the figure next to it.
 * `<dt>`/`<dd>` says that in markup, for sighted and screen-reader users at
 * once, which an `sr-only` prefix would have done for only one of them.
 */
const CARD_ROW_LABEL_CLASS = 'text-sm font-medium text-muted-foreground';
const CARD_ROW_VALUE_CLASS = 'text-sm font-mono tabular-nums';

/* ---------- Shared: discard-edits confirm dialog ---------- */

interface DiscardEditsDialogProps {
  open: boolean;
  count: number;
  destinationLabel: string;
  /**
   * Phrasing register. `'navigation'` (default) reads "Leaving for X"
   * for sidebar/browser-back interruptions; `'switch'` reads
   * "Switching to X" for the year/month dropdowns inside this page
   * — the user isn't leaving the page, just swapping context.
   */
  mode?: 'navigation' | 'switch';
  onCancel: () => void;
  onConfirm: () => void;
}

/**
 * Shadcn-styled replacement for the native `window.confirm` we used to
 * pop when a user tried to leave the Budgets page (year dropdown, month
 * dropdown, sidebar navigation, or browser-back) with unsaved budget
 * edits. One component for all call sites; the body reads "Discard
 * <N> unsaved change(s)? <Leaving for | Switching to> <destinationLabel>
 * will lose them."
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
  mode = 'navigation',
  onCancel,
  onConfirm,
}: DiscardEditsDialogProps) {
  const verb = mode === 'switch' ? 'Switching to' : 'Leaving for';
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
            . {verb}{' '}
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
            className={destructiveActionClass}
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
// goal tracking). Lower bound is `PLANNING_MIN_YEAR` so historical xlsx imports
// that predate the current year still have a landing spot.
const BUDGET_YEARS_AHEAD = 5;

/**
 * One month as a card row — the below-`md` presentation of the row the monthly
 * table renders.
 *
 * Two columns do not survive a 360px viewport, which is not obvious until you
 * measure it: measured on the built container, the table wanted 556px inside a
 * 345px box, so 211px of it sat behind a horizontal scroll — and what was out
 * there was the Amount column, the only thing on this surface you can do
 * anything with. The page itself does not pan, which is what makes this easy to
 * miss: the overflow is inside the table's own `overflow-auto` wrapper.
 *
 * ANATOMY: the row stays horizontal — label left, value right — because a month
 * name is a bounded one-word token (`September` is the longest at ~72px) and
 * twelve of these are scrolled past in one gesture. `<CategoryLimitCard>` below
 * stacks instead, and the difference is the identity: a category name is
 * user-supplied up to 100 characters, so it cannot share a row with a field.
 *
 * `min-w-0` ON THE INPUT IS NOT COSMETIC. A flex item's automatic minimum is
 * its min-content size, and for an `<input>` that is its intrinsic width from
 * the `size` attribute (~177px at the default 20) — not zero. Without it the
 * field refuses to shrink below that, and a narrow enough viewport gets the
 * exact in-card pan this card list exists to remove.
 */
function MonthlyBudgetCard({
  month,
  name,
  year,
  baseCurrency,
  admin,
  saving,
  value,
  onChange,
}: {
  month: number;
  name: string;
  year: number;
  baseCurrency: string;
  admin: boolean;
  saving: boolean;
  value: string;
  onChange: (month: number, next: string) => void;
}) {
  const fieldId = `budget-month-${year}-${month}`;
  return (
    <li className="flex items-center justify-between gap-3 p-4">
      {admin ? (
        <>
          {/*
            The visible label is the month alone; the year and the currency —
            which the table carried in every row's `aria-label` and in its
            `Amount (USD)` column header — follow it for a screen reader only.
            Repeating "2026 in USD" twelve times on screen is noise, but a
            forms-mode reader hears ONLY the field's own name, with no
            surrounding card or picker for context, so dropping it there would
            have left twelve fields called "April" through "December" and no
            year among them. A real `<Label>` rather than an `aria-label`: an
            `aria-label` beside visible label text overrides it and orphans
            what is on screen.
          */}
          <Label htmlFor={fieldId} className={`shrink-0 ${CARD_ROW_LABEL_CLASS}`}>
            {name}{' '}
            <span className="sr-only">
              budget for {year} in {baseCurrency}
            </span>
          </Label>
          {/* No `max-w-[160px]`: that cap is what a two-column table row can
              spare, and it is also what made this the column that got clipped.
              The row gives the field everything the label does not need. The
              44px touch floor comes from the `Input` primitive's own
              `coarse:min-h-11` — see `lib/touch-target.ts`.

              `inputMode="decimal"` alongside `type="number"` because this card
              exists ONLY on a phone, where the two attributes answer different
              questions: `type` is what the value is, `inputMode` is which
              keypad opens for it. Android in particular does not reliably infer
              a decimal keypad from `type="number"` alone, and every amount here
              has cents. Pairs with the app's newest numeric field, the
              large-transaction threshold in `Settings.tsx`. */}
          <Input
            id={fieldId}
            type="number"
            step="0.01"
            min="0"
            inputMode="decimal"
            placeholder="0.00"
            value={value}
            onChange={(e) => onChange(month, e.target.value)}
            onFocus={selectAllOnFocus}
            disabled={saving}
            className="min-w-0 text-right"
          />
        </>
      ) : (
        <dl className="flex flex-1 items-baseline justify-between gap-3">
          <dt className={CARD_ROW_LABEL_CLASS}>{name}</dt>
          <dd className={CARD_ROW_VALUE_CLASS}>
            {readOnlyAmount(value, baseCurrency)}
          </dd>
        </dl>
      )}
    </li>
  );
}

interface MonthlyBudgetsSectionProps {
  // Editing/saving is admin-only because the backend PUT *and* DELETE for
  // budgets reject non-admins. Non-admins still get the read view (the plan
  // matters to everyone who spends against it), but the dollar fields render
  // as static text and every write control is hidden so they can't trigger a
  // request that would 403. Mirrors <CategoryLimitsSectionProps>.admin.
  //
  // Without the gate a member could stage edits she can never save — and once
  // clearing a month began issuing a real DELETE, a cleared row also counted
  // toward dirtyCount, so she got unsaved-changes prompts with no way to
  // discharge them.
  admin: boolean;
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
  admin,
  dirtyCountRef,
  onDirtyChange,
}: MonthlyBudgetsSectionProps) {
  const baseCurrency = useBaseCurrency();
  const isMobile = useIsMobileViewport();
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

  // `currentYear + N … PLANNING_MIN_YEAR`, descending. Memoized not for perf
  // (32 items) but to keep `new Date()` out of render — two renders
  // crossing a New Year midnight would otherwise produce different
  // option arrays and reset Radix's Select focus state.
  const yearSelectOptions = useMemo(() => {
    const opts: number[] = [];
    for (let y = initialYear + BUDGET_YEARS_AHEAD; y >= PLANNING_MIN_YEAR; y--) {
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
    if (prev < PLANNING_MIN_YEAR) return;
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

    // Bucket the 12 rows into pending (PUT), cleared (DELETE), invalid
    // (block), unchanged (skip) — the same four buckets the Category Limits
    // editor below uses. O(1) baseline lookups via the ref; comparison is on
    // strings, see the `baselineRef` comment for rationale.
    const pending: { month: number; amount: number }[] = [];
    const clearedMonths: number[] = [];
    const invalidMonths: number[] = [];
    for (let m = 1; m <= 12; m++) {
      const raw = editAmounts[m] ?? '';
      const baseline = baselineRef.current[m] ?? '';
      if (raw === '') {
        // Was set, now cleared → DELETE. The baseline check is LOAD-BEARING,
        // not an assertion: a month that was never budgeted also has
        // raw === '' (the raw === baseline skip only runs further down), so
        // without it every empty month would stage a spurious DELETE — the
        // test pins 11 of them. Clearing is not the same as budgeting zero:
        // a month with no row falls back to the household default_budget in
        // the Reports budget-vs-actual table, which is exactly what the user
        // is asking for. PUT cannot express it — it rejects amount <= 0 — so
        // the delete verb is the only way to say it.
        if (baseline !== '') clearedMonths.push(m);
        continue;
      }
      const n = Number(raw);
      // Backend rejects amount <= 0; surface it client-side with a
      // concrete per-row error instead of silently dropping.
      if (!Number.isFinite(n) || n <= 0) {
        invalidMonths.push(m);
        continue;
      }
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

    const clearedNames = clearedMonths
      .map((m) => MONTH_NAMES_FULL[m - 1])
      .join(', ');

    if (pending.length === 0 && clearedMonths.length === 0) {
      toast.info('No changes to save');
      return;
    }

    setSaving(true);
    // Track the month currently in flight so a mid-loop failure can point the
    // user at the row that broke, rather than a generic error. Covers the
    // DELETE pass too — a clear that fails needs naming just as much as a save.
    let failedMonth: number | null = null;
    try {
      for (const { month, amount } of pending) {
        failedMonth = month;
        await api.put(`budgets/${year}/${month}`, { amount });
      }
      for (const month of clearedMonths) {
        failedMonth = month;
        await api.del(`budgets/${year}/${month}`);
      }
      failedMonth = null;
      // Name the fallback explicitly. "Cleared January" alone reads as "January
      // now has no budget", but Reports will compare that month against the
      // household default — the user should not have to discover that there.
      let msg = '';
      if (pending.length > 0) {
        msg = `Saved ${pending.length} budget${pending.length === 1 ? '' : 's'}`;
      }
      if (clearedMonths.length > 0) {
        const cleared =
          clearedMonths.length === 1
            ? `Cleared ${clearedNames} — that month now uses the household default budget`
            : `Cleared ${clearedNames} — those months now use the household default budget`;
        msg = msg ? `${msg}. ${cleared}` : cleared;
      }
      toast.success(msg);
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
  // AND would produce a valid PUT or DELETE at save time — mirroring the
  // bucketing logic in `handleSave`. The count drives the "(N)" badge on the
  // Save button and the year-change / beforeunload confirms, so it has to
  // match "what would actually save" rather than "what's visually different".
  //
  // A CLEARED row counts. It did not use to, on the stated grounds that no
  // DELETE was issued for it so it could not be lost by navigating away —
  // true then, wrong now that clearing really unsets the month. Leaving it
  // out would let the user walk away from a pending clear with no prompt and
  // no "(N)" on the button. Same rule as the Category Limits editor below.
  //
  // `baselineRef.current` is read on every render; React re-renders when
  // `editAmounts` changes, and every write to `baselineRef.current` in
  // `fetchBudgets` is paired with a `setEditAmounts`, so this memo always
  // sees the latest ref value.
  //
  // The `react-hooks/refs` suppressions below are the price of that
  // deliberate design, not an oversight. The baseline is intentionally
  // non-reactive — see the ref's own comment above — while the count it
  // feeds is user-visible on the Save button, so it has to be derived
  // during render. The pairing invariant is what makes the read safe, and
  // it is enforced by keeping every `baselineRef.current = …` write inside
  // `fetchBudgets` / its catch. Scoped to these two reads; the rest of the
  // file keeps the rule.
  const dirtyCount = useMemo(() => {
    let count = 0;
    for (let m = 1; m <= 12; m++) {
      const raw = editAmounts[m] ?? '';
      // eslint-disable-next-line react-hooks/refs
      const baseline = baselineRef.current[m] ?? '';
      // eslint-disable-next-line react-hooks/refs
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
    if (!Number.isInteger(next) || next < PLANNING_MIN_YEAR || next > PLANNING_MAX_YEAR) {
      return;
    }
    if (next === year) return;
    if (dirtyCount > 0) {
      setPendingYear(next);
      return;
    }
    setYear(next);
  }

  const copyPrevDisabled = saving || year <= PLANNING_MIN_YEAR;
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
          {/* Write controls. The year picker above stays for everyone — it
              navigates the read view — but Set-all and Copy-from only stage
              edits a member could never save. */}
          {admin && (
            <>
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
            </>
          )}
          <div className="flex flex-col gap-1 sm:ml-auto sm:items-end">
            <span className="text-sm text-muted-foreground">Annual total</span>
            {/* `font-mono` with `tabular-nums`, not `tabular-nums` alone: the
                pair is how every currency read-cell in this app renders (both
                tables below, and `CARD_ROW_VALUE_CLASS` for the cards), and
                `tabular-nums` on a proportional face only equalizes DIGIT
                widths — the separators and the symbol still shift, which is
                what this figure does as the user types. `font-medium` survives
                alongside it; weight and family are different properties. */}
            <span
              className="text-sm font-medium font-mono tabular-nums"
              aria-label="Annual total"
            >
              {formatCurrency(annualTotal, baseCurrency)}
            </span>
          </div>
        </div>
        {/* `flex-wrap` so the row breaks on narrow viewports rather than
            crushing the badge into the dirty-count span; `sm:ml-auto` on the
            dirty-count pushes it to the right edge on >= sm so the bulk-change
            pill stays anchored left. The wrapper is always mounted so the
            aria-live region below is in the DOM before its content changes —
            assistive tech needs that to announce the first 0->1 transition. */}
        <div className="flex flex-wrap items-center gap-3">
          {admin && preBulkSnapshot !== null && (
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
          {admin && (
            // Always-mounted live region (gated only by admin so the region
            // exists before the first dirty edit); only the text is
            // conditional so AT observes a content mutation rather than a
            // node insertion on 0->1. Mirrors the Category Limits indicator.
            <span
              className={`text-sm sm:ml-auto ${dirtyCount > 0 ? ATTENTION_TEXT_CLASS : ''}`}
              aria-live="polite"
              data-testid="budget-dirty-indicator"
            >
              {dirtyCount > 0 ? `${dirtyCount} unsaved change${dirtyCount === 1 ? '' : 's'}` : ''}
            </span>
          )}
        </div>
      </CardHeader>
      <CardContent>
        <form
          onSubmit={(e) => void handleSave(e)}
          className="flex flex-col gap-4"
          noValidate
        >
          {/*
            --- The presentation fork ------------------------------------
            ONE tree or the other, chosen in JS — never `md:hidden` on two.
            Both presentations render the same twelve FIELDS, so mounting both
            would put two controls for April in the document and submit
            whichever the browser reached last; `display: none` drops the loser
            from the a11y tree but never from React or from the form. See
            `useIsMobileViewport`.

            Inset inside `CardContent` rather than running to the card's edges,
            which is the call `<CurrenciesSection>` makes for the same shape:
            this list shares its `CardContent` with the Save button below it, so
            reaching the edges would mean negative margins that the sibling
            control does not want.
          */}
          {isMobile ? (
            /* Tailwind's preflight strips the list-style and Safari/VoiceOver
               drop the list role with the marker — hence the explicit `role`. */
            <ul
              role="list"
              aria-label="Monthly budgets"
              className="flex flex-col divide-y divide-border rounded-md border"
            >
              {MONTH_NAMES_FULL.map((name, idx) => (
                <MonthlyBudgetCard
                  key={idx + 1}
                  month={idx + 1}
                  name={name}
                  year={year}
                  baseCurrency={baseCurrency}
                  admin={admin}
                  saving={saving}
                  value={editAmounts[idx + 1] ?? ''}
                  onChange={(month, next) =>
                    setEditAmounts((prev) => ({ ...prev, [month]: next }))
                  }
                />
              ))}
            </ul>
          ) : (
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
                        {admin ? (
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
                        ) : (
                          <span className="font-mono tabular-nums">
                            {readOnlyAmount(editAmounts[month], baseCurrency)}
                          </span>
                        )}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
          {admin && (
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
          )}
        </form>
      </CardContent>
      <DiscardEditsDialog
        open={pendingYear !== null}
        count={dirtyCount}
        mode="switch"
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

/**
 * The width bound for a category name on a card.
 *
 * A name is user-supplied and capped only by the server's
 * `MaxCategoryNameLength` = 100 (`internal/api/limits.go`), so it is the one
 * string on this surface that can pan a phone sideways on its own.
 * `overflow-wrap:anywhere` is what bounds the WIDTH — Tailwind's `break-words`
 * breaks a run for painting but leaves the element's min-content contribution
 * at the full token width — and it needs a `min-w-0` ancestor to bind, since a
 * flex item's automatic minimum is `min-content`. Same pair, same reasoning, as
 * `CLAMPED_CATEGORY_NAME` on the Categories page; no `line-clamp` here because
 * the name is the label for the field under it and a truncated one would leave
 * the user editing a limit for a category they cannot fully read.
 *
 * `leading-5` IS THE THIRD MEMBER OF THAT SET, and it is a PIN rather than a
 * fix — the distinction matters, so here is the measurement.
 *
 * shadcn's `Label` base is `text-sm font-medium leading-none`
 * (`components/ui/label.tsx`). Line-height 1 is right for the one-line labels
 * it was written for and would set a WRAPPED name solid — at 14px the lines
 * touch and an emoji, whose ink overruns the em box, collides with the line
 * above. It never reached the DOM here: `Label` composes through `cn()`, and
 * tailwind-merge's conflict table has `font-size` override `leading`, so the
 * `text-sm` this call site passes in `CARD_ROW_LABEL_CLASS` DELETES the
 * preceding `leading-none`. Measured against the project's own `cn`:
 *
 *     cn('leading-none', 'text-sm')                    -> 'text-sm'
 *     cn(<Label base>, 'text-sm … min-w-0 …')          -> no leading-* at all
 *
 * So both role views were already at `text-sm`'s own 1.25rem, and they agreed.
 * What they agreed by was a dependency's conflict table plus the accident that
 * this constant happens to lead with `text-sm` — drop that token in a future
 * refactor and `leading-none` comes back, on the one string on this page that
 * can wrap. `leading-5` states the intent instead of inheriting it.
 *
 * `leading-5` (1.25rem) rather than `leading-normal` (1.5) so the pin agrees
 * with what is already rendering: the member's `<dt>` composes by string
 * concatenation rather than `cn()`, so both `text-sm` and `leading-5` reach the
 * DOM there, and because the two carry the SAME value it does not matter which
 * one Tailwind emits last. Zero pixels change on either view. Same trick as the
 * touch floor in `lib/touch-target.ts`: agree with the thing you are overriding
 * on the VALUE, and stylesheet emission order stops being load-bearing.
 */
const WRAPPED_CATEGORY_NAME = 'min-w-0 leading-5 [overflow-wrap:anywhere]';

/**
 * One expense category as a stacked card — the below-`md` presentation of the
 * row the Category Limits table renders.
 *
 * Same measured defect as `<MonthlyBudgetCard>`: a 556px table inside a 345px
 * box, with the Limit column — the only editable thing here — 211px out past
 * the edge of its own scroll wrapper.
 *
 * ANATOMY: stacked, not the horizontal row the monthly card uses, and the
 * reason is the identity. A category name is user-supplied up to 100
 * characters; sharing a row with a field would leave it ~139px of a ~244px
 * card, i.e. two clamped lines of a name the user needs whole to know which
 * limit they are typing. Stacking gives the name the full width and the field
 * the full width under it — the shape `<CurrencyCard>` uses for the same
 * "identity plus one editable value" row.
 */
function CategoryLimitCard({
  category,
  monthLabel,
  year,
  baseCurrency,
  admin,
  saving,
  value,
  onChange,
}: {
  category: Category;
  monthLabel: string;
  year: number;
  baseCurrency: string;
  admin: boolean;
  saving: boolean;
  value: string;
  onChange: (categoryId: number, next: string) => void;
}) {
  const fieldId = `category-limit-${category.id}`;
  const icon = category.icon && (
    <span className="mr-2" aria-hidden="true">
      {category.icon}
    </span>
  );
  return (
    <li className="flex flex-col gap-1.5 p-4">
      {admin ? (
        <>
          {/* The card's identity line IS the field's label — see
              `<MonthlyBudgetCard>` for why the month/year/currency ride along
              `sr-only` rather than on screen. Unique per row, which a bare
              "Limit" repeated down twenty cards would not be: a screen-reader
              user would tab through twenty identically-named fields. The icon
              stays `aria-hidden` exactly as the table has it, so it decorates
              the line without entering the accessible name. */}
          <Label
            htmlFor={fieldId}
            className={`${CARD_ROW_LABEL_CLASS} ${WRAPPED_CATEGORY_NAME}`}
          >
            {icon}
            {category.name}{' '}
            <span className="sr-only">
              limit for {monthLabel} {year} in {baseCurrency}
            </span>
          </Label>
          {/* Full width, unlike the table's `max-w-[160px]`: that cap is what a
              two-column row can spare, and it is also what made this the column
              that got clipped. A card has the whole width to give. `text-right`
              mirrors the alignment the table's Limit column has, and
              `inputMode` opens the decimal keypad — see `<MonthlyBudgetCard>`
              for why `type="number"` alone does not. */}
          <Input
            id={fieldId}
            type="number"
            step="0.01"
            min="0"
            inputMode="decimal"
            placeholder="No limit"
            value={value}
            onChange={(e) => onChange(category.id, e.target.value)}
            onFocus={selectAllOnFocus}
            disabled={saving}
            className="text-right"
          />
        </>
      ) : (
        /* A member has no field, so the value fits beside the name and the card
           collapses to the same one-line shape the monthly card has. */
        <dl className="flex items-baseline justify-between gap-3">
          <dt className={`${CARD_ROW_LABEL_CLASS} ${WRAPPED_CATEGORY_NAME}`}>
            {icon}
            {category.name}
          </dt>
          <dd className={`shrink-0 ${CARD_ROW_VALUE_CLASS}`}>
            {readOnlyAmount(value, baseCurrency)}
          </dd>
        </dl>
      )}
    </li>
  );
}

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
  const isMobile = useIsMobileViewport();
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
    for (let y = initialYear + BUDGET_YEARS_AHEAD; y >= PLANNING_MIN_YEAR; y--) {
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
    if (!Number.isInteger(next) || next < PLANNING_MIN_YEAR || next > PLANNING_MAX_YEAR) return;
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
  //
  // Same reason as <MonthlyBudgetsSection>'s `dirtyCount` but a different
  // suppression set: the baseline is deliberately non-reactive while the
  // count it feeds is user-visible, so the read has to happen during
  // render — here the ref read flows through `.trim()`, which moves the
  // analyzer's report onto the memo itself, hence one `refs` directive
  // plus `preserve-manual-memoization` instead of Monthly's two `refs`.
  // `preserve-manual-memoization` is the downstream consequence — React
  // Compiler cannot keep a memo whose inputs it cannot see — and dropping
  // the `useMemo` instead is not an option: this count also gates the
  // year/month pickers and the nav guard.
  // eslint-disable-next-line react-hooks/preserve-manual-memoization
  const dirtyCount = useMemo(() => {
    let count = 0;
    for (const cat of expenseCategories) {
      const raw = (editAmounts[cat.id] ?? '').trim();
      // eslint-disable-next-line react-hooks/refs
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
          {/*
            The currency belongs in the description because it has nowhere else
            to be below `md`. The table states it in a column header
            (`Limit ({baseCurrency})`) that the card list drops, and unlike the
            Monthly section — whose header still shows `Set all months (USD)` —
            this one leaves an admin on a phone with no visible currency at all.
            A member is fine either way: her values render through
            `formatCurrency` and carry the symbol. This household enters LBP and
            USD daily, so which one a bare "400" means is a real question.

            One shared line rather than a phone-only element: it costs the
            desktop a mild redundancy against its own column header, and buys
            the phone the datum outright.
          */}
          Set an optional monthly spending limit per expense category.
          Leave a field blank for no limit. Amounts are in {baseCurrency}.
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
          {admin && (
            // Always-mounted live region (gated only by admin so the region
            // exists before the first dirty edit); only the text is conditional
            // so assistive tech announces the first 0->1 transition.
            <span
              className={`self-end text-sm ${dirtyCount > 0 ? ATTENTION_TEXT_CLASS : ''}`}
              aria-live="polite"
              data-testid="category-limits-dirty-indicator"
            >
              {dirtyCount > 0 ? `${dirtyCount} unsaved change${dirtyCount === 1 ? '' : 's'}` : ''}
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
            {/* The same JS fork the monthly section above documents in full:
                one tree or the other, never two hidden with `md:`. */}
            {isMobile ? (
              <ul
                role="list"
                aria-label="Category limits"
                className="flex flex-col divide-y divide-border rounded-md border"
              >
                {expenseCategories.map((cat) => (
                  <CategoryLimitCard
                    key={cat.id}
                    category={cat}
                    monthLabel={monthLabel}
                    year={year}
                    baseCurrency={baseCurrency}
                    admin={admin}
                    saving={saving}
                    value={editAmounts[cat.id] ?? ''}
                    onChange={(categoryId, next) =>
                      setEditAmounts((prev) => ({
                        ...prev,
                        [categoryId]: next,
                      }))
                    }
                  />
                ))}
              </ul>
            ) : (
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
                            {readOnlyAmount(editAmounts[cat.id], baseCurrency)}
                          </span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
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
        mode="switch"
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
        mode="switch"
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
        admin={admin}
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
