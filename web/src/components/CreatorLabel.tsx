import { User } from 'lucide-react';

export interface CreatorLabelProps {
  /**
   * `Transaction.created_by` — the creator's **display name**. The empty
   * string is the wire's "the creator's user row is gone" value and renders
   * the neutral `Unknown` fallback, never a blank.
   */
  createdBy: string;
  /**
   * `Transaction.created_by_username` — the creator's **login handle**,
   * without the `@`. The empty string means the same orphaned-creator case as
   * an empty `createdBy` and renders no handle at all.
   */
  createdByUsername: string;
}

/**
 * "Entered by <display name> @<handle>" — the one attribution line the ledger
 * renders, shared by every surface that names a transaction's creator.
 *
 * **Why the handle is here at all (B36).** `created_by` is a display name read
 * through a live JOIN, and a member can PATCH their own display name to any
 * string — including the admin's. The relabel then applies retroactively to
 * every row they have ever entered, so the display name alone cannot attribute
 * a row. The username is the login identifier: it is unique, admin-visible in
 * Settings beside the display name, and not self-selectable into a collision.
 * A server-side uniqueness check on display names was rejected as the fix —
 * the error would leak the set of existing display names to a member.
 *
 * **The handle is suppressed, not blanked, when either half is empty.** An `@`
 * with nothing after it is a bug, and `Unknown @somebody` contradicts itself,
 * so both strings must be non-empty before the handle renders. On the wire the
 * two are set by the same LEFT JOIN and empty together, but this component is
 * the place that must not depend on that.
 *
 * The two emptiness checks below are deliberately the SAME test. `name` falls
 * back on truthiness (`createdBy || 'Unknown'`), so `showHandle` uses
 * truthiness too rather than `!== ''`. The pair disagrees only off-type — but
 * off-type is reachable: a producer that omits the field (an un-annotated
 * fixture, a hand-built mock, a stale endpoint) delivers `undefined`, which is
 * `!== ''` and therefore passed the strict version. Measured output of the
 * mismatched pair: `Entered by Elie @undefined`, and `Unknown @elienop` —
 * the second being exactly the "names the person it just said it cannot name"
 * case the gate exists to prevent. Whatever makes `name` fall back must also
 * suppress the handle, in one step, or the two rules drift apart at the edges.
 *
 * **The DISPLAY NAME clips and the handle survives.** This is the whole point
 * of the two-span layout, and it is not the obvious arrangement: with both
 * strings in one truncating span, the tail ellipsis eats `@handle` FIRST and
 * leaves the spoofable half standing alone — the exact failure the handle was
 * added to prevent. `MaxDisplayNameLength` is 64 and the phone card is 360px,
 * so that is not a corner case, it is the common one. Hence:
 *
 *   - the name sits in its own `min-w-0 truncate` span, so it is the flex item
 *     that absorbs the shortfall;
 *   - the handle is `shrink-0`, so it keeps its width while the name gives
 *     ground;
 *   - `max-w-[50%]` stops the reverse starvation — `MaxUsernameLength` is 32,
 *     and without the cap a long handle on a narrow card would leave the name
 *     nothing. The percentage needs a definite basis to resolve against, which
 *     is why the inner container is `flex-1`: a content-sized container would
 *     make the cap cyclic and clip the handle even when there is room for it.
 *
 * **The separator is a rendered space character, not a margin.** Margins do
 * not separate words for a screen reader, so `ml-1` would have this announce
 * "Elie@elienop" — one token, and one that reads as an email address. The
 * space therefore lives in the handle's own text (`{' @' + username}`), and
 * `whitespace-pre` is what keeps it: the handle span is a flex item, so it is
 * block-level, and a block's leading white space is otherwise collapsed away.
 *
 * `overflow-hidden text-ellipsis whitespace-pre` rather than the shorter
 * `truncate whitespace-pre`: `truncate` also sets `white-space: nowrap`, so
 * the pair only works because Tailwind emits the `whitespace` plugin after
 * `textOverflow` (verified against the generated CSS in 3.4.19 — it does win
 * today, and tailwind-merge keeps both). Relying on utility source order to
 * override a property is a silent dependency on Tailwind's internal plugin
 * ordering, and this project has a v4 upgrade in the backlog. The explicit
 * triple sets each property exactly once and cannot flip.
 *
 * **The register is the metadata one** (`text-xs text-muted-foreground`,
 * `gap-1.5`, `size-3.5` icon) and the whole line already sits in it, so the
 * handle inherits rather than restating it. There is no lighter step available
 * that still clears the contrast floor.
 *
 * `items-center` on the inner row, not `items-baseline`: both spans carry the
 * same inherited 12px/1rem metrics, so the two are identical here — but both
 * also have non-visible `overflow`, which means their baselines are synthesized
 * from the bottom margin edge rather than read from the text. Aligning on a
 * synthesized baseline is a subtlety with no payoff; `items-center` matches the
 * wrapping `<p>` so the whole line aligns by one rule.
 *
 * `title` carries the untruncated "Name @handle" for the desktop tables, where
 * this line shares the description cell — the slack column (`w-full max-w-0`),
 * so it clips rather than widening the table. Mirrors the description's own
 * `title` one line up. Dead on touch — which is exactly why the clipping order
 * above matters and the tooltip is a bonus rather than the fix.
 *
 * Shared rather than copied into the six surfaces that render it (the ledger
 * table row, the phone card, the heatmap day sheet, Trash's card and table,
 * and the QuickAdd recents panel) so the suppression and clipping rules cannot
 * hold on five of them and drift on the sixth.
 */
export function CreatorLabel({
  createdBy,
  createdByUsername,
}: CreatorLabelProps) {
  const name = createdBy || 'Unknown';
  const showHandle = Boolean(createdBy) && Boolean(createdByUsername);
  return (
    <p
      className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground"
      title={showHandle ? `${name} @${createdByUsername}` : name}
    >
      <User className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="flex min-w-0 flex-1 items-center">
        <span className="min-w-0 truncate">
          {/* A bare name in a muted line does not announce what it IS, and the
              icon is aria-hidden decoration. */}
          <span className="sr-only">Entered by </span>
          {name}
        </span>
        {showHandle && (
          <span className="max-w-[50%] shrink-0 overflow-hidden text-ellipsis whitespace-pre">
            {` @${createdByUsername}`}
          </span>
        )}
      </span>
    </p>
  );
}
