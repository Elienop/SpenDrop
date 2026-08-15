/**
 * The class for TEXT in the attention register — a dirty-edit count, a
 * "fix these before you can continue" status line.
 *
 * TWO SHADES, ONE MEANING, and the split is a contrast requirement rather
 * than taste. `amber-500` (rgb 245,158,11) is the register's colour and
 * reads correctly on the dark surface it was chosen against; on the light
 * theme's white it measures ≈2.1:1 at 14px, well under the 4.5:1 that
 * body-sized text needs. `amber-600` clears it on white, and would be too
 * dark on the dark surface — hence one token that switches, not one
 * colour that compromises.
 *
 * TEXT ONLY. An icon beside the text keeps `text-amber-500` in both
 * themes: it is a non-text element (the 3:1 rule applies, and it clears
 * that), it is never the only signal — the sentence beside it says the
 * same thing — and matching the text's shade would make the two amber
 * shades sit side by side on the light theme.
 *
 * Lifted out of `pages/Budgets.tsx`, where it started, when the import
 * preview's status line needed the same fix. Anything that colours text
 * amber should import this rather than write the pair out; the day a
 * `--warning` semantic token lands, this is the one edit.
 */
export const ATTENTION_TEXT_CLASS = 'text-amber-600 dark:text-amber-500';
