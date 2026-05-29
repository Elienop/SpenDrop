import {
  forwardRef,
  useCallback,
  useId,
  useMemo,
  useState,
  type InputHTMLAttributes,
  type KeyboardEvent,
  type ChangeEvent,
} from 'react';
import * as PopoverPrimitive from '@radix-ui/react-popover';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent } from '@/components/ui/popover';
import { Command, CommandGroup, CommandItem, CommandList } from '@/components/ui/command';
import { useIsCoarsePointer } from '@/hooks/useIsCoarsePointer';
import { cn } from '@/lib/utils';

export interface AutocompleteInputProps
  extends InputHTMLAttributes<HTMLInputElement> {
  suggestions: string[];
  /** Fired when the user accepts a suggestion (ghost tab/arrow-right/end, or tapping a popover item). */
  onAccept?: (value: string) => void;
}

const MAX_TOUCH_MATCHES = 5;

/**
 * AutocompleteInput — inline ghost-text completion (desktop) + popover list (touch).
 *
 * Accessibility tradeoff note:
 *   The desktop "ghost" is drawn as a sibling overlay `<span>` rather than as real
 *   selected text inside the input. This keeps the component controlled-friendly
 *   for React-Hook-Form (single source of truth on `value`). To compensate, we
 *   wire `role="combobox"` + `aria-autocomplete="inline"` on the input and mirror
 *   the full match into an `aria-live="polite"` span so screen readers announce
 *   the completion as the user types.
 *
 * Key-propagation contract (see spec 2026-04-18-inline-edit-keyboard-shortcuts):
 *   - Keys this component ACTS on (Enter/Escape with ghost, Tab/ArrowRight/End
 *     with ghost, Escape to close the touch popover) are consumed with BOTH
 *     preventDefault() and stopPropagation(), so the row-level keydown handler
 *     does NOT see them.
 *   - Keys this component does NOT act on (Enter without a ghost, Escape without
 *     a ghost, any Cmd/Ctrl+Enter) bubble to the parent unchanged.
 *   - Cmd/Ctrl+Enter short-circuits at the TOP of handleKeyDown so the row-level
 *     force-save can never be intercepted by widget-local Enter handling.
 */
export const AutocompleteInput = forwardRef<
  HTMLInputElement,
  AutocompleteInputProps
>(function AutocompleteInput(
  {
    suggestions,
    onAccept,
    value,
    onChange,
    onKeyDown,
    className,
    id,
    ...props
  },
  ref,
) {
  const isCoarse = useIsCoarsePointer();
  const text = String(value ?? '');
  const generatedId = useId();
  const listboxId = `${id ?? generatedId}-autocomplete-listbox`;

  // Escape-dismisses the ghost for the exact prefix at time of press.
  // If the user keeps typing (prefix changes) the suppression is released.
  const [suppressedPrefix, setSuppressedPrefix] = useState<string | null>(null);
  const [touchOpen, setTouchOpen] = useState(false);

  // --- Desktop: compute single inline match ---------------------------------
  const match = useMemo(() => {
    if (!text) return '';
    const lower = text.toLowerCase();
    const found = suggestions.find(
      (s) => s.toLowerCase().startsWith(lower) && s.length > text.length,
    );
    return found ?? '';
  }, [text, suggestions]);

  const ghostActive = Boolean(match) && suppressedPrefix !== text;
  const ghostTail = ghostActive ? match.slice(text.length) : '';

  // --- Touch: compute top-N match list --------------------------------------
  const touchMatches = useMemo(() => {
    if (!text) return [] as string[];
    const lower = text.toLowerCase();
    return suggestions
      .filter((s) => s.toLowerCase().includes(lower) && s !== text)
      .slice(0, MAX_TOUCH_MATCHES);
  }, [text, suggestions]);

  const shouldTouchOpen = isCoarse && touchMatches.length > 0 && text.length > 0;

  const handleInputChange = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      // Releasing the Escape suppression: once prefix diverges, re-enable ghost.
      if (suppressedPrefix !== null && e.target.value !== suppressedPrefix) {
        setSuppressedPrefix(null);
      }
      if (isCoarse) {
        // Open popover when the user types something non-empty.
        if (e.target.value.length > 0) setTouchOpen(true);
        else setTouchOpen(false);
      }
      onChange?.(e);
    },
    [onChange, suppressedPrefix, isCoarse],
  );

  const acceptMatch = useCallback(
    (accepted: string) => {
      onAccept?.(accepted);
      setSuppressedPrefix(null);
    },
    [onAccept],
  );

  // --- Desktop key handling -------------------------------------------------
  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      // Modifier bypass — row-level Cmd/Ctrl+Enter must always see the event.
      // This runs before any branch so no widget-local key handling can intercept.
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        onKeyDown?.(e);
        return;
      }

      if (isCoarse) {
        // Touch: only Escape closes the popover. Consume when we actually close.
        if (e.key === 'Escape' && touchOpen) {
          e.preventDefault();
          e.stopPropagation();
          setTouchOpen(false);
          return;
        }
        onKeyDown?.(e);
        return;
      }

      // Desktop branch.
      const caretAtEnd =
        e.currentTarget.selectionStart === text.length &&
        e.currentTarget.selectionEnd === text.length;

      // Tab: accept ONLY when ghost visible; otherwise let native focus advance.
      if (e.key === 'Tab' && !e.shiftKey && ghostActive && match !== text) {
        e.preventDefault();
        e.stopPropagation();
        acceptMatch(match);
        return;
      }

      if (e.key === 'ArrowRight' && ghostActive && caretAtEnd) {
        e.preventDefault();
        e.stopPropagation();
        acceptMatch(match);
        return;
      }

      if (e.key === 'End' && ghostActive) {
        e.preventDefault();
        e.stopPropagation();
        acceptMatch(match);
        return;
      }

      // Enter with ghost visible: accept the ghost. Consumed — does not bubble.
      if (e.key === 'Enter' && ghostActive) {
        e.preventDefault();
        e.stopPropagation();
        acceptMatch(match);
        return;
      }

      // Enter without ghost: fall through — the row-level handler will save.
      // (No preventDefault, no stopPropagation, no return.)

      if (e.key === 'Escape' && ghostActive) {
        // Consume Escape when the ghost is actually visible: suppress it for this
        // prefix and STOP here. The prior "don't preventDefault so parent Escape
        // handlers fire" comment was wrong for the layered-edit model — letting
        // Escape bubble while also handling it causes the GitHub 2023 regression
        // (single Esc press closes picker AND cancels the row).
        e.preventDefault();
        e.stopPropagation();
        setSuppressedPrefix(text);
        return;
      }

      onKeyDown?.(e);
    },
    [isCoarse, ghostActive, match, text, touchOpen, onKeyDown, acceptMatch],
  );

  // Shared aria props on the input.
  //
  // aria-controls is deliberately NOT here: on desktop the only element bearing
  // listboxId is the sr-only aria-live announcement <span>, not a popup/listbox,
  // so advertising aria-controls there is misleading (it claims a controlled
  // popup that does not exist for the inline-ghost pattern). On touch the
  // popover IS a real controlled region, so the touch branch re-adds
  // aria-controls inline, gated on `open`.
  const commonAria = {
    role: 'combobox' as const,
    'aria-autocomplete': (isCoarse ? 'list' : 'inline') as 'list' | 'inline',
    autoComplete: 'off' as const,
  };

  // --- Render: Touch branch -------------------------------------------------
  if (isCoarse) {
    const open = shouldTouchOpen && touchOpen;

    // Radix Popover wraps an Anchor that positions the content relative to the
    // input. We use PopoverAnchor (not PopoverTrigger) so the input keeps focus
    // — trigger semantics would toggle open/close on every click.
    const inputRef = ref;

    return (
      <Popover open={open} onOpenChange={setTouchOpen}>
        <PopoverPrimitive.Anchor asChild>
          <Input
            ref={inputRef}
            id={id}
            value={value}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            className={cn(className)}
            aria-expanded={open}
            // Only advertise the controlled popup while it is actually rendered,
            // pointed at the PopoverContent container (which wraps cmdk's
            // role="listbox"). Referencing the popup container is valid ARIA.
            aria-controls={open ? listboxId : undefined}
            {...commonAria}
            {...props}
          />
        </PopoverPrimitive.Anchor>
        <PopoverContent
          align="start"
          sideOffset={4}
          // Prevent the popover from stealing focus when it opens — the input
          // must remain the typing surface. Users tap a row to pick.
          onOpenAutoFocus={(e: Event) => e.preventDefault()}
          // Don't refocus the trigger (there is none in the usual sense) on close.
          onCloseAutoFocus={(e: Event) => e.preventDefault()}
          className="w-[--radix-popover-trigger-width] p-0"
          id={listboxId}
        >
          <Command shouldFilter={false}>
            <CommandList>
              <CommandGroup>
                {touchMatches.map((s) => (
                  <CommandItem
                    key={s}
                    value={s}
                    onSelect={(picked) => {
                      acceptMatch(picked);
                      setTouchOpen(false);
                    }}
                  >
                    {s}
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
    );
  }

  // --- Render: Desktop branch -----------------------------------------------
  return (
    <div className="relative rounded-md bg-background">
      {/* Visual ghost overlay: aria-hidden because we announce via live region below. */}
      <div
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-0 flex items-center overflow-hidden whitespace-nowrap px-3 text-sm text-muted-foreground/40',
          className,
        )}
      >
        <span className="invisible">{text}</span>
        <span data-testid="autocomplete-ghost">{ghostTail}</span>
      </div>

      {/* Screen-reader announcement for the full match when ghost is visible. */}
      <span
        id={listboxId}
        aria-live="polite"
        className="sr-only"
        data-testid="autocomplete-live"
      >
        {ghostActive ? match : ''}
      </span>

      <Input
        ref={ref}
        id={id}
        value={value}
        onChange={handleInputChange}
        onKeyDown={handleKeyDown}
        className={cn('bg-transparent', className)}
        aria-expanded={ghostActive}
        {...commonAria}
        {...props}
      />
    </div>
  );
});
