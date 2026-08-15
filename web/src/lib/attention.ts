/**
 * The class for TEXT in the attention register — a dirty-edit count, a
 * "fix these before you can continue" status line, an over-budget note.
 *
 * TWO SHADES, ONE MEANING, and the pair is arithmetic rather than taste.
 * Measured against the surfaces these strings actually sit on — the light
 * `--card` / `--background`, both pure white, and the dark `--card`
 * (`240 4% 9%`):
 *
 *   | shade    | on white | on the dark card |
 *   |----------|----------|------------------|
 *   | amber-500 |  2.15:1 |            8.41:1 |
 *   | amber-600 |  3.19:1 |                 — |
 *   | amber-700 |  5.02:1 |            3.60:1 |
 *
 * Body-sized text needs 4.5:1 (WCAG AA, under 18.66px or under 14px
 * bold), so amber-700 is the light-theme shade and amber-500 is the dark
 * one. Neither works in both directions: amber-500 on white is barely
 * more than a tint of the page, and amber-700 on the dark card lands at
 * 3.60:1 — which is why this is a pair and not a single "safe" shade.
 *
 * amber-600 was here first and was wrong: at 3.19:1 it clears the 3:1 that
 * NON-TEXT elements are held to, not the 4.5:1 its own comment claimed.
 * Numbers in this file are computed, not estimated — recompute them if a
 * surface token moves.
 *
 * TEXT ONLY. An icon beside the text keeps `text-amber-500` in both
 * themes: it is a non-text element, so 3:1 is its bar and 2.15:1 is a
 * miss — but it is never the only signal (the sentence beside it says the
 * same thing), which is the exception the guideline itself carves out for
 * decorative graphics. It also stays the register's own colour, so the
 * icon does not shift shade between themes while the text does.
 *
 * Anything that colours text amber should import this rather than write
 * the pair out; the day a `--warning` semantic token lands, this is the
 * one edit.
 */
export const ATTENTION_TEXT_CLASS = 'text-amber-700 dark:text-amber-500';
