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
  const previewValue = showPreview && rate ? value / rate : 0;

  return (
    // `relative` anchors the absolutely-positioned preview/error spans below.
    // Keeping them out of the flex-col flow means toggling currency does NOT
    // change the component's height — the parent form row can stay aligned
    // via `items-end` without the amount field "jumping" upward when the
    // `≈ <base>` preview appears. Same stability applies inside the inline
    // edit TableCell: the table row height stays constant instead of the
    // amount input shifting within its cell.
    //
    // `pb-5` reserves ~20px below the input for the absolute preview/error.
    // The preview/error spans use `bottom: 0` so their bottom edge sits at
    // the bottom of the padding box — inside the reserved strip. Using
    // `top: 100%` instead places the span's top edge at the padding-box
    // bottom and extends it downward past the wrapper's own rendered box,
    // which is what previously triggered a spurious vertical scrollbar on
    // the transactions table (the span overflowed the table's
    // `.overflow-auto` ancestor's clientHeight). Reservation is at the
    // component level — always present regardless of currency — so the
    // entry-row and inline-edit row heights stay constant, which is the
    // whole point of the absolute-positioning layout.
    <div className="relative flex flex-col pb-5">
      <div className="flex items-stretch">
        <Input
          id={id}
          type="number"
          step="0.01"
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
        <span className="pointer-events-none absolute bottom-0 left-0 text-xs text-muted-foreground">
          &asymp; {formatCurrency(previewValue, baseCode)}
        </span>
      )}
      {error && (
        <span className="pointer-events-none absolute bottom-0 left-0 text-xs text-destructive">
          {error}
        </span>
      )}
    </div>
  );
}
