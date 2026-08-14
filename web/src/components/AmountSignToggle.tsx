import { useId } from 'react';
import { Switch } from '@/components/ui/switch';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';
import { TYPE_EXPENSE, type TransactionType } from '@/lib/transaction-types';
import type { AmountSignKind } from '@/lib/format';

/**
 * The word is the same one `AmountSignNote` puts on the saved row, and that is
 * not a coincidence to be tidied away: the control that creates the state and
 * the label that explains it afterwards have to be one vocabulary, or the
 * household learns two names for the same thing.
 * `AmountSignToggle.test.tsx` pins them equal.
 */
const COPY: Record<AmountSignKind, { label: string; hint: string }> = {
  refund: { label: 'Refund', hint: 'Money that came back to you.' },
  reversal: { label: 'Reversal', hint: 'Income that went back out.' },
};

export interface AmountSignToggleProps {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  /**
   * The kind of entry this sits on — the SELECTED category's type, not the
   * row's stored one, so switching an expense to an income category on an edit
   * surface relabels the control instead of leaving it lying.
   */
  type: TransactionType;
  /** Renders the one-line explanation under the label. */
  showHint?: boolean;
  disabled?: boolean;
  /** Overrides the generated id, for a surface that labels the control
   *  itself. Omit it — the internal `<Label htmlFor>` is already wired. */
  id?: string;
  className?: string;
}

/**
 * The sign channel for every amount the user enters.
 *
 * A refund is a NEGATIVE expense, and the one thing this design refuses to do
 * is read that sign out of the digits: a typed minus stays invalid in every
 * amount input, so a slipped keystroke cannot silently invert a row's meaning.
 * The direction is a separate, deliberate control instead, and it is the only
 * thing that puts a sign on a transaction (`applyAmountSign`).
 *
 * ONE CONTROL, FOUR SURFACES: the desktop entry row, QuickAdd, the inline edit
 * row and the phone edit Sheet. The edit surfaces are not optional — they are
 * how a stored refund survives an unrelated edit, since the amount box shows
 * its magnitude and something has to carry the sign back.
 *
 * `font-sans font-normal` on the root is load-bearing, the same way it is on
 * `AmountSignNote`: two of the four mounts nest this inside a `font-mono`
 * amount block, and without it the words render in tabular figures.
 */
export function AmountSignToggle({
  checked,
  onCheckedChange,
  type,
  showHint = false,
  disabled = false,
  id,
  className,
}: AmountSignToggleProps) {
  const generatedId = useId();
  const controlId = id ?? `${generatedId}-sign`;
  const hintId = `${controlId}-hint`;
  const kind: AmountSignKind = type === TYPE_EXPENSE ? 'refund' : 'reversal';
  const copy = COPY[kind];

  return (
    <div
      className={cn(
        'flex items-center gap-3 font-sans font-normal',
        className,
      )}
    >
      <Switch
        id={controlId}
        checked={checked}
        onCheckedChange={onCheckedChange}
        disabled={disabled}
        // The accessible name, deliberately duplicating the visible label:
        // with `showHint` the <Label> holds two lines, and a name of "Refund
        // Money that came back to you." is not what anyone would say out loud.
        // Same arrangement as Settings' notification switches.
        aria-label={copy.label}
        // The hint is the DEFINITION of the word in the name — "Refund" alone
        // does not say which direction the money went — so it has to reach a
        // screen reader too. As a description rather than part of the name:
        // the name stays the one word, and the sentence follows it.
        aria-describedby={showHint ? hintId : undefined}
      />
      <Label htmlFor={controlId} className="flex flex-col gap-1">
        <span>{copy.label}</span>
        {showHint && (
          <span
            id={hintId}
            className="text-xs font-normal text-muted-foreground"
          >
            {copy.hint}
          </span>
        )}
      </Label>
    </div>
  );
}
