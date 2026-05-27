import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react';
import { Link } from 'react-router-dom';
import { toast } from 'sonner';
import { ArrowUpRight, CheckCircle2 } from 'lucide-react';
import { ApiError } from '@/api/client';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Toaster } from '@/components/ui/sonner';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { AmountCurrencyInput } from '@/components/AmountCurrencyInput';
import { AutocompleteInput } from '@/components/AutocompleteInput';
import { TagInput } from '@/components/TagInput';
import { CategoryChips } from '@/components/CategoryChips';
import { RecentlyAdded } from '@/components/RecentlyAdded';
import { useCategories } from '@/hooks/useCategories';
import { useCurrencies } from '@/hooks/useCurrencies';
import { useQuickAdd, type QuickAddOutcome } from '@/hooks/useQuickAdd';
import { useOfflineQueue } from '@/hooks/useOfflineQueue';
import { parseQuickEntry } from '@/lib/quick-parse';
import { toCreatePayload } from '@/lib/currency';
import { formatCurrency } from '@/lib/format';
import { formatYYYYMMDD } from '@/lib/dates';
import { isExpense } from '@/lib/transaction-types';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import type { Category } from '@/api/types';

type QuickMode = 'freeform' | 'tap';

function todayIso(): string {
  return formatYYYYMMDD(new Date());
}

function getStickyMode(): QuickMode {
  return localStorage.getItem(STORAGE_KEYS.quickAddMode) === 'tap'
    ? 'tap'
    : 'freeform';
}

function getLastCategoryId(): number {
  const raw = localStorage.getItem(STORAGE_KEYS.lastTransactionCategory);
  if (!raw) return 0;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function getLastCurrency(fallback: string): string {
  return localStorage.getItem(STORAGE_KEYS.lastTransactionCurrency) ?? fallback;
}

/**
 * Mobile-first, full-screen quick-capture screen mounted at `/quick`
 * (the installed PWA's `start_url`). Renders OUTSIDE `AppShell` so there
 * is no desktop sidebar/padding — it owns the whole viewport. Two entry
 * modes share one submit pipeline:
 *   - Freeform: one chat-style line parsed by `parseQuickEntry`.
 *   - Tap: amount keypad + one-tap category chips.
 * Both build the wire payload with `toCreatePayload` and POST through
 * `useQuickAdd().create`, mirroring `TransactionEntryRow`'s success toast
 * + Undo affordance and sticky-localStorage behavior.
 */
export function QuickAdd() {
  const [mode, setMode] = useState<QuickMode>(getStickyMode);
  // Bumped after each successful add so the "Recently added" panel re-pulls.
  const [recentRefreshKey, setRecentRefreshKey] = useState(0);

  const {
    categories,
    loading: categoriesLoading,
    error: categoriesError,
    refetch: refetchCategories,
  } = useCategories();
  const {
    list: currencies,
    baseCode,
    rateFor,
    loading: currenciesLoading,
  } = useCurrencies();
  const { create, undo, saving } = useQuickAdd();
  const { pending, count: pendingCount } = useOfflineQueue();

  // Expense categories only (quick-add captures spending). Surface the
  // sticky last-used category first so the common case is one tap away.
  const expenseCategories = useMemo<Category[]>(() => {
    const lastId = getLastCategoryId();
    const expenses = categories.filter((c) => isExpense(c.type));
    if (!lastId) return expenses;
    const idx = expenses.findIndex((c) => c.id === lastId);
    if (idx <= 0) return expenses;
    const copy = [...expenses];
    const [last] = copy.splice(idx, 1);
    return [last, ...copy];
  }, [categories]);

  // --- Freeform state ------------------------------------------------------
  const [raw, setRaw] = useState('');
  const parsed = useMemo(
    () =>
      parseQuickEntry(raw, {
        categories: expenseCategories,
        currencies,
        baseCurrency: baseCode,
      }),
    [raw, expenseCategories, currencies, baseCode],
  );

  // --- Tap state -----------------------------------------------------------
  const [tapAmount, setTapAmount] = useState(0);
  const [tapCurrency, setTapCurrency] = useState(() => getLastCurrency(baseCode));
  const [tapDescription, setTapDescription] = useState('');
  const [tapTags, setTapTags] = useState('');

  // Sticky currency resync once currencies load (mirrors TransactionEntryRow).
  const didInitCurrency = useRef(false);
  useEffect(() => {
    if (didInitCurrency.current || currenciesLoading) return;
    didInitCurrency.current = true;
    setTapCurrency(getLastCurrency(baseCode));
  }, [currenciesLoading, baseCode]);

  // --- Shared category selection ------------------------------------------
  // A manual chip pick overrides the parser's match (Freeform) or is the
  // only source (Tap). `null` means "use parser / unset".
  const [pickedCategoryId, setPickedCategoryId] = useState<number | null>(null);

  const inputRef = useRef<HTMLInputElement>(null);
  const tapAmountRef = useRef<HTMLInputElement>(null);
  // Latest submit, so the error toast's Retry action can re-run it without
  // making `submit` depend on itself.
  const submitRef = useRef<() => void>(() => {});

  // Switch mode + persist the toggle. Defer focus so the target input is
  // mounted (mirrors the reset-focus idiom in `resetForNext`).
  const onModeChange = useCallback((next: string) => {
    const m: QuickMode = next === 'tap' ? 'tap' : 'freeform';
    setMode(m);
    localStorage.setItem(STORAGE_KEYS.quickAddMode, m);
    setTimeout(() => {
      if (m === 'tap') tapAmountRef.current?.focus();
      else inputRef.current?.focus();
    }, 0);
  }, []);

  // Effective values per mode.
  const effective =
    mode === 'freeform'
      ? {
          amount: parsed.amount ?? 0,
          currency: parsed.currency,
          description: parsed.description,
          tags: parsed.tags,
          categoryId: pickedCategoryId ?? parsed.categoryId,
        }
      : {
          amount: tapAmount,
          currency: tapCurrency,
          description: tapDescription.trim(),
          tags: tapTags,
          categoryId: pickedCategoryId,
        };

  const hasNoRate =
    !currenciesLoading &&
    effective.currency !== baseCode &&
    rateFor(effective.currency) == null;

  const canSubmit =
    effective.amount > 0 &&
    effective.description.length > 0 &&
    effective.categoryId != null &&
    effective.categoryId > 0 &&
    !hasNoRate &&
    !saving;

  // First unmet requirement, shown as a muted hint near Add so a disabled
  // button is never a silent dead-end on touch.
  const missingHint = !canSubmit
    ? effective.amount <= 0
      ? 'Enter an amount'
      : effective.description.length === 0
        ? 'Add a description'
        : effective.categoryId == null || effective.categoryId <= 0
          ? 'Pick a category'
          : hasNoRate
            ? 'Set a rate for this currency in Settings'
            : null
    : null;

  const resetForNext = useCallback(() => {
    setRaw('');
    setTapAmount(0);
    setTapDescription('');
    setTapTags('');
    setPickedCategoryId(null);
    setTimeout(() => {
      if (mode === 'freeform') inputRef.current?.focus();
      else tapAmountRef.current?.focus();
    }, 0);
  }, [mode]);

  const submit = useCallback(async () => {
    if (!canSubmit || effective.categoryId == null) return;
    let payload: CreateTransactionInput;
    try {
      payload = toCreatePayload(
        {
          date: todayIso(),
          amount: effective.amount,
          currency: effective.currency,
          description: effective.description,
          category_id: effective.categoryId,
          tags: effective.tags,
        },
        baseCode,
        rateFor,
      ) as CreateTransactionInput;
    } catch {
      toast.error('Failed to save transaction');
      return;
    }

    let outcome: QuickAddOutcome;
    try {
      outcome = await create(payload);
    } catch (err) {
      // We only auto-queue when navigator reports the device is offline (see
      // useQuickAdd) — there the request never leaves the device, so replay is
      // dup-safe. A throw here means either the server was reached and rejected
      // the write (ApiError) or the fetch failed while the browser still thinks
      // it is online (ambiguous: the write may have landed). Neither is safe to
      // silently queue, so prompt a retry instead.
      toast.error(
        err instanceof ApiError
          ? err.message || 'Failed to save transaction'
          : 'Couldn’t reach the server. Check your connection and try again.',
        {
          // The form is preserved on failure; Retry re-submits it so the user
          // doesn't have to guess that re-tapping Add is the way back.
          action: { label: 'Retry', onClick: () => submitRef.current() },
        },
      );
      return;
    }

    // Sticky last-used values for the next entry (whether saved or queued).
    localStorage.setItem(
      STORAGE_KEYS.lastTransactionCategory,
      String(effective.categoryId),
    );
    localStorage.setItem(STORAGE_KEYS.lastTransactionDate, payload.date);
    localStorage.setItem(
      STORAGE_KEYS.lastTransactionCurrency,
      effective.currency,
    );

    // Capture this outcome in the toast's own closure so rapid successive saves
    // each undo their own entry — the server row, or the queued row by its
    // queue id if it has not synced yet.
    const undoThis = outcome;
    toast.success(
      undoThis.status === 'queued'
        ? 'Saved offline — will sync when you’re back online'
        : 'Transaction saved',
      {
        duration: 4000,
        action: {
          label: 'Undo',
          onClick: () =>
            void undo(undoThis).catch(() => toast.error('Could not undo')),
        },
      },
    );

    setRecentRefreshKey((k) => k + 1);
    resetForNext();
  }, [
    canSubmit,
    effective.amount,
    effective.currency,
    effective.description,
    effective.categoryId,
    effective.tags,
    baseCode,
    rateFor,
    create,
    undo,
    resetForNext,
  ]);

  const onFreeformKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        void submit();
      }
    },
    [submit],
  );

  // Keep the error toast's Retry handler pointed at the current submit closure.
  useEffect(() => {
    submitRef.current = () => void submit();
  }, [submit]);

  const descriptionSuggestions = useMemo(
    () => expenseCategories.map((c) => c.name),
    [expenseCategories],
  );

  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <header className="flex items-center justify-between border-b border-border px-4 py-3">
        <h1 className="sr-only">Quick add</h1>
        <span className="text-lg font-semibold tracking-tight">SpenDrop</span>
        <Button asChild variant="ghost" size="sm">
          <Link to="/">
            Full app
            <ArrowUpRight data-icon="inline-end" />
          </Link>
        </Button>
      </header>

      <main className="flex flex-1 flex-col gap-6 px-4 py-6">
        {/* Offline-capture reassurance. A neutral "saved" receipt, NOT an
            amber warning — capturing offline is the designed happy path, so
            this confirms the entry is safe rather than flagging a problem.
            aria-live="off" because the success toast already announces each
            save; a live region here would double-narrate to screen readers. */}
        {pendingCount > 0 && (
          <Alert role="status" aria-live="off">
            <CheckCircle2 className="h-4 w-4" />
            <AlertDescription>
              {pendingCount} {pendingCount === 1 ? 'entry' : 'entries'} saved on
              this device · will sync when online
            </AlertDescription>
          </Alert>
        )}

        <Tabs value={mode} onValueChange={onModeChange}>
          <TabsList className="grid w-full grid-cols-2">
            <TabsTrigger value="freeform">Freeform</TabsTrigger>
            <TabsTrigger value="tap">Tap</TabsTrigger>
          </TabsList>
        </Tabs>

        {mode === 'freeform' ? (
          <div className="flex flex-col gap-4">
            <Input
              ref={inputRef}
              autoFocus
              value={raw}
              onChange={(e) => setRaw(e.target.value)}
              onKeyDown={onFreeformKeyDown}
              placeholder="e.g. lunch 12.50 #work"
              aria-label="Quick entry"
              className="h-14 text-lg"
            />

            {raw.trim().length > 0 && (
              <div className="flex flex-col gap-4 rounded-lg border border-border p-4">
                <div
                  data-testid="quick-preview-amount"
                  className="font-mono text-3xl font-semibold tabular-nums"
                >
                  {parsed.amount != null ? (
                    formatCurrency(parsed.amount, effective.currency)
                  ) : (
                    <span className="text-base font-normal text-muted-foreground">
                      Add an amount
                    </span>
                  )}
                </div>
                {effective.description && (
                  <p className="text-base text-muted-foreground">
                    {effective.description}
                  </p>
                )}
                {parsed.tags && (
                  <p className="text-sm text-muted-foreground">
                    {parsed.tags
                      .split(' ')
                      .filter(Boolean)
                      .map((t) => `#${t}`)
                      .join(' ')}
                  </p>
                )}
              </div>
            )}
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="quick-tap-amount">Amount</Label>
              <AmountCurrencyInput
                id="quick-tap-amount"
                value={tapAmount}
                onValueChange={setTapAmount}
                currency={tapCurrency}
                onCurrencyChange={setTapCurrency}
                baseCode={baseCode}
                currencies={currencies}
                hideInactive
                rateFor={rateFor}
                loading={currenciesLoading}
                error={
                  hasNoRate
                    ? 'No rate configured for this currency. Set one in Settings.'
                    : null
                }
                inputRef={tapAmountRef}
              />
            </div>

            <div className="flex flex-col gap-2">
              <Label htmlFor="quick-tap-desc">Description</Label>
              <AutocompleteInput
                id="quick-tap-desc"
                suggestions={descriptionSuggestions}
                value={tapDescription}
                onChange={(e) => setTapDescription(e.target.value)}
                onAccept={(v) => setTapDescription(v)}
                placeholder="What was it?"
              />
            </div>

            <div className="flex flex-col gap-2">
              <Label>Tags</Label>
              <TagInput
                value={tapTags}
                onChange={setTapTags}
                placeholder="Add tags..."
              />
            </div>
          </div>
        )}

        <div className="flex flex-col gap-2">
          <Label>Category</Label>
          {categoriesLoading && expenseCategories.length === 0 ? (
            <p className="text-sm text-muted-foreground">Loading categories…</p>
          ) : categoriesError ? (
            <div className="flex flex-col items-start gap-2">
              <p className="text-sm text-destructive" role="alert">
                {categoriesError}
              </p>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => refetchCategories()}
              >
                Retry
              </Button>
            </div>
          ) : expenseCategories.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No expense categories yet —{' '}
              <Link to="/categories" className="underline">
                create one
              </Link>
            </p>
          ) : (
            <CategoryChips
              categories={expenseCategories}
              selectedId={effective.categoryId}
              onSelect={setPickedCategoryId}
            />
          )}
        </div>

        {hasNoRate && (
          <p className="text-sm text-destructive" role="alert">
            No rate configured for this currency. Set one in Settings.
          </p>
        )}

        <RecentlyAdded
          pending={pending}
          categories={categories}
          baseCode={baseCode}
          refreshKey={recentRefreshKey}
        />
      </main>

      <footer className="sticky bottom-0 flex flex-col gap-2 border-t border-border bg-background px-4 py-4 pb-[env(safe-area-inset-bottom)]">
        {missingHint && (
          <p className="text-center text-sm text-muted-foreground">
            {missingHint}
          </p>
        )}
        <Button
          type="button"
          className="h-14 w-full text-base"
          disabled={!canSubmit}
          onClick={() => void submit()}
        >
          {saving ? 'Saving…' : 'Add'}
        </Button>
      </footer>

      <Toaster />
    </div>
  );
}
