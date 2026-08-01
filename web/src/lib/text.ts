// Text measurement for the fields that carry a stated length limit.

/**
 * Length of `value` in CHARACTERS (Unicode code points).
 *
 * This exists because `String.prototype.length` is the wrong number for this
 * job and looks like the right one. JavaScript counts UTF-16 code units, so a
 * character outside the Basic Multilingual Plane — every emoji, most notably —
 * counts as 2. Go counts code points (`charLen`, `internal/api/limits.go`), so
 * the server counts that same emoji as 1.
 *
 * The trap is that the two agree on almost everything a test is likely to use.
 * Arabic, accented Latin and CJK are all single UTF-16 units, so a `.length`
 * check matches the server exactly until someone types an emoji into a
 * description — and then a value the server accepts is refused here, or the
 * character counter on screen reads one higher than the limit the server
 * applied.
 *
 * Spreading a string iterates it by code point, which is the count Go produces.
 */
export function charCount(value: string): number {
  return [...value].length;
}
