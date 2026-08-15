import {
  useCallback,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react';
import * as PopoverPrimitive from '@radix-ui/react-popover';
import { Popover, PopoverContent } from '@/components/ui/popover';
import { Command, CommandGroup, CommandItem, CommandList } from '@/components/ui/command';
import { useIsCoarsePointer } from '@/hooks/useIsCoarsePointer';
import { cn } from '@/lib/utils';

interface TagInputProps {
  value: string; // comma-separated
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  suggestions?: string[];
}

const MAX_TOUCH_MATCHES = 5;

/**
 * TagInput — comma-separated tag entry with autocomplete.
 *
 * Mirrors AutocompleteInput's a11y strategy: the desktop "ghost" is a sibling
 * overlay `<span>` (not selected text inside the input), and a `role="combobox"`
 * + `aria-live` mirror announce the completion to screen readers. Touch uses a
 * shadcn Popover + Command dropdown anchored to the input via PopoverAnchor so
 * the input keeps focus.
 *
 * Key-propagation contract (see spec 2026-04-18-inline-edit-keyboard-shortcuts):
 *   - Keys this component ACTS on (Enter/",", on a non-empty buffer or visible
 *     ghost; Tab/ArrowRight/End on ghost; Escape on ghost or open popover) are
 *     consumed with BOTH preventDefault() and stopPropagation().
 *   - Enter on an EMPTY buffer with no ghost bubbles — this is the "Enter twice
 *     after your last tag" pattern: first Enter commits the chip, second Enter
 *     reaches the row handler and saves.
 *   - Cmd/Ctrl+Enter short-circuits at the top so the row-level force-save is
 *     never intercepted.
 */
export function TagInput({
  value,
  onChange,
  placeholder = 'Add tag...',
  className,
  suggestions = [],
}: TagInputProps) {
  const [input, setInput] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const isCoarse = useIsCoarsePointer();
  const generatedId = useId();
  const listboxId = `${generatedId}-taginput-listbox`;

  // Escape-dismisses the ghost for the exact prefix at time of press.
  const [suppressedPrefix, setSuppressedPrefix] = useState<string | null>(null);
  const [touchOpen, setTouchOpen] = useState(false);

  // Guards the input's onBlur against committing the typed buffer when
  // focus is leaving to a tap on the popover listbox. Set synchronously
  // in the popover's capture-phase pointerdown (which fires before the
  // blur event is dispatched) and cleared on the next microtask so it
  // suppresses exactly one blur. The prior heuristic checked
  // `relatedTarget.closest('[data-radix-popper-content-wrapper]')`, which
  // failed whenever Radix changed its focus order or when relatedTarget
  // was null (focus briefly on `body` during the transition).
  const isPickingRef = useRef(false);

  const tags = useMemo(
    () => (value ? value.split(',').map((t) => t.trim()).filter(Boolean) : []),
    [value],
  );

  const addTag = useCallback(
    (tag: string) => {
      const trimmed = tag.trim();
      if (!trimmed) return;
      if (tags.includes(trimmed)) return;
      onChange([...tags, trimmed].join(','));
      setInput('');
      setSuppressedPrefix(null);
      setTouchOpen(false);
    },
    [onChange, tags],
  );

  const removeTag = useCallback(
    (index: number) => {
      const next = tags.filter((_, i) => i !== index);
      onChange(next.join(','));
    },
    [onChange, tags],
  );

  // --- Desktop: compute single inline match ---------------------------------
  const match = useMemo(() => {
    if (!input) return '';
    const lower = input.toLowerCase();
    const found = suggestions.find(
      (s) =>
        s.toLowerCase().startsWith(lower) &&
        s.length > input.length &&
        !tags.includes(s),
    );
    return found ?? '';
  }, [input, suggestions, tags]);

  const ghostActive = Boolean(match) && suppressedPrefix !== input && !isCoarse;
  const ghostTail = ghostActive ? match.slice(input.length) : '';

  // --- Touch: compute top-N match list --------------------------------------
  const touchMatches = useMemo(() => {
    if (!input) return [] as string[];
    const lower = input.toLowerCase();
    return suggestions
      .filter(
        (s) =>
          s.toLowerCase().includes(lower) && s !== input && !tags.includes(s),
      )
      .slice(0, MAX_TOUCH_MATCHES);
  }, [input, suggestions, tags]);

  const shouldTouchOpen = isCoarse && touchMatches.length > 0 && input.length > 0;
  const popoverOpen = shouldTouchOpen && touchOpen;

  const handleInputChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const next = e.target.value;
      if (suppressedPrefix !== null && next !== suppressedPrefix) {
        setSuppressedPrefix(null);
      }
      if (isCoarse) {
        if (next.length > 0) setTouchOpen(true);
        else setTouchOpen(false);
      }
      setInput(next);
    },
    [suppressedPrefix, isCoarse],
  );

  function handleKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    // Modifier bypass — row-level Cmd/Ctrl+Enter must always see the event.
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      return;
    }

    // Touch branch.
    if (isCoarse) {
      if (e.key === 'Escape' && touchOpen) {
        e.preventDefault();
        e.stopPropagation();
        setTouchOpen(false);
        return;
      }
      if (e.key === 'Enter') {
        // Commit typed buffer only if there is one; otherwise bubble so the row
        // handler can save. `addTag` would early-return on empty, but we also
        // must not call preventDefault in that case or Enter is swallowed.
        if (input.length > 0) {
          e.preventDefault();
          e.stopPropagation();
          addTag(input);
        }
        return;
      }
      if (e.key === ',') {
        // Always swallow the literal ',' so it never lands in the buffer,
        // but only consume (stopPropagation) when we actually commit a tag.
        e.preventDefault();
        if (input.length > 0) {
          e.stopPropagation();
          addTag(input);
        }
        return;
      }
      if (e.key === 'Backspace' && input === '' && tags.length > 0) {
        removeTag(tags.length - 1);
      }
      return;
    }

    // Desktop branch.
    const caretAtEnd =
      e.currentTarget.selectionStart === input.length &&
      e.currentTarget.selectionEnd === input.length;

    if (e.key === 'Tab' && !e.shiftKey && ghostActive) {
      e.preventDefault();
      e.stopPropagation();
      addTag(match);
      return;
    }

    if (e.key === 'ArrowRight' && ghostActive && caretAtEnd) {
      e.preventDefault();
      e.stopPropagation();
      addTag(match);
      return;
    }

    if (e.key === 'End' && ghostActive) {
      e.preventDefault();
      e.stopPropagation();
      addTag(match);
      return;
    }

    if (e.key === 'Enter') {
      // Two commit paths:
      //   - ghost visible → commit the ghost suggestion (consumed)
      //   - buffer non-empty → commit the typed buffer (consumed)
      //   - buffer empty, no ghost → bubble so the row handler can save
      if (ghostActive) {
        e.preventDefault();
        e.stopPropagation();
        addTag(match);
        return;
      }
      if (input.length > 0) {
        e.preventDefault();
        e.stopPropagation();
        addTag(input);
        return;
      }
      return; // bubble
    }

    if (e.key === ',') {
      // Always swallow the literal ',' so it never lands in the buffer,
      // but only consume (stopPropagation) when we actually commit a tag.
      e.preventDefault();
      if (input.length > 0) {
        e.stopPropagation();
        addTag(input);
      }
      return;
    }

    if (e.key === 'Escape' && ghostActive) {
      e.preventDefault();
      e.stopPropagation();
      setSuppressedPrefix(input);
      return;
    }

    if (e.key === 'Backspace' && input === '' && tags.length > 0) {
      removeTag(tags.length - 1);
    }
  }

  // Shared aria props on the inner <input>.
  const commonAria = {
    role: 'combobox' as const,
    'aria-autocomplete': (isCoarse ? 'list' : 'inline') as 'list' | 'inline',
    'aria-controls': listboxId,
    'aria-expanded': ghostActive || popoverOpen,
    autoComplete: 'off' as const,
  };

  const innerInput = (
    <input
      ref={inputRef}
      type="text"
      className="w-full border-0 bg-transparent px-1 py-0.5 text-sm text-foreground outline-none placeholder:text-muted-foreground"
      value={input}
      onChange={handleInputChange}
      onKeyDown={handleKeyDown}
      onBlur={() => {
        // Don't commit the typed buffer when focus is leaving to the
        // popover listbox — the tap on an option will addTag itself, and
        // committing "g" before "groceries" would add a stray tag. The
        // flag is set synchronously by PopoverContent's capture-phase
        // pointerdown and cleared on the next microtask.
        if (isPickingRef.current) return;
        addTag(input);
      }}
      placeholder={tags.length === 0 ? placeholder : ''}
      aria-label="Add tag"
      {...commonAria}
    />
  );

  return (
    /*
      The field's OUTER BOX. It looks like the control, but it is not one: the
      control is the `<input>` below, which carries the combobox role and the
      accessible name. This div only draws the border, lays the tag pills out
      and forwards a click on its whitespace to that input — the affordance a
      user expects from anything shaped like a text field.

      `role="presentation"` is that statement, and it is what stops jsx-a11y's
      `click-events-have-key-events` / `no-static-element-interactions` (Sonar
      typescript:S1082 / S6848) from reporting the handler: both rules return
      early on a presentation role, because — quoting the second rule's own
      source — "presentation is an intentional signal from the author that this
      element is not meant to be perceivable".

      What the rules guard against is functionality reachable ONLY by pointer,
      and there is none here: keyboard users Tab straight to the input, which
      is where the click sends focus anyway. Adding a key listener instead
      would be actively wrong — keystrokes typed INTO the input bubble to this
      div, so the handler would fire on every character.

      The role changes nothing in the accessibility tree: a plain div is
      already `generic`, i.e. already not announced. It is not inherited, so
      the tag pills and their remove buttons keep their own semantics, and with
      no `tabindex` and no global `aria-*` attribute here the box stays out of
      the tab order (and the role stays honoured — ARIA drops `presentation` on
      an element that is focusable or carries a global ARIA attribute).
    */
    <div
      role="presentation"
      className={cn(
        'flex flex-wrap items-center gap-1 min-h-10 rounded-md border border-input bg-background px-2 py-1 cursor-text transition-colors focus-within:border-ring focus-within:ring-1 focus-within:ring-ring',
        className,
      )}
      onClick={() => inputRef.current?.focus()}
    >
      {tags.map((tag, i) => (
        <span
          key={`${tag}-${i}`}
          className="inline-flex items-center gap-1 rounded-sm bg-primary/15 px-2 py-0.5 text-xs font-semibold text-primary whitespace-nowrap"
        >
          {tag}
          <button
            type="button"
            className="border-0 bg-transparent p-0 text-sm leading-none text-primary opacity-70 transition-opacity hover:opacity-100"
            onClick={(e) => {
              e.stopPropagation();
              removeTag(i);
            }}
            aria-label={`Remove tag ${tag}`}
          >
            ×
          </button>
        </span>
      ))}
      <div className="relative flex-1 min-w-[60px]">
        {/* Desktop ghost overlay + aria-live mirror */}
        {!isCoarse && (
          <>
            {ghostTail && (
              <div
                aria-hidden
                className="pointer-events-none absolute inset-0 flex items-center overflow-hidden whitespace-nowrap px-1 py-0.5 text-sm text-muted-foreground/40"
              >
                <span className="invisible">{input}</span>
                <span data-testid="taginput-ghost">{ghostTail}</span>
              </div>
            )}
            <span
              id={listboxId}
              aria-live="polite"
              className="sr-only"
              data-testid="taginput-live"
            >
              {ghostActive ? match : ''}
            </span>
          </>
        )}

        {isCoarse ? (
          <Popover open={popoverOpen} onOpenChange={setTouchOpen}>
            <PopoverPrimitive.Anchor asChild>{innerInput}</PopoverPrimitive.Anchor>
            <PopoverContent
              align="start"
              sideOffset={4}
              onOpenAutoFocus={(e: Event) => e.preventDefault()}
              onCloseAutoFocus={(e: Event) => e.preventDefault()}
              onPointerDownCapture={() => {
                // Capture phase runs root→target before the sync blur the
                // pointerdown will trigger, so the flag is set before the
                // input's onBlur reads it. Microtask drain happens after
                // the blur but before click/onSelect, so exactly one blur
                // is suppressed.
                isPickingRef.current = true;
                queueMicrotask(() => {
                  isPickingRef.current = false;
                });
              }}
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
                          addTag(picked);
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
        ) : (
          innerInput
        )}
      </div>
    </div>
  );
}
