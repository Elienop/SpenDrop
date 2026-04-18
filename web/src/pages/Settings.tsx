import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import type { ChangeEvent, FormEvent, MutableRefObject } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import * as z from 'zod';
import { AlertCircle, CheckCircle2, CircleAlert, Upload } from 'lucide-react';
import { toast } from 'sonner';
import { formatDistanceToNowStrict } from 'date-fns';
import { api } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type {
  ApiToken,
  Budget,
  Category,
  CreateTokenResponse,
  Currency,
  ImportPreview,
  ListTokensResponse,
  PatchRowRequest,
  SavingsGoal,
  User,
} from '../api/types';
import { ImportPreviewTable } from '@/components/ImportPreviewTable';
import { useImportSession, type CellError } from '@/hooks/useImportSession';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn, selectAllOnFocus } from '@/lib/utils';
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui/tabs';
import { ButtonGroup } from '@/components/ui/button-group';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Separator } from '@/components/ui/separator';
import { MIN_YEAR, MAX_YEAR, MONTH_NAMES_FULL } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { ROLE_ADMIN, ROLE_MEMBER, isAdmin, type Role } from '@/lib/roles';

/* ---------- Module-scope constants ---------- */

const VALID_TABS = [
  'general',
  'currencies',
  'savings',
  'users',
  'api-tokens',
  'data',
] as const;
type SettingsTab = (typeof VALID_TABS)[number];

// User-facing label for each tab. Used in the discard-edits dialog so the
// prompt can name the destination ("Leaving for Currencies will lose
// them...") rather than showing a bare slug.
const TAB_LABELS: Record<SettingsTab, string> = {
  general: 'General',
  currencies: 'Currencies',
  savings: 'Savings',
  users: 'Users',
  'api-tokens': 'API tokens',
  data: 'Import / Export',
};

const EXPIRY_OPTIONS = {
  never: null,
  '7d': 7,
  '30d': 30,
  '90d': 90,
  '1y': 365,
} as const;

type ExpiryChoice = keyof typeof EXPIRY_OPTIONS;

function computeExpiresAt(choice: ExpiryChoice): string | null {
  const days = EXPIRY_OPTIONS[choice];
  if (days === null) return null;
  // Compute in UTC — the server validates `expires_at > now`, and a
  // tz-naive local-midnight string could land in the past for any user
  // east of UTC on the day the token is minted.
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString();
}

const createTokenSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, 'Name is required')
    .max(100, 'Name must be 100 characters or fewer'),
  expires: z.enum(['never', '7d', '30d', '90d', '1y']),
  password: z.string().min(1, 'Password is required'),
});
type CreateTokenValues = z.infer<typeof createTokenSchema>;

// User-facing label for each top-level route the sidebar links to. Used
// in the discard-edits dialog when a route change is intercepted — keeps
// the description friendly ("Leaving for Transactions") instead of
// dumping the raw pathname. Falls back to the pathname if a new route
// ever shows up without a label here.
const ROUTE_LABELS: Record<string, string> = {
  '/': 'Dashboard',
  '/transactions': 'Transactions',
  '/reports': 'Reports',
  '/categories': 'Categories',
  '/trash': 'Trash',
  '/settings': 'Settings',
};

/* ---------- Pure helpers ---------- */

function isValidTab(value: string | null): value is SettingsTab {
  return value !== null && (VALID_TABS as readonly string[]).includes(value);
}

// Match preview category names (case-insensitive) to existing category ids.
// Pure — captures no closure, hoisted for test-ability and perf.
function autoMapCategories(
  previewData: ImportPreview,
  cats: Category[],
): Record<string, string> {
  const map: Record<string, string> = {};
  const uniqueCategories = previewData.unique_categories ?? [];
  for (const catName of uniqueCategories) {
    const match = cats.find(
      (c) => c.name.toLowerCase() === catName.toLowerCase(),
    );
    if (match) {
      map[catName] = String(match.id);
    }
  }
  return map;
}

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
 * pop when a user tried to leave the General tab (year dropdown, tab
 * switch, or sidebar navigation) with unsaved budget edits. One dialog
 * for all three call sites: the body reads "Discard <N> unsaved
 * change(s)? You are about to leave for <destinationLabel>." and the
 * destructive button wins focus so Enter commits (matching the muscle
 * memory of the old OS dialog). Mirrors <ConfirmPurgeDialog> in
 * Trash.tsx for visual consistency.
 */
function DiscardEditsDialog({
  open,
  count,
  destinationLabel,
  onCancel,
  onConfirm,
}: DiscardEditsDialogProps) {
  return (
    <Dialog
      open={open}
      onOpenChange={(isOpen) => {
        if (!isOpen) onCancel();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Discard unsaved budget changes?</DialogTitle>
          <DialogDescription>
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
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel}>
            Keep editing
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={onConfirm}
            autoFocus
          >
            Discard changes
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* ---------- General Tab ---------- */

// How far ahead of the current year the budget picker lets users plan.
// Keep small enough that the dropdown stays scannable but large enough
// to cover realistic forward budgeting (mortgage amortization, yearly
// goal tracking). Lower bound is `MIN_YEAR` so historical xlsx imports
// that predate the current year still have a landing spot.
const BUDGET_YEARS_AHEAD = 5;

interface GeneralSectionProps {
  // Parent-held mirror of `dirtyCount`. Lets the Settings-level tab
  // switcher consult the current dirty state *before* committing a tab
  // change (and before Radix unmounts this component and wipes the
  // in-progress edits with it). Passed as a ref rather than a callback
  // so keystrokes don't re-render the parent.
  dirtyCountRef?: MutableRefObject<number>;
}

function GeneralSection({ dirtyCountRef }: GeneralSectionProps) {
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

  // Mirror `dirtyCount` into the parent's ref so the Settings-level tab
  // switcher can guard tab changes. `TabsContent` unmounts inactive tabs
  // by default, so without this the user's in-progress edits vanish
  // silently when they switch to Currencies/Savings/etc. The cleanup
  // resets to 0 on unmount so a stale count can't block a future tab
  // change after this component is already gone.
  useEffect(() => {
    if (!dirtyCountRef) return;
    dirtyCountRef.current = dirtyCount;
    return () => {
      dirtyCountRef.current = 0;
    };
  }, [dirtyCount, dirtyCountRef]);

  // Block accidental browser close / reload while changes are unsaved.
  // The browser always shows its own generic prompt; the `returnValue`
  // assignment is the legacy handshake that triggers it on Chromium.
  // Note: this does NOT fire on in-app navigation (route changes or
  // Settings tab switches) — those are guarded separately.
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

/* ---------- Currencies Tab ---------- */

const newCurrencySchema = z.object({
  code: z.string().min(1, 'Code is required').max(3),
  name: z.string().min(1, 'Name is required'),
  symbol: z.string().min(1, 'Symbol is required').max(3),
  rate_to_base: z.number().positive('Rate must be positive'),
});
type NewCurrencyValues = z.infer<typeof newCurrencySchema>;

function CurrenciesSection() {
  const [currencies, setCurrencies] = useState<Currency[]>([]);
  const [editRates, setEditRates] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);

  const addForm = useForm<NewCurrencyValues>({
    resolver: zodResolver(newCurrencySchema),
    defaultValues: { code: '', name: '', symbol: '', rate_to_base: 0 },
  });

  const fetchCurrencies = useCallback(async () => {
    const data = await api.get<Currency[]>('currencies');
    setCurrencies(data);
    const rates: Record<string, string> = {};
    data.forEach((c) => {
      rates[c.code] = String(c.rate_to_base);
    });
    setEditRates(rates);
  }, []);

  useEffect(() => {
    fetchCurrencies().catch(() => {
      /* initial load failure is non-critical; table will show empty */
    });
  }, [fetchCurrencies]);

  async function handleSaveRates(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    try {
      for (const currency of currencies) {
        if (currency.is_base) continue;
        const newRateVal = editRates[currency.code];
        if (newRateVal && parseFloat(newRateVal) !== currency.rate_to_base) {
          await api.put(`currencies/${currency.code}`, {
            name: currency.name,
            symbol: currency.symbol,
            rate_to_base: parseFloat(newRateVal),
            is_base: currency.is_base,
          });
        }
      }
      toast.success('Rates updated successfully');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update');
    } finally {
      setSaving(false);
      // Re-sync with server truth whether or not the save loop threw. Saves
      // could have been partially applied before a later PUT failed; without
      // this refetch the table would keep showing stale local edits.
      fetchCurrencies().catch((err) => {
        toast.error(
          'Refresh failed: ' +
            (err instanceof Error ? err.message : 'unknown'),
        );
      });
    }
  }

  async function onAddCurrency(values: NewCurrencyValues) {
    try {
      await api.post('currencies', {
        code: values.code.toUpperCase(),
        name: values.name,
        symbol: values.symbol,
        rate_to_base: values.rate_to_base,
      });
      addForm.reset();
      toast.success('Currency added');
      fetchCurrencies().catch((err) => {
        toast.error(
          'Refresh failed: ' +
            (err instanceof Error ? err.message : 'unknown'),
        );
      });
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to add currency');
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Currencies</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <form
          onSubmit={(e) => void handleSaveRates(e)}
          className="flex flex-col gap-4"
          noValidate
        >
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Code</TableHead>
                <TableHead>Name</TableHead>
                <TableHead>Symbol</TableHead>
                <TableHead>Rate to Base</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {currencies.map((c) => (
                <TableRow key={c.code}>
                  <TableCell className="font-mono">{c.code}</TableCell>
                  <TableCell>{c.name}</TableCell>
                  <TableCell>{c.symbol}</TableCell>
                  <TableCell>
                    {c.is_base ? (
                      <span className="text-muted-foreground font-mono tabular-nums">
                        1.0000
                      </span>
                    ) : (
                      <Input
                        type="number"
                        step="0.0001"
                        min="0"
                        value={editRates[c.code] ?? ''}
                        onChange={(e) =>
                          setEditRates((prev) => ({
                            ...prev,
                            [c.code]: e.target.value,
                          }))
                        }
                        onFocus={selectAllOnFocus}
                        aria-label={`Rate for ${c.code}`}
                        className="max-w-[160px]"
                      />
                    )}
                  </TableCell>
                  <TableCell>
                    {c.is_base && <Badge variant="secondary">Base</Badge>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <Button type="submit" className="w-fit" disabled={saving}>
            {saving ? 'Saving...' : 'Save Rates'}
          </Button>
        </form>

        <Separator />
        <div className="flex flex-col gap-4">
          <h3 className="text-sm font-semibold">Add Currency</h3>
          <Form {...addForm}>
            <form
              onSubmit={(e) => void addForm.handleSubmit(onAddCurrency)(e)}
              className="grid gap-4 sm:grid-cols-2 md:grid-cols-4"
              noValidate
            >
              <FormField
                control={addForm.control}
                name="code"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Code</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder="GBP"
                        maxLength={3}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={addForm.control}
                name="name"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Name</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="British Pound" />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={addForm.control}
                name="symbol"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Symbol</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="\u00A3" maxLength={3} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={addForm.control}
                name="rate_to_base"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Rate to Base</FormLabel>
                    <FormControl>
                      <Input
                        type="number"
                        step="0.0001"
                        placeholder="0.79"
                        name={field.name}
                        onBlur={field.onBlur}
                        ref={field.ref}
                        value={field.value ?? ''}
                        onChange={(e) =>
                          field.onChange(
                            e.target.value === ''
                              ? 0
                              : Number(e.target.value),
                          )
                        }
                        onFocus={selectAllOnFocus}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <div className="sm:col-span-2 md:col-span-4">
                <Button type="submit">Add Currency</Button>
              </div>
            </form>
          </Form>
        </div>
      </CardContent>
    </Card>
  );
}

/* ---------- Savings Tab ---------- */

const goalSchema = z.object({
  year: z.number().int().min(MIN_YEAR).max(MAX_YEAR),
  target_amount: z.number().min(0),
});
type GoalValues = z.infer<typeof goalSchema>;

function SavingsSection() {
  const baseCurrency = useBaseCurrency();
  const [goals, setGoals] = useState<SavingsGoal[]>([]);
  const [addOpen, setAddOpen] = useState(false);

  const form = useForm<GoalValues>({
    resolver: zodResolver(goalSchema),
    defaultValues: {
      year: new Date().getFullYear(),
      target_amount: 0,
    },
  });

  const fetchGoals = useCallback(async () => {
    const data = await api.get<SavingsGoal[]>('savings-goals');
    setGoals(data);
  }, []);

  useEffect(() => {
    fetchGoals().catch(() => {
      /* initial load failure is non-critical; list will show empty */
    });
  }, [fetchGoals]);

  function refreshGoals() {
    fetchGoals().catch((err) => {
      toast.error(
        'Refresh failed: ' + (err instanceof Error ? err.message : 'unknown'),
      );
    });
  }

  async function onAdd(values: GoalValues) {
    try {
      await api.put(`savings-goals/${values.year}`, {
        target_amount: values.target_amount,
      });
      form.reset();
      setAddOpen(false);
      toast.success('Savings goal added');
      refreshGoals();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to add goal');
    }
  }

  async function handleDelete(goal: SavingsGoal) {
    try {
      await api.put(`savings-goals/${goal.year}`, {
        target_amount: 0,
      });
      toast.success('Savings goal removed');
      refreshGoals();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete');
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-base">Savings Goals</CardTitle>
        <Dialog open={addOpen} onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) form.reset();
        }}>
          <DialogTrigger asChild>
            <Button size="sm">Add Goal</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add Savings Goal</DialogTitle>
              <DialogDescription>
                Set a yearly savings target.
              </DialogDescription>
            </DialogHeader>
            <Form {...form}>
              <form
                onSubmit={(e) => void form.handleSubmit(onAdd)(e)}
                className="grid gap-4"
                noValidate
              >
                <FormField
                  control={form.control}
                  name="year"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Year</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          name={field.name}
                          onBlur={field.onBlur}
                          ref={field.ref}
                          value={field.value ?? ''}
                          onChange={(e) =>
                            field.onChange(
                              e.target.value === ''
                                ? 0
                                : Number(e.target.value),
                            )
                          }
                          onFocus={selectAllOnFocus}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="target_amount"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Target Amount</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          step="0.01"
                          placeholder="0.00"
                          name={field.name}
                          onBlur={field.onBlur}
                          ref={field.ref}
                          value={field.value ?? ''}
                          onChange={(e) =>
                            field.onChange(
                              e.target.value === ''
                                ? 0
                                : Number(e.target.value),
                            )
                          }
                          onFocus={selectAllOnFocus}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <DialogFooter>
                  <Button type="submit">Add Goal</Button>
                </DialogFooter>
              </form>
            </Form>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Year</TableHead>
              <TableHead>Target Amount</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {goals.map((g) => (
              <TableRow key={g.id}>
                <TableCell>{g.year}</TableCell>
                <TableCell className="font-mono tabular-nums">
                  {formatCurrency(g.target_amount, baseCurrency)}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    variant="destructive"
                    size="sm"
                    onClick={() => void handleDelete(g)}
                    aria-label={`Delete ${g.year} goal`}
                  >
                    Delete
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

/* ---------- Users Tab (Admin only) ---------- */

const newUserSchema = z.object({
  username: z.string().min(1, 'Username required'),
  password: z.string().min(1, 'Password required'),
  display_name: z.string().min(1, 'Display name required'),
  role: z.enum([ROLE_ADMIN, ROLE_MEMBER] as const),
});
type NewUserValues = z.infer<typeof newUserSchema>;

function UsersSection() {
  const [users, setUsers] = useState<User[]>([]);
  const [addOpen, setAddOpen] = useState(false);

  const form = useForm<NewUserValues>({
    resolver: zodResolver(newUserSchema),
    defaultValues: {
      username: '',
      password: '',
      display_name: '',
      role: ROLE_MEMBER,
    },
  });

  const fetchUsers = useCallback(async () => {
    const data = await api.get<User[]>('users');
    setUsers(data);
  }, []);

  useEffect(() => {
    fetchUsers().catch(() => {
      /* initial load failure is non-critical; list will show empty */
    });
  }, [fetchUsers]);

  function refreshUsers() {
    fetchUsers().catch((err) => {
      toast.error(
        'Refresh failed: ' + (err instanceof Error ? err.message : 'unknown'),
      );
    });
  }

  async function onAddUser(values: NewUserValues) {
    try {
      await api.post('users', {
        username: values.username,
        password: values.password,
        display_name: values.display_name,
        role: values.role,
      });
      form.reset();
      setAddOpen(false);
      toast.success('User added');
      refreshUsers();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to add user');
    }
  }

  async function handleRoleChange(userId: number, role: Role) {
    try {
      await api.put(`users/${userId}`, { role });
      toast.success('Role updated');
      refreshUsers();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update role');
    }
  }

  async function handleDeleteUser(userId: number) {
    try {
      await api.del(`users/${userId}`);
      toast.success('User deleted');
      refreshUsers();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete user');
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-base">Users</CardTitle>
        <Dialog open={addOpen} onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) form.reset();
        }}>
          <DialogTrigger asChild>
            <Button size="sm">Add User</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add User</DialogTitle>
              <DialogDescription>
                Create a new household member account.
              </DialogDescription>
            </DialogHeader>
            <Form {...form}>
              <form
                onSubmit={(e) => void form.handleSubmit(onAddUser)(e)}
                className="grid gap-4"
                noValidate
              >
                <FormField
                  control={form.control}
                  name="username"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Username</FormLabel>
                      <FormControl>
                        <Input {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="password"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Password</FormLabel>
                      <FormControl>
                        <Input type="password" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="display_name"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Display Name</FormLabel>
                      <FormControl>
                        <Input {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="role"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Role</FormLabel>
                      <Select
                        value={field.value}
                        onValueChange={(v) => {
                          if (v !== ROLE_ADMIN && v !== ROLE_MEMBER) return;
                          field.onChange(v);
                        }}
                      >
                        <FormControl>
                          <SelectTrigger aria-label="New user role">
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectGroup>
                            <SelectItem value={ROLE_ADMIN}>Admin</SelectItem>
                            <SelectItem value={ROLE_MEMBER}>Member</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <DialogFooter>
                  <Button type="submit">Add User</Button>
                </DialogFooter>
              </form>
            </Form>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Username</TableHead>
              <TableHead>Display Name</TableHead>
              <TableHead>Role</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.map((u) => (
              <TableRow key={u.id}>
                <TableCell className="font-mono">{u.username}</TableCell>
                <TableCell>{u.display_name}</TableCell>
                <TableCell>
                  <Select
                    value={u.role}
                    onValueChange={(v) => {
                      if (v !== ROLE_ADMIN && v !== ROLE_MEMBER) return;
                      void handleRoleChange(u.id, v);
                    }}
                  >
                    <SelectTrigger
                      aria-label={`Role for ${u.username}`}
                      className="w-[140px]"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectItem value={ROLE_ADMIN}>Admin</SelectItem>
                        <SelectItem value={ROLE_MEMBER}>Member</SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    variant="destructive"
                    size="sm"
                    onClick={() => void handleDeleteUser(u.id)}
                    aria-label={`Delete ${u.username}`}
                  >
                    Delete
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

/* ---------- API tokens tab ---------- */

function ShowOnceReveal({
  token,
  onClose,
}: {
  token: CreateTokenResponse;
  onClose: () => void;
}) {
  return (
    <>
      <DialogHeader>
        <DialogTitle>Token created</DialogTitle>
        <DialogDescription>
          This is the only time you'll see this token. Store it
          somewhere safe — you will not be able to retrieve it again.
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-3">
        <Label htmlFor="api-token-reveal">Your new API token</Label>
        <div className="flex items-center gap-2">
          <Input
            id="api-token-reveal"
            value={token.token}
            readOnly
            className="font-mono"
            onFocus={selectAllOnFocus}
          />
          <Button
            type="button"
            variant="outline"
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(token.token);
                toast.success('Copied');
              } catch {
                // navigator.clipboard.writeText rejects on insecure
                // contexts (common for self-hosted SpenDrop served
                // over HTTP on a LAN IP — localhost is treated as
                // secure, but `http://192.168.x.x` is not) and on
                // explicit permission denial. Either way the user
                // cannot rely on the button alone. Focus + select
                // the reveal <Input> so one Ctrl/Cmd+C still copies,
                // and tell them so via a toast. The dialog stays
                // open and the plaintext stays on screen — the user
                // does not have to re-trigger the Create flow.
                const revealInput = document.getElementById(
                  'api-token-reveal',
                ) as HTMLInputElement | null;
                if (revealInput) {
                  revealInput.focus();
                  revealInput.select();
                }
                toast.info(
                  'Press Ctrl/Cmd+C to copy \u2014 clipboard blocked in this context.',
                );
              }
            }}
          >
            Copy
          </Button>
        </div>
        <details className="rounded-md border p-3 text-sm">
          <summary className="cursor-pointer font-medium">
            Use with Homepage
          </summary>
          <pre className="mt-3 overflow-x-auto rounded bg-muted p-3 text-xs">
{`widget:
  type: customapi
  url: https://<your-spendrop>/api/homepage/summary
  refreshInterval: 30000
  method: GET
  display: list
  headers:
    Authorization: "Bearer ${token.token}"
  mappings:
    - { field: month_spent, label: This month, format: float, prefix: "$" }
    - { field: txn_count, label: Transactions, format: number }
    - field: month_remaining
      label: Remaining
      format: float
      prefix: "$"
      additionalField: { field: month_remaining, format: float, color: adaptive }
    - { field: over_budget_categories, label: Over budget, format: number }`}
          </pre>
        </details>
      </div>
      <DialogFooter>
        <Button type="button" onClick={onClose}>
          Done
        </Button>
      </DialogFooter>
    </>
  );
}

function ApiTokensSection() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [createdToken, setCreatedToken] = useState<CreateTokenResponse | null>(null);
  const [creating, setCreating] = useState(false);

  const createForm = useForm<CreateTokenValues>({
    resolver: zodResolver(createTokenSchema),
    defaultValues: { name: '', expires: 'never', password: '' },
  });

  const fetchTokens = useCallback(async () => {
    const data = await api.get<ListTokensResponse>('api-tokens');
    setTokens(data.tokens);
  }, []);

  useEffect(() => {
    fetchTokens().catch(() => {
      /* Initial load failure is non-critical; empty table renders and
         the user can retry by creating a token. */
    });
  }, [fetchTokens]);

  function formatLastUsed(t: ApiToken): string {
    if (!t.last_used_at) return 'Never used';
    const relative = formatDistanceToNowStrict(new Date(t.last_used_at), {
      addSuffix: true,
    });
    return t.last_used_ip ? `${relative} \u00b7 ${t.last_used_ip}` : relative;
  }

  function formatExpires(t: ApiToken): string {
    if (!t.expires_at) return 'Never';
    const d = new Date(t.expires_at);
    if (d.getTime() <= Date.now()) return 'Expired';
    return d.toLocaleDateString();
  }

  async function onCreate(values: CreateTokenValues) {
    setCreating(true);
    try {
      const body: CreateTokenResponse = await api.post<CreateTokenResponse>(
        'api-tokens',
        {
          name: values.name.trim(),
          expires_at: computeExpiresAt(values.expires),
          password: values.password,
        },
      );
      setCreatedToken(body);
      createForm.reset();
      // Optimistic update: the new row (sans full plaintext) slides
      // into the list behind the reveal dialog, so when the user closes
      // the reveal the list is already current. We assemble the list
      // row by picking the `ApiToken` fields explicitly rather than
      // destructuring `token` out, so we don't trip `noUnusedLocals`
      // on the discarded `token` variable.
      const listRow: ApiToken = {
        id: body.id,
        name: body.name,
        token_prefix: body.token_prefix,
        created_at: body.created_at,
        last_used_at: body.last_used_at,
        last_used_ip: body.last_used_ip,
        expires_at: body.expires_at,
      };
      setTokens((prev) => [listRow, ...prev]);
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to create token',
      );
    } finally {
      setCreating(false);
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-base">API tokens</CardTitle>
        <div className="flex gap-2">
          <Dialog
            open={createOpen}
            onOpenChange={(open) => {
              if (open) {
                setCreateOpen(true);
                return;
              }
              // Closing covers both rendered states: if the reveal is up, clear
              // the plaintext; if the form is up, reset the form. Either way, the
              // plaintext can never be re-summoned from this component — the
              // next time the user opens the dialog they start in the form state.
              setCreateOpen(false);
              setCreatedToken(null);
              createForm.reset();
            }}
          >
            <DialogTrigger asChild>
              <Button size="sm">+ New token</Button>
            </DialogTrigger>
            <DialogContent>
              {createdToken === null ? (
                <>
                  <DialogHeader>
                    <DialogTitle>Create API token</DialogTitle>
                    <DialogDescription>
                      Tokens grant full read/write access to this account's data.
                      Store the generated token in your password manager — you
                      won't be able to see it again after you close this dialog.
                    </DialogDescription>
                  </DialogHeader>
                  <Form {...createForm}>
                    <form
                      onSubmit={(e) => void createForm.handleSubmit(onCreate)(e)}
                      className="grid gap-4"
                      noValidate
                    >
                      <FormField
                        control={createForm.control}
                        name="name"
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>Name</FormLabel>
                            <FormControl>
                              <Input
                                {...field}
                                placeholder="Homepage dashboard"
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <FormField
                        control={createForm.control}
                        name="expires"
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>Expires</FormLabel>
                            <Select
                              value={field.value}
                              onValueChange={(v) => {
                                if (!(v in EXPIRY_OPTIONS)) return;
                                field.onChange(v as ExpiryChoice);
                              }}
                            >
                              <FormControl>
                                <SelectTrigger aria-label="Expires">
                                  <SelectValue />
                                </SelectTrigger>
                              </FormControl>
                              <SelectContent>
                                <SelectGroup>
                                  <SelectItem value="never">Never</SelectItem>
                                  <SelectItem value="7d">7 days</SelectItem>
                                  <SelectItem value="30d">30 days</SelectItem>
                                  <SelectItem value="90d">90 days</SelectItem>
                                  <SelectItem value="1y">1 year</SelectItem>
                                </SelectGroup>
                              </SelectContent>
                            </Select>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <FormField
                        control={createForm.control}
                        name="password"
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>Confirm password</FormLabel>
                            <FormControl>
                              <Input type="password" autoComplete="current-password" {...field} />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <DialogFooter>
                        <Button type="submit" disabled={creating}>
                          {creating ? 'Creating\u2026' : 'Create'}
                        </Button>
                      </DialogFooter>
                    </form>
                  </Form>
                </>
              ) : (
                <ShowOnceReveal
                  token={createdToken}
                  onClose={() => {
                    // Clicking Done closes the whole dialog — Dialog's
                    // onOpenChange(false) handler above clears the plaintext,
                    // so this one-liner is enough.
                    setCreateOpen(false);
                  }}
                />
              )}
            </DialogContent>
          </Dialog>
          {tokens.length > 0 && (
            <Button size="sm" variant="destructive" disabled>
              Revoke all
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent>
        {tokens.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No API tokens yet. Create one to connect this household to
            Homepage or another dashboard.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Token</TableHead>
                <TableHead>Last used</TableHead>
                <TableHead>Expires</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.map((t) => (
                <TableRow key={t.id}>
                  <TableCell>{t.name}</TableCell>
                  <TableCell className="font-mono">{t.token_prefix}</TableCell>
                  <TableCell>{formatLastUsed(t)}</TableCell>
                  <TableCell>{formatExpires(t)}</TableCell>
                  <TableCell
                    title={new Date(t.created_at).toLocaleString()}
                  >
                    {formatDistanceToNowStrict(new Date(t.created_at), {
                      addSuffix: true,
                    })}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      disabled
                      aria-label={`Revoke ${t.name}`}
                    >
                      Revoke
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

/* ---------- Import Preview Step ---------- */

interface ImportPreviewStepProps {
  preview: ImportPreview;
  cellErrors: Record<string, CellError>;
  unresolvedCount: number;
  canImport: boolean;
  pendingPatchCount: number;
  patchRow: (
    rowID: number,
    field: PatchRowRequest['field'],
    value: string | boolean,
  ) => Promise<void>;
  categories: Category[];
  categoryMap: Record<string, string>;
  setCategoryMap: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  defaultCategoryId: number | null;
  setDefaultCategoryId: (id: number | null) => void;
  onConfirm: () => void;
  onCancel: () => void;
}

function ImportPreviewStep({
  preview,
  cellErrors,
  unresolvedCount,
  canImport,
  pendingPatchCount,
  patchRow,
  categories,
  categoryMap,
  setCategoryMap,
  defaultCategoryId,
  setDefaultCategoryId,
  onConfirm,
  onCancel,
}: ImportPreviewStepProps) {
  const uniqueImportCategories = useMemo(
    () => preview.unique_categories ?? [],
    [preview.unique_categories],
  );

  const { matched, unmatched } = useMemo(() => {
    const m: { name: string; target: string }[] = [];
    const u: string[] = [];
    for (const catName of uniqueImportCategories) {
      const mappedId = categoryMap[catName];
      if (mappedId) {
        const target = categories.find((c) => String(c.id) === mappedId);
        m.push({ name: catName, target: target?.name ?? mappedId });
      } else {
        u.push(catName);
      }
    }
    return { matched: m, unmatched: u };
  }, [uniqueImportCategories, categoryMap, categories]);

  const rowsWithoutCategory = useMemo(
    () => preview.rows.filter((r) => !r.category).length,
    [preview.rows],
  );

  const needsDefaultCategory = unmatched.length > 0 || rowsWithoutCategory > 0;

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm font-medium">
        {`Found ${preview.row_count} rows to import.`}
      </p>

      {/* Data table — editable, with inline collision resolution.
          The component owns its entire footer (status + Cancel + Import)
          so this step renders no standalone action block below. Pairing
          Cancel and Import on the same decision row keeps the primary
          and abort actions together — Fitts's and user-expectation both. */}
      <ImportPreviewTable
        preview={preview}
        cellErrors={cellErrors}
        unresolvedCount={unresolvedCount}
        canImport={canImport}
        pendingPatchCount={pendingPatchCount}
        onPatchRow={patchRow}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />

      {/* Category mapping summary */}
      {uniqueImportCategories.length > 0 && (
        <div className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold">Category Mapping</h3>

          {/* Matched categories - compact summary */}
          {matched.length > 0 && (
            <div className="flex flex-col gap-2 rounded-md border p-3">
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <CheckCircle2 className="h-4 w-4 text-emerald-500" />
                <span>
                  {matched.length} of {uniqueImportCategories.length} categories
                  matched automatically
                </span>
              </div>
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                {matched.map((m) => (
                  <span key={m.name}>
                    {m.name === m.target
                      ? m.name
                      : `${m.name} \u2192 ${m.target}`}
                  </span>
                ))}
              </div>
            </div>
          )}

          {/* Unmatched categories - show dropdowns */}
          {unmatched.length > 0 && (
            <div className="flex flex-col gap-3 rounded-md border border-yellow-500/30 bg-yellow-500/5 p-3">
              <div className="flex items-center gap-2 text-sm">
                <CircleAlert className="h-4 w-4 text-yellow-500" />
                <span>
                  {unmatched.length} {unmatched.length === 1 ? 'category needs' : 'categories need'} mapping
                </span>
              </div>
              {unmatched.map((catName) => (
                <div key={catName} className="flex max-w-sm items-center gap-3">
                  <Label className="w-32 shrink-0 text-sm">{catName}</Label>
                  <Select
                    value={categoryMap[catName] ?? undefined}
                    onValueChange={(v) =>
                      setCategoryMap((prev) => ({
                        ...prev,
                        [catName]: v,
                      }))
                    }
                  >
                    <SelectTrigger aria-label={`Map category ${catName}`}>
                      <SelectValue placeholder="Select category..." />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        {categories.map((c) => (
                          <SelectItem key={c.id} value={String(c.id)}>
                            {c.name}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Default category - only when needed */}
      {needsDefaultCategory && (
        <div className="flex max-w-sm flex-col gap-2">
          <Label htmlFor="default-category">
            Default Category
            <span className="ml-1 text-xs font-normal text-muted-foreground">
              (for unmapped or missing categories)
            </span>
          </Label>
          <Select
            value={defaultCategoryId ? String(defaultCategoryId) : undefined}
            onValueChange={(v) => setDefaultCategoryId(Number(v))}
          >
            <SelectTrigger id="default-category" aria-label="Default Category">
              <SelectValue placeholder="Select default..." />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {categories.map((c) => (
                  <SelectItem key={c.id} value={String(c.id)}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      )}
    </div>
  );
}

/* ---------- Import / Export Tab ---------- */

function DataSection() {
  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [month, setMonth] = useState(now.getMonth() + 1);
  const [exportMode, setExportMode] = useState<'monthly' | 'yearly'>('monthly');

  // Import wizard state — preview / importStep / result are owned by the
  // hook now; destructure them so the rest of the function reads identically
  // to the old local-state version.
  const importSession = useImportSession();
  const { preview, importStep, result, error: importError } = importSession;

  const [defaultCategoryId, setDefaultCategoryId] = useState<number | null>(
    null,
  );
  const [categories, setCategories] = useState<Category[]>([]);
  const [categoryMap, setCategoryMap] = useState<Record<string, string>>({});
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  // Tracks the import_id we last auto-mapped categories for. Resets to null
  // on cancel / startOver so the next upload (or a re-upload) re-runs the
  // auto-map exactly once. See autoMapCategories effect below.
  const lastAutoMappedImportIdRef = useRef<string | null>(null);

  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch(() => {
        /* non-critical */
      });
  }, []);

  // Auto-map categories whenever the hook's preview changes to a new
  // import_id. The guard avoids clobbering the user's manual re-mapping on
  // unrelated re-renders (e.g. after a PATCH that only updates one row).
  useEffect(() => {
    if (!preview) {
      // Upload cancelled / session reset — arm the ref for the next preview.
      lastAutoMappedImportIdRef.current = null;
      return;
    }
    if (lastAutoMappedImportIdRef.current === preview.import_id) return;
    setCategoryMap(autoMapCategories(preview, categories));
    lastAutoMappedImportIdRef.current = preview.import_id;
  }, [preview, categories]);

  function clearFileInput() {
    if (fileInputRef.current) fileInputRef.current.value = '';
  }

  async function handleFileChange(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    // uploadFile catches internally and sets importSession.error, which
    // the Alert below renders. On success importStep flips to 'preview'
    // and the file input unmounts, so clearing it here only matters for
    // the rejected path (so the same file can be re-selected without
    // tripping the browser's value-equality no-op on change events).
    await importSession.uploadFile(file);
    clearFileInput();
  }

  async function handleConfirmImport() {
    if (!preview) return;
    // Convert string IDs to numbers for the backend (Go expects int64).
    const numericCategoryMap: Record<string, number> = {};
    for (const [name, id] of Object.entries(categoryMap)) {
      if (id) numericCategoryMap[name] = parseInt(id, 10);
    }
    // confirmImport never throws — it converts 409s into a local
    // collision_groups update + error message, and non-409s into
    // importSession.error. The Alert below renders that state.
    await importSession.confirmImport(numericCategoryMap, defaultCategoryId);
  }

  async function handleCancelImport() {
    await importSession.cancelImport();
    setCategoryMap({});
    setDefaultCategoryId(null);
    clearFileInput();
  }

  function handleImportAnother() {
    importSession.startOver();
    setCategoryMap({});
    setDefaultCategoryId(null);
    clearFileInput();
  }

  function handleExportMonthly() {
    window.open(`/api/export/monthly/${year}/${month}`, '_blank');
  }

  function handleExportYearly() {
    window.open(`/api/export/yearly/${year}`, '_blank');
  }

  return (
    <div className="flex flex-col gap-6">
      {/* ---------- Import card ---------- */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Import</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {importError && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{importError}</AlertDescription>
            </Alert>
          )}

          {importStep === 'upload' && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-muted-foreground">
                Upload an Excel file with columns: date, description, amount.
                Optional columns: category, tags, notes, original_amount,
                original_currency.
              </p>
              <Input
                ref={fileInputRef}
                id="excel-file"
                type="file"
                accept=".xlsx,.xls"
                aria-label="Excel File"
                onChange={(e) => void handleFileChange(e)}
                className="hidden"
              />
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                onDragOver={(e) => {
                  e.preventDefault();
                  e.currentTarget.setAttribute('data-drag-over', 'true');
                }}
                onDragLeave={(e) => {
                  e.preventDefault();
                  e.currentTarget.removeAttribute('data-drag-over');
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  e.currentTarget.removeAttribute('data-drag-over');
                  const file = e.dataTransfer.files[0];
                  if (file) {
                    const dt = new DataTransfer();
                    dt.items.add(file);
                    if (fileInputRef.current) {
                      fileInputRef.current.files = dt.files;
                      fileInputRef.current.dispatchEvent(
                        new Event('change', { bubbles: true }),
                      );
                    }
                  }
                }}
                className={cn(
                  'flex max-w-sm flex-col items-center gap-2 rounded-lg border-2 border-dashed border-muted-foreground/25 px-6 py-8 text-center transition-colors',
                  'hover:border-muted-foreground/50 hover:bg-muted/50',
                  'data-[drag-over=true]:border-primary data-[drag-over=true]:bg-primary/5',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
                )}
              >
                <Upload className="size-8 text-muted-foreground" />
                <div className="flex flex-col gap-1">
                  <span className="text-sm font-medium">
                    Drag & drop your Excel file here, or click to browse
                  </span>
                  <span className="text-xs text-muted-foreground">
                    .xlsx, .xls
                  </span>
                </div>
              </button>
            </div>
          )}

          {importStep === 'preview' && preview && (
            <ImportPreviewStep
              preview={preview}
              cellErrors={importSession.cellErrors}
              unresolvedCount={importSession.unresolvedCount}
              canImport={importSession.canImport}
              pendingPatchCount={importSession.pendingPatchCount}
              patchRow={importSession.patchRow}
              categories={categories}
              categoryMap={categoryMap}
              setCategoryMap={setCategoryMap}
              defaultCategoryId={defaultCategoryId}
              setDefaultCategoryId={setDefaultCategoryId}
              onConfirm={() => void handleConfirmImport()}
              onCancel={() => void handleCancelImport()}
            />
          )}

          {importStep === 'done' && result && (
            <div className="flex flex-col gap-4">
              <p className="text-sm font-medium">
                {`${result.imported} imported, ${result.skipped} skipped out of ${result.total} total rows.`}
              </p>
              <Button type="button" variant="outline" className="w-fit" onClick={handleImportAnother}>
                Import Another
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      {/* ---------- Export card ---------- */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Export</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <ButtonGroup>
            {([
              { value: 'monthly', label: 'Monthly' },
              { value: 'yearly', label: 'Yearly' },
            ] as const).map((opt) => (
              <Button
                key={opt.value}
                type="button"
                variant={exportMode === opt.value ? 'secondary' : 'outline'}
                size="sm"
                onClick={() => setExportMode(opt.value)}
              >
                {opt.label}
              </Button>
            ))}
          </ButtonGroup>
          <div className="flex max-w-md items-end gap-3">
            <div className="flex flex-col gap-2">
              <Label htmlFor="export-year">Year</Label>
              <Input
                id="export-year"
                type="number"
                value={year}
                onChange={(e) => setYear(Number(e.target.value))}
                onFocus={selectAllOnFocus}
                min={MIN_YEAR}
                max={MAX_YEAR}
                className="w-28"
              />
            </div>
            {exportMode === 'monthly' && (
              <div className="flex flex-col gap-2">
                <Label>Month</Label>
                <Select
                  value={String(month)}
                  onValueChange={(v) => setMonth(Number(v))}
                >
                  <SelectTrigger aria-label="Export Month" className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {MONTH_NAMES_FULL.map((m, i) => (
                        <SelectItem key={m} value={String(i + 1)}>
                          {m}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            )}
            <Button
              type="button"
              onClick={exportMode === 'monthly' ? handleExportMonthly : handleExportYearly}
            >
              Export
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

/* ---------- Main Settings Page ---------- */

export function Settings() {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const tabParam = searchParams.get('tab');
  const initialTab = isValidTab(tabParam) ? tabParam : 'general';
  const [activeTab, setActiveTab] = useState<SettingsTab>(initialTab);

  // Live mirror of GeneralSection's dirtyCount so this component can
  // block a tab change without GeneralSection having to re-render the
  // whole Settings tree on every keystroke. GeneralSection writes into
  // this ref via an effect and resets it on unmount.
  const generalDirtyCountRef = useRef(0);

  // Parked targets while the discard-edits dialog is open. Storing the
  // target (tab name or href) rather than a boolean lets the same dialog
  // name the destination and commit the change on "Discard".
  const [pendingTab, setPendingTab] = useState<SettingsTab | null>(null);
  const [pendingHref, setPendingHref] = useState<string | null>(null);

  useEffect(() => {
    if (isValidTab(tabParam) && tabParam !== activeTab) {
      setActiveTab(tabParam);
    }
    // activeTab is intentionally excluded from deps: this effect is a
    // one-way URL → state sync. Including activeTab would re-run the
    // effect every time the user clicks a tab and could race with the
    // history listener. The guard above already prevents redundant
    // setState on equal values, so the only meaningful trigger is a
    // fresh tabParam coming in from the router.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabParam]);

  // Guard in-app navigation (e.g. sidebar NavLinks) when the General
  // tab has unsaved budget edits. The app uses <BrowserRouter> — not
  // the data router — so react-router v7's `useBlocker` isn't
  // available. We intercept at the DOM level instead: a capture-phase
  // document listener inspects anchor clicks before React Router's own
  // onClick handler runs, which is early enough to preventDefault and
  // park the target in `pendingHref`.
  //
  // Scope / filters:
  //   - Only active while we're on the General tab (other tabs have
  //     no dirty state today).
  //   - Only primary-button, unmodified clicks — ctrl/cmd/shift/middle
  //     are "open in new tab/window" and the user has not asked to
  //     leave the current view.
  //   - Only same-origin anchors whose pathname differs from current.
  //     Hash links, external links, and `target="_blank"` downloads
  //     (e.g. Import/Export) pass through unguarded.
  useEffect(() => {
    if (activeTab !== 'general') return;
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
      if (generalDirtyCountRef.current <= 0) return;
      e.preventDefault();
      e.stopPropagation();
      setPendingHref(url.pathname + url.search + url.hash);
    }
    document.addEventListener('click', handler, true);
    return () => document.removeEventListener('click', handler, true);
  }, [activeTab]);

  function handleTabChange(v: string) {
    const next = v as SettingsTab;
    if (next === activeTab) return;
    // Only the General tab owns unsaved budget edits today. If other
    // sections grow their own dirty state, add parallel refs and OR
    // them here rather than collapsing into a single shared counter —
    // we want to report which tab is dirty in the prompt.
    if (activeTab === 'general' && generalDirtyCountRef.current > 0) {
      setPendingTab(next);
      return;
    }
    setActiveTab(next);
  }

  // Friendly label for the intercepted route. Falls back to the raw
  // pathname when a route we don't know about shows up, so the dialog
  // is still readable — just less polished.
  const pendingHrefLabel = useMemo(() => {
    if (pendingHref === null) return '';
    const pathOnly = pendingHref.split(/[?#]/)[0];
    return ROUTE_LABELS[pathOnly] ?? pathOnly;
  }, [pendingHref]);

  const dirtyCount = generalDirtyCountRef.current;

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      <Tabs
        value={activeTab}
        onValueChange={handleTabChange}
        activationMode="manual"
        className="w-full"
      >
        <TabsList>
          <TabsTrigger value="general">General</TabsTrigger>
          <TabsTrigger value="currencies">Currencies</TabsTrigger>
          <TabsTrigger value="savings">Savings</TabsTrigger>
          {admin && <TabsTrigger value="users">Users</TabsTrigger>}
          <TabsTrigger value="api-tokens">API tokens</TabsTrigger>
          <TabsTrigger value="data">Import / Export</TabsTrigger>
        </TabsList>
        <TabsContent value="general" className="mt-6">
          <GeneralSection dirtyCountRef={generalDirtyCountRef} />
        </TabsContent>
        <TabsContent value="currencies" className="mt-6">
          <CurrenciesSection />
        </TabsContent>
        <TabsContent value="savings" className="mt-6">
          <SavingsSection />
        </TabsContent>
        {admin && (
          <TabsContent value="users" className="mt-6">
            <UsersSection />
          </TabsContent>
        )}
        <TabsContent value="api-tokens" className="mt-6">
          <ApiTokensSection />
        </TabsContent>
        <TabsContent value="data" className="mt-6">
          <DataSection />
        </TabsContent>
      </Tabs>
      <DiscardEditsDialog
        open={pendingTab !== null}
        count={dirtyCount}
        destinationLabel={
          pendingTab === null ? '' : `the ${TAB_LABELS[pendingTab]} tab`
        }
        onCancel={() => setPendingTab(null)}
        onConfirm={() => {
          if (pendingTab !== null) setActiveTab(pendingTab);
          setPendingTab(null);
        }}
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
    </div>
  );
}
