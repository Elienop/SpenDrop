import { useState, type Ref } from 'react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Loader2 } from 'lucide-react';
import type { Currency } from '@/api/types';
import { selectAllOnFocus } from '@/lib/utils';
import { formatCurrency } from '@/lib/format';
import {
  dollarsToCents,
  foreignMoneyUnchanged,
  type StoredMoney,
} from '@/lib/currency';

/**
 * Composed amount + currency picker. **The amount `<Input>` is not self-labeled** —
 * consumers must wrap this component with a `<Label>` / `<FormLabel>` bound by
 * `id`, or use `aria-labelledby` on a surrounding element, for screen-reader
 * accessibility. The integrating parent in Task 3.1 / 3.3 owns the label.
 */
export interface AmountCurrencyInputProps {
  value: number;
  onValueChange: (v: number) => void;
  currency: string;
  onCurrencyChange: (code: string) => void;
  baseCode: string;
  currencies: Currency[];
  hideInactive: boolean;
  rateFor: (code: string) => number | null;
  loading?: boolean;
  error?: string | null;
  disabled?: boolean;
  inputRef?: Ref<HTMLInputElement>;
  dataEntryField?: string;
  /**
   * The money already stored on the transaction being edited. Omit it on the
   * create surfaces — its absence is what keeps their `≈` preview on the live
   * conversion, which is the right number for a row that does not exist yet.
   * See the preview derivation below for what it changes when present.
   */
  storedMoney?: StoredMoney | null;
  /**
   * Whether the Refund toggle beside this control is on.
   *
   * The digits in this input are a MAGNITUDE — the sign lives on
   * `AmountSignToggle` and nowhere else — so this is what lets the preview
   * reconstruct the signed amount the save will actually send.
   *
   * It changes exactly one thing, and only where `storedMoney` is also
   * present: whether the freeze matches. `foreignMoneyUnchanged` compares
   * SIGNED cents because the server does, so a stored -150,000 LBP refund
   * whose toggle has been flipped off is the user changing the money and
   * re-prices at today's rate. Without this the magnitudes would still match,
   * the preview would promise the frozen figure, and the save would store a
   * different one. The create surfaces omit it for the same reason they omit
   * `storedMoney`: there is no stored value to freeze against.
   */
  negative?: boolean;
  /** Optional DOM id to apply to the inner amount `<input>`. When composed
   *  inside a shadcn `FormControl`, Radix `Slot` injects `id={formItemId}`
   *  onto this component; forwarding that id to the actual input is what
   *  makes `<Label htmlFor={formItemId}>` work. */
  id?: string;
}

/**
 * Format the numeric `value` prop back into the raw input string.
 * We render `0` as an empty string so the placeholder state looks
 * clean in the entry row, matching the pattern used by
 * `TransactionEntryRow`. Anything non-zero renders as its numeric
 * toString so the user sees the committed amount.
 */
function valueToRaw(value: number): string {
  return value ? String(value) : '';
}

export function AmountCurrencyInput({
  value,
  onValueChange,
  currency,
  onCurrencyChange,
  baseCode,
  currencies,
  hideInactive,
  rateFor,
  loading = false,
  error = null,
  disabled = false,
  inputRef,
  dataEntryField,
  storedMoney = null,
  negative = false,
  id,
}: AmountCurrencyInputProps) {
  const [open, setOpen] = useState(false);

  // Local buffer for the amount input. While the user is typing (input
  // focused) this holds the raw text they've entered, independent of
  // the committed `value` prop, so a parent re-render (e.g. currency
  // change) does not stomp on in-progress input. When the input is
  // NOT focused we treat `value` as the source of truth and sync the
  // buffer from it — that way external updates (form reset, API round
  // trip) flow through correctly.
  const [rawInput, setRawInput] = useState<string>(() => valueToRaw(value));
  const [lastSyncedValue, setLastSyncedValue] = useState(value);
  const [isFocused, setIsFocused] = useState(false);

  // Sync the raw buffer from `value` during render rather than in a useEffect.
  // React docs recommend this pattern ("Adjusting state when a prop changes")
  // — the useEffect variant was flagged by react-hooks/set-state-in-effect.
  // Focus must be tracked in state (not a ref) because react-hooks/refs
  // disallows reading `ref.current` during render.
  // While focused we intentionally ignore incoming `value` so in-flight
  // keystrokes aren't stomped; blur re-syncs via the `onBlur` handler below.
  if (lastSyncedValue !== value) {
    setLastSyncedValue(value);
    if (!isFocused) {
      setRawInput(valueToRaw(value));
    }
  }

  const visible = currencies.filter((c) => {
    if (!hideInactive) return true;
    // `!== false` intentionally treats `undefined` as active so payloads
    // from backends that haven't started emitting `is_active` yet keep
    // every currency visible. See `Currency` type in `api/types.ts`.
    return c.is_active !== false;
  });

  const rate = rateFor(currency);
  const showPreview = currency !== baseCode && rate != null && rate > 0;

  // What today's rate makes this worth. Correct for a NEW transaction — a row
  // that does not exist yet really is priced at today's rate.
  const liveValue = showPreview && rate ? value / rate : 0;

  // The signed amount the save will send, rebuilt from the two halves the user
  // enters it in: the magnitude in the box and the Refund toggle beside it.
  // Only the freeze predicate is asked about it — the figure below stays in
  // the magnitude the digits above are written in, because the toggle carries
  // the direction for the whole amount block and a preview that grew its own
  // minus would be a second sign on the same money.
  const signedValue = negative ? -value : value;

  // ...and wrong for an edit that leaves the foreign money alone. Saving one
  // of those does not re-price the row: the server carries the stored base
  // value forward untouched (database.foreignMoneyUnchanged), so once the rate
  // has moved, the live conversion is a number the save will not produce. The
  // preview's whole job is to say what saving will store, and a confident wrong
  // figure is worse than none, so it shows the stored value instead.
  //
  // `storedMoney` reads from the live transaction rather than a snapshot taken
  // when the editor opened, so a refetch landing mid-edit moves this with the
  // row the server will actually compare against.
  const frozen =
    storedMoney != null &&
    foreignMoneyUnchanged(
      storedMoney,
      { amount: signedValue, currency },
      baseCode,
    );
  // `Math.abs` for the same reason `liveValue` needs none: both figures are the
  // magnitude of the base-currency value, and a stored refund's is negative.
  const previewValue = frozen ? Math.abs(storedMoney.amount) : liveValue;

  // The two figures disagree only when the rate moved since the row was
  // recorded, and that is the one case where a bare correct number reads as a
  // bug: the user knows the rate they set in Settings, the arithmetic does not
  // check out, and the obvious "fix" is to retype the amount — the single
  // action that re-prices the row and destroys the value being preserved. So
  // the qualifier appears exactly when there is something to say, and never on
  // the ordinary path. Compared in cents because that is the precision the
  // figure is rendered at; a sub-cent gap nobody can see does not earn words.
  const rateHasMoved =
    frozen &&
    showPreview &&
    dollarsToCents(previewValue) !== dollarsToCents(liveValue);

  return (
    // Preview/error render in normal flow below the input row, NOT absolutely
    // positioned. Two consequences:
    //
    //  1. Wrapper height equals the Input's `h-10` when neither preview nor
    //     error is present — so the control sits flush with peer fields
    //     (Date, Description, Category, Tags). A prior iteration reserved
    //     `pb-5` unconditionally and anchored the spans with `absolute
    //     bottom: 0` to keep the height constant; that fixed the "jump
    //     on toggle" symptom but made the wrapper permanently 20px taller
    //     than siblings, which looked worse.
    //
    //  2. When the preview or error does appear, the wrapper grows
    //     downward by ~20px — inside its own rendered box. That descent
    //     stays within any `.overflow-auto` ancestor's clientHeight, so
    //     the transactions table does NOT get a spurious vertical
    //     scrollbar (the bug that the earlier `top: 100%` attempt
    //     triggered by projecting the span past the wrapper).
    //
    // Consumers that want the amount row to stay top-aligned while this
    // grows should use `items-start` on the surrounding flex container
    // (see TransactionEntryRow's form). The inline-edit TableCell has no
    // sibling alignment constraint, so growth there just extends the
    // row height — acceptable UX while actively editing.
    <div className="flex flex-col">
      <div className="flex items-stretch">
        <Input
          id={id}
          type="number"
          step="0.01"
          // `min="0"` STAYS now that amounts can be negative, and it is a
          // feature rather than leftover strictness. The sign of a transaction
          // comes from the Refund toggle (`AmountSignToggle`) and from nothing
          // else, so a minus in the digits is always a typo — and this is what
          // marks it `:invalid` and clamps the spinner at zero. The consuming
          // surfaces refuse it for real: the entry row's schema is `.positive()`
          // and QuickAdd's submit gate is `> 0`. Dropping it here would make a
          // slipped keystroke a silent sign flip on the two edit surfaces,
          // which are the ones where it would corrupt an existing row.
          min="0"
          value={rawInput}
          onChange={(e) => {
            const next = e.target.value;
            setRawInput(next);
            // `type="number"` inputs can hold intermediate strings like `"1e"`,
            // `"-"`, `"."`, `"1.2.3"`, or pasted garbage. `Number()` of those
            // yields `NaN`, which would propagate into the parent state,
            // preview math, and Zod validation. Guard with `isFinite` so we
            // emit a clean `0` until the user produces a parseable number.
            const n = Number(next);
            onValueChange(next === '' || !Number.isFinite(n) ? 0 : n);
          }}
          onFocus={(e) => {
            setIsFocused(true);
            selectAllOnFocus(e);
          }}
          onBlur={() => {
            setIsFocused(false);
            // Re-sync from the authoritative `value` on blur so any
            // parent-side clamping/rounding is reflected in the input.
            setRawInput(valueToRaw(value));
          }}
          ref={inputRef}
          data-entry-field={dataEntryField}
          disabled={disabled}
          className="rounded-r-none font-mono tabular-nums"
        />
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <Button
              type="button"
              variant="outline"
              disabled={loading || disabled}
              className="rounded-l-none border-l-0 px-2 font-mono text-xs"
              aria-label={`Currency: ${currency}`}
              data-entry-field="currency"
            >
              {loading && <Loader2 className="mr-1 size-3 animate-spin" />}
              {currency}
            </Button>
          </PopoverTrigger>
          <PopoverContent className="p-0" align="end">
            <Command>
              <CommandInput placeholder="Search currency..." />
              <CommandList>
                <CommandEmpty>No currency found.</CommandEmpty>
                {visible.map((c) => {
                  // The base currency is always selectable even without a valid
                  // rate, because "convert from base to base" is a no-op
                  // (amount === original_amount, no original_* stored).
                  const itemDisabled =
                    rateFor(c.code) == null && c.code !== baseCode;
                  const inactive = c.is_active === false;
                  return (
                    <CommandItem
                      key={c.code}
                      value={c.code}
                      aria-disabled={itemDisabled}
                      disabled={itemDisabled}
                      title={
                        itemDisabled
                          ? 'No exchange rate configured — set in Settings'
                          : undefined
                      }
                      onSelect={() => {
                        if (itemDisabled) return;
                        onCurrencyChange(c.code);
                        setOpen(false);
                      }}
                    >
                      <span className="font-mono">{c.code}</span>
                      <span className="ml-2 text-muted-foreground">
                        {c.name}
                      </span>
                      {inactive && (
                        <span className="ml-auto text-xs text-muted-foreground">
                          (inactive)
                        </span>
                      )}
                    </CommandItem>
                  );
                })}
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
      </div>
      {showPreview && (
        <span className="pointer-events-none mt-1 text-xs text-muted-foreground">
          &asymp; {formatCurrency(previewValue, baseCode)}
          {rateHasMoved && (
            // `pointer-events-auto` re-enables hover on this span alone: the
            // parent switches pointer events off so the preview cannot swallow
            // a click aimed at the input, and that also suppresses the native
            // `title` tooltip. The visible text is already true without the
            // tooltip — the tooltip only carries the "why", which does not fit
            // in a table cell.
            <span
              className="pointer-events-auto"
              title="The exchange rate has changed since this was recorded. Saving keeps the recorded value; changing the amount or the currency re-prices it at today's rate."
            >
              {' '}
              (as recorded)
            </span>
          )}
        </span>
      )}
      {error && (
        <span className="pointer-events-none mt-1 text-xs text-destructive">
          {error}
        </span>
      )}
    </div>
  );
}
