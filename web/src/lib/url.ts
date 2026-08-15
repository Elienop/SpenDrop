// URL normalization shared by every module that resolves the API base.

/**
 * Return `s` with every trailing '/' removed ('' for an all-slash string).
 *
 * This exists so the API base URL is trimmed by ONE implementation. It was
 * previously three separate copies of `s.replace(/\/+$/, '')` — in
 * `api/client.ts`, `api/import.ts` and `hooks/useLiveUpdates.ts` — which is
 * both a drift hazard (the three must produce identical bases or dev and prod
 * disagree about where `/events` lives) and a super-linear regex
 * (typescript:S8786): `\/+$` retries the greedy run from every start offset,
 * so a string of N slashes followed by any other character costs O(N²). At
 * 100k slashes that is ~3s of blocked main thread.
 *
 * The scan below is O(N): it walks back from the end while the character is a
 * slash and slices once. Output is identical to the regex for every input,
 * including '' and '///'.
 */
export function trimTrailingSlashes(s: string): string {
  let end = s.length;
  while (end > 0 && s[end - 1] === '/') end--;
  return end === s.length ? s : s.slice(0, end);
}
