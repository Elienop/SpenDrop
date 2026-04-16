import { useEffect, useRef, useState, type Ref } from 'react';
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
import { cn, selectAllOnFocus } from '@/lib/utils';
import { formatCurrency } from '@/lib/format';

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
  const focusedRef = useRef(false);

  useEffect(() => {
    if (!focusedRef.current) {
      setRawInput(valueToRaw(value));
    }
  }, [value]);

  const visible = currencies.filter((c) => {
    if (!hideInactive) return true;
    return (c as Currency & { is_active?: boolean }).is_active !== false;
  });

  const rate = rateFor(currency);
  const showPreview = currency !== baseCode && rate != null && rate > 0;
  const previewValue = showPreview && rate ? value / rate : 0;

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-stretch">
        <Input
          type="number"
          step="0.01"
          min="0"
          value={rawInput}
          onChange={(e) => {
            const next = e.target.value;
            setRawInput(next);
            onValueChange(next === '' ? 0 : Number(next));
          }}
          onFocus={(e) => {
            focusedRef.current = true;
            selectAllOnFocus(e);
          }}
          onBlur={() => {
            focusedRef.current = false;
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
            >
              {loading ? <Loader2 className="size-3 animate-spin" /> : currency}
            </Button>
          </PopoverTrigger>
          <PopoverContent className="p-0" align="end">
            <Command>
              <CommandInput placeholder="Search currency..." />
              <CommandList>
                <CommandEmpty>No currency found.</CommandEmpty>
                {visible.map((c) => {
                  const itemDisabled =
                    rateFor(c.code) == null && c.code !== baseCode;
                  const inactive =
                    (c as Currency & { is_active?: boolean }).is_active === false;
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
        <span className="text-xs text-muted-foreground">
          &asymp; {formatCurrency(previewValue, baseCode)}
        </span>
      )}
      {error && (
        <span className={cn('text-xs text-destructive')}>{error}</span>
      )}
    </div>
  );
}
