import { useMemo } from 'react';
import { Button } from '@/components/ui/button';

interface QuickAddSuggestionsProps {
  /** All known past descriptions, ranked best-first (typically by frequency). */
  suggestions: string[];
  /** Current description draft the user is typing (live-parsed in freeform). */
  query: string;
  /** Called when the user picks a chip. */
  onPick: (suggestion: string) => void;
}

const MAX_CHIPS = 3;

/**
 * Horizontal chip strip rendered just below the freeform QuickAdd input.
 * Suggests up to three past description phrases by case-insensitive prefix
 * match on the current draft; tap accepts the suggestion (the parent owns
 * the input and rewrites `raw` accordingly).
 *
 * Renders nothing when the filtered list is empty so the strip never
 * becomes visual noise. The exact-match case (typed text matches a
 * suggestion verbatim with no longer match available) is also skipped —
 * suggesting back what the user already typed adds no value.
 *
 * The Tab-to-accept-first accelerator is handled by the parent's input
 * keydown handler (locality > a window listener), so this component
 * stays a pure render of the visible options.
 */
export function QuickAddSuggestions({
  suggestions,
  query,
  onPick,
}: QuickAddSuggestionsProps) {
  const filtered = useMemo(() => {
    const q = query.toLowerCase().trim();
    if (q.length === 0) return [];
    const matches: string[] = [];
    for (const s of suggestions) {
      const lower = s.toLowerCase();
      if (!lower.startsWith(q)) continue;
      // Skip exact match where there is nothing to extend.
      if (lower === q) continue;
      matches.push(s);
      if (matches.length >= MAX_CHIPS) break;
    }
    return matches;
  }, [suggestions, query]);

  if (filtered.length === 0) return null;

  return (
    <ul
      role="listbox"
      aria-label="Description suggestions"
      className="flex gap-2 overflow-x-auto"
    >
      {filtered.map((suggestion, idx) => (
        <li key={suggestion} role="option" aria-selected="false">
          <Button
            type="button"
            variant="secondary"
            size="sm"
            // h-10 matches the >=44px-ish touch target of nearby CategoryChips.
            className="h-10 rounded-full px-3 text-sm"
            onClick={() => onPick(suggestion)}
          >
            {suggestion}
            {idx === 0 && (
              <kbd
                aria-hidden="true"
                className="ml-2 hidden rounded border border-border bg-muted px-1 py-0.5 text-xs font-medium md:inline-block"
              >
                Tab
              </kbd>
            )}
          </Button>
        </li>
      ))}
    </ul>
  );
}
