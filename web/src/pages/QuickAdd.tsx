import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type MouseEvent,
} from 'react';
import { Link } from 'react-router-dom';
import { toast } from 'sonner';
import { ArrowUpRight, CheckCircle2, LogIn, WifiOff } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AppVersion } from '@/components/AppVersion';
import { Toaster } from '@/components/ui/sonner';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { AmountCurrencyInput } from '@/components/AmountCurrencyInput';
import { AutocompleteInput } from '@/components/AutocompleteInput';
import { TagInput } from '@/components/TagInput';
import { CategoryChips } from '@/components/CategoryChips';
import { QuickAddSuggestions } from '@/components/QuickAddSuggestions';
import { RecentlyAdded } from '@/components/RecentlyAdded';
import { useCategories } from '@/hooks/useCategories';
import { useCurrencies } from '@/hooks/useCurrencies';
import { useDescriptionHistory } from '@/hooks/useDescriptionHistory';
import { useQuickAdd, type QuickAddOutcome } from '@/hooks/useQuickAdd';
import { useOfflineQueue } from '@/hooks/useOfflineQueue';
import { useAuth } from '@/hooks/useAuth';
import { parseQuickEntry } from '@/lib/quick-parse';
import { newClientKey } from '@/lib/client-key';
import {
  isRetryableSaveFailure,
  noRateMessage,
  saveFailureMessage,
} from '@/lib/save-failure';
import { toCreatePayload } from '@/lib/currency';
import { cn } from '@/lib/utils';
import { formatCurrency } from '@/lib/format';
import { formatYYYYMMDD } from '@/lib/dates';
import { isExpense, isIncome, TYPE_INCOME } from '@/lib/transaction-types';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import type { Category } from '@/api/types';

type QuickMode = 'freeform' | 'tap';
type EntryKind = 'income' | 'expense';

/**
 * One user intent, built once. `payload` already carries the `client_key`
 * minted when it was assembled, so re-sending THIS object — rather than
 * rebuilding it from the form — is what makes a retry after an ambiguous
 * failure land on the same row instead of creating a second one.
 */
interface QuickSubmission {
  payload: CreateTransactionInput;
  /** Currency the user typed in, kept for the sticky last-used value. */
  currency: string;
  /**
   * Toast slot for this submission — the failure, the "Retrying…" spinner and
   * the eventual result all render into it, so one intent never stacks three
   * toasts. Shares the client key's value: one intent, one identity.
   */
  toastId: string;
  /**
   * What the form held when this was built. A retry can be tapped minutes
   * later, by which time the screen may hold a different draft; comparing
   * against this tells the success path whether clearing the form would throw
   * away work the user has since done.
   */
  formSnapshot: string;
}

function todayIso(): string {
  return formatYYYYMMDD(new Date());
}

function getStickyMode(): QuickMode {
  return localStorage.getItem(STORAGE_KEYS.quickAddMode) === 'tap'
    ? 'tap'
    : 'freeform';
}

function getStickyKind(): EntryKind {
  // Default to 'expense' — the fast common path stays unchanged.
  return localStorage.getItem(STORAGE_KEYS.quickAddKind) === TYPE_INCOME
    ? 'income'
    : 'expense';
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
  // Income vs expense is derived purely from the selected category's type, so
  // this axis only narrows which categories the parser/chips see — the submit
  // pipeline is identical. Sticky so the screen reopens on the last-used kind.
  const [kind, setKind] = useState<EntryKind>(getStickyKind);
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
  const { user } = useAuth();
  const { create, undo, saving } = useQuickAdd();
  const {
    pending,
    count: pendingCount,
    needsSignIn: queueHeld,
  } = useOfflineQueue(user?.id);
  const {
    descriptions: historyDescriptions,
    failed: historyFailed,
    waitingForNetwork: historyWaiting,
    retry: retryHistory,
  } = useDescriptionHistory();

  // Categories for the active kind (income or expense). Surface the sticky
  // last-used category first so the common case is one tap away.
  const activeCategories = useMemo<Category[]>(() => {
    const lastId = getLastCategoryId();
    const pool = categories.filter((c) =>
      kind === 'income' ? isIncome(c.type) : isExpense(c.type),
    );
    if (!lastId) return pool;
    const idx = pool.findIndex((c) => c.id === lastId);
    if (idx <= 0) return pool;
    const copy = [...pool];
    const [last] = copy.splice(idx, 1);
    return [last, ...copy];
  }, [categories, kind]);

  // Filter the history list to suggestions that round-trip through the
  // freeform parser as a *pure description* — no amount, no tags. If a past
  // description like "test 2" was suggested as-is, tapping the chip would
  // rewrite the input to "test 2 " and the parser would immediately split
  // it back apart (description="test", amount=$2), reversing the user's
  // intent. Keep only suggestions the parser leaves intact.
  const safeDescriptions = useMemo(() => {
    return historyDescriptions.filter((s) => {
      const p = parseQuickEntry(s, {
        categories: activeCategories,
        currencies,
        baseCurrency: baseCode,
      });
      return (
        p.amount == null &&
        !p.tags &&
        p.description.trim().toLowerCase() === s.trim().toLowerCase()
      );
    });
  }, [historyDescriptions, activeCategories, currencies, baseCode]);

  // --- Freeform state ------------------------------------------------------
  const [raw, setRaw] = useState('');
  const parsed = useMemo(
    () =>
      parseQuickEntry(raw, {
        categories: activeCategories,
        currencies,
        baseCurrency: baseCode,
      }),
    [raw, activeCategories, currencies, baseCode],
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
  // Latest send, so the error toast's Retry action can re-run it without
  // making `send` depend on itself.
  const sendRef = useRef<
    (submission: QuickSubmission, isRetry?: boolean) => void
  >(() => {});

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

  // Switch income/expense kind + persist the toggle. Reset the picked category
  // so a category chosen under one kind can't leak into the other and wrongly
  // satisfy canSubmit — only the category selection is cleared; the amount,
  // description, and tags are intentionally preserved.
  const onKindChange = useCallback((next: string) => {
    const k: EntryKind = next === TYPE_INCOME ? 'income' : 'expense';
    setKind(k);
    localStorage.setItem(STORAGE_KEYS.quickAddKind, k);
    setPickedCategoryId(null);
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

  // Identity of the entry currently on screen. A retry that lands long after
  // the failure compares against this to decide whether clearing the form
  // would discard a draft the user has started in the meantime.
  const formSnapshot = JSON.stringify([
    effective.amount,
    effective.currency,
    effective.description,
    effective.categoryId ?? '',
    effective.tags ?? '',
  ]);
  const formSnapshotRef = useRef(formSnapshot);
  useEffect(() => {
    formSnapshotRef.current = formSnapshot;
  }, [formSnapshot]);

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

  // Sends an already-built submission: the online POST, the offline capture
  // (both inside `create`), and every retry of that same intent go through
  // here. It deliberately takes the payload rather than reading the form, so
  // no path can rebuild — and therefore re-key — a submission the server may
  // already have accepted.
  const send = useCallback(
    async (submission: QuickSubmission, isRetry = false) => {
      const { payload, currency, toastId, formSnapshot } = submission;
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
        toast.error(saveFailureMessage(err), {
          id: toastId,
          // Never auto-dismiss. This toast holds the only path that cannot
          // duplicate the entry; the Add button beside it mints a fresh key and
          // never expires. A window that closes on its own would quietly demote
          // the safe option to the risky one, so the user closes it themselves.
          duration: Infinity,
          closeButton: true,
          // The form is preserved on failure; Retry re-sends this exact
          // submission so the user doesn't have to guess that re-tapping Add is
          // the way back. Re-sending the built payload (not rebuilding it from
          // the form) keeps the client_key stable, which is what lets a failure
          // that actually landed resolve to the existing row instead of a
          // duplicate. Offered only where sending again could change the answer.
          action: isRetryableSaveFailure(err)
            ? {
                label: 'Retry',
                onClick: (event: MouseEvent<HTMLButtonElement>) => {
                  // sonner dismisses a toast after its action runs unless the
                  // event is default-prevented. Keep it, and reuse the slot so
                  // the spinner replaces the error where the user is looking.
                  event.preventDefault();
                  toast.loading('Retrying…', { id: toastId });
                  sendRef.current(submission, true);
                },
              }
            : undefined,
        });
        return;
      }

      // Sticky last-used values for the next entry (whether saved or queued).
      // Read off the submission, not the live form: a retry may arrive after
      // the user has started typing something else.
      localStorage.setItem(
        STORAGE_KEYS.lastTransactionCategory,
        String(payload.category_id),
      );
      localStorage.setItem(STORAGE_KEYS.lastTransactionDate, payload.date);
      localStorage.setItem(STORAGE_KEYS.lastTransactionCurrency, currency);

      // Capture this outcome in the toast's own closure so rapid successive
      // saves each undo their own entry — the server row, or the queued row by
      // its queue id if it has not synced yet.
      const undoThis = outcome;
      toast.success(
        undoThis.status === 'queued'
          ? 'Saved offline — will sync when you’re back online'
          : 'Transaction saved',
        {
          // Same slot as the failure it replaces, so a retry resolves where the
          // user is already looking instead of stacking a third toast.
          id: toastId,
          duration: 4000,
          action: {
            label: 'Undo',
            onClick: () =>
              void undo(undoThis).catch(() => toast.error('Could not undo')),
          },
        },
      );

      setRecentRefreshKey((k) => k + 1);
      // A retry can succeed long after the failure, by which time the screen
      // may hold a different entry the user has started. Clearing it then would
      // destroy work to tidy up after a save they already believe happened. The
      // entry itself is still saved either way — only the form is spared.
      if (!isRetry || formSnapshot === formSnapshotRef.current) {
        resetForNext();
      }
    },
    // formSnapshotRef is a stable ref; listed because the compiler infers it
    // as a dependency of the draft check and refuses to memoize otherwise.
    [create, undo, resetForNext, formSnapshotRef],
  );

  // Builds the payload for what the form currently holds — one intent, one
  // `client_key`, minted here and nowhere else. Editing the form and adding
  // again therefore mints a new key, which is correct: that is a new intent.
  const submit = useCallback(async () => {
    if (!canSubmit || effective.categoryId == null) return;
    const clientKey = newClientKey();
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
          client_key: clientKey,
        },
        baseCode,
        rateFor,
      ) as CreateTransactionInput;
    } catch {
      // Nothing was built and nothing was sent, so there is no Retry to offer —
      // and a generic "failed to save" would send the user looking for a
      // network problem. Name the missing rate instead.
      toast.error(noRateMessage(effective.currency));
      return;
    }
    await send({
      payload,
      currency: effective.currency,
      toastId: clientKey,
      formSnapshot,
    });
  }, [
    canSubmit,
    effective.amount,
    effective.currency,
    effective.description,
    effective.categoryId,
    effective.tags,
    formSnapshot,
    baseCode,
    rateFor,
    send,
  ]);

  // Show the past-descriptions chip strip only while the user is still
  // shaping the description (>=2 chars typed, no amount yet). Once an
  // amount appears we assume they're committed and don't want the input
  // rewritten under them.
  const showSuggestions =
    mode === 'freeform' &&
    parsed.amount == null &&
    parsed.description.trim().length >= 2;

  // First chip that would render in the strip — used by the Tab accelerator
  // below. Kept in sync with QuickAddSuggestions' own filter (prefix match,
  // skip exact-no-extension) so Tab picks exactly the visible first chip.
  const firstSuggestion: string | null = useMemo(() => {
    if (!showSuggestions) return null;
    const q = parsed.description.trim().toLowerCase();
    if (q.length === 0) return null;
    for (const s of safeDescriptions) {
      const lower = s.toLowerCase();
      if (!lower.startsWith(q)) continue;
      if (lower === q) continue;
      return s;
    }
    return null;
  }, [showSuggestions, parsed.description, safeDescriptions]);

  const onPickSuggestion = useCallback((suggestion: string) => {
    setRaw(`${suggestion} `);
    // Refocus + place caret at end so the user can keep typing the amount.
    requestAnimationFrame(() => {
      const el = inputRef.current;
      if (el) {
        el.focus();
        const pos = el.value.length;
        el.setSelectionRange(pos, pos);
      }
    });
  }, []);

  const onFreeformKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        void submit();
        return;
      }
      // Tab accepts the first visible chip (desktop accelerator). Plain Tab
      // only — Shift+Tab is normal reverse focus. Kept here (vs a window
      // listener in the chip strip) so the behavior lives next to the input
      // it targets.
      if (e.key === 'Tab' && !e.shiftKey && firstSuggestion) {
        e.preventDefault();
        onPickSuggestion(firstSuggestion);
      }
    },
    [submit, firstSuggestion, onPickSuggestion],
  );

  // Keep the error toast's Retry handler pointed at the current send closure.
  // Only the closure is refreshed — the submission it is handed is the one
  // captured when that toast was raised.
  useEffect(() => {
    sendRef.current = (submission, isRetry) => void send(submission, isRetry);
  }, [send]);

  const descriptionSuggestions = useMemo(
    () => activeCategories.map((c) => c.name),
    [activeCategories],
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
        {pendingCount > 0 &&
          (queueHeld ? (
            /* The server rejected this identity, so these rows will not sync
               on their own. Say what has to happen instead of repeating a
               promise that cannot be kept — and never drop the rows. */
            <Alert role="status" aria-live="off">
              <LogIn className="h-4 w-4" />
              <AlertDescription>
                {pendingCount} {pendingCount === 1 ? 'entry' : 'entries'} saved
                on this device ·{' '}
                <Link to="/login" className="font-medium underline">
                  Sign in to sync
                </Link>
              </AlertDescription>
            </Alert>
          ) : (
            <Alert role="status" aria-live="off">
              <CheckCircle2 className="h-4 w-4" />
              <AlertDescription>
                {pendingCount} {pendingCount === 1 ? 'entry' : 'entries'} saved
                on this device · will sync when online
              </AlertDescription>
            </Alert>
          ))}

        {/* Two stacked segmented controls: first WHAT (income vs expense),
            then HOW (freeform vs tap). Each carries a visible Label + an
            aria-labelledby so the otherwise-identical tablists are
            distinguishable to both sighted and screen-reader users. */}
        <div className="flex flex-col gap-2">
          <Label id="quick-kind-label">Type</Label>
          <Tabs value={kind} onValueChange={onKindChange}>
            <TabsList
              aria-labelledby="quick-kind-label"
              className="grid w-full grid-cols-2"
            >
              <TabsTrigger value="expense">Expense</TabsTrigger>
              <TabsTrigger value="income">Income</TabsTrigger>
            </TabsList>
          </Tabs>
        </div>

        <div className="flex flex-col gap-2">
          <Label id="quick-mode-label">Entry mode</Label>
          <Tabs value={mode} onValueChange={onModeChange}>
            <TabsList
              aria-labelledby="quick-mode-label"
              className="grid w-full grid-cols-2"
            >
              <TabsTrigger value="freeform">Freeform</TabsTrigger>
              <TabsTrigger value="tap">Tap</TabsTrigger>
            </TabsList>
          </Tabs>
        </div>

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

            {/* Three distinct outcomes, never collapsed into "no chips":
                the history broke, the history has not been fetched yet
                (offline), or there is genuinely nothing to suggest. Mirrors
                the category panel below, which already separates a load
                failure from an empty list. */}
            {showSuggestions &&
              (historyFailed ? (
                <div className="flex flex-col items-start gap-2">
                  <p className="text-sm text-destructive" role="alert">
                    Couldn’t load your past entries.
                  </p>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    aria-label="Retry past entries"
                    onClick={retryHistory}
                  >
                    Retry
                  </Button>
                </div>
              ) : historyWaiting ? (
                <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <WifiOff className="size-3.5 shrink-0" aria-hidden="true" />
                  You’re offline — past entries will appear when you reconnect.
                </p>
              ) : (
                <QuickAddSuggestions
                  suggestions={safeDescriptions}
                  query={parsed.description}
                  onPick={onPickSuggestion}
                />
              ))}

            {raw.trim().length > 0 && (
              <div className="flex flex-col gap-4 rounded-lg border border-border p-4">
                <div
                  data-testid="quick-preview-amount"
                  className={cn(
                    'font-mono text-3xl font-semibold tabular-nums',
                    kind === 'income' &&
                      parsed.amount != null &&
                      'text-emerald-500',
                  )}
                >
                  {parsed.amount != null ? (
                    `${kind === 'income' ? '+' : ''}${formatCurrency(parsed.amount, effective.currency)}`
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
          {categoriesLoading && activeCategories.length === 0 ? (
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
          ) : activeCategories.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No {kind} categories yet —{' '}
              <Link to="/categories" className="underline">
                create one
              </Link>
            </p>
          ) : (
            <CategoryChips
              categories={activeCategories}
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

        {user && (
          <RecentlyAdded
            userId={user.id}
            pending={pending}
            categories={categories}
            baseCode={baseCode}
            refreshKey={recentRefreshKey}
          />
        )}

        {/* End of the scrolling content, NOT the sticky footer below. The
            footer holds the Add button at a fixed height that the on-screen
            keyboard pushes against, and sonner stacks its toasts over that
            same corner — a permanent line there would eat touch-target room
            and sit under the save toasts. Here it scrolls away and interferes
            with nothing. */}
        <AppVersion className="justify-center pt-2" />
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
