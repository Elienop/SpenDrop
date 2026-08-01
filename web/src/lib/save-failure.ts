import { ApiError } from '@/api/client';

/**
 * Shared reading of a failed single-transaction save, so the phone (`QuickAdd`)
 * and the desktop entry row cannot drift into telling the user two different
 * stories about the same failure.
 *
 * The distinction that matters is not "did it work" but "do we KNOW whether it
 * worked". An `ApiError` means the server answered and refused, so nothing was
 * written. Anything else means the request went out and no answer came back —
 * the write may well have committed, and only the idempotency key makes trying
 * again safe (see `newClientKey`).
 */
export function saveFailureMessage(err: unknown): string {
  if (err instanceof ApiError) {
    return err.message || 'Failed to save transaction';
  }
  // Says the load-bearing part out loud: the entry may already be saved, and
  // pressing Retry cannot end up with two of them. Without that sentence the
  // honest user response to an unknown outcome is to check the ledger first.
  return 'Couldn’t confirm the save — Retry won’t create a duplicate.';
}

/**
 * Whether re-sending the same submission could plausibly change the outcome.
 *
 * An unanswered request always qualifies: the point of the key is that trying
 * again is free. A server that answered has already made its decision — a 401
 * is fixed by signing in, and a 400 re-sent byte-identically is refused
 * byte-identically, so offering Retry there only teaches the user that the
 * button does nothing. 5xx is the exception: the server broke rather than
 * judged, and the same request may well succeed in a moment.
 */
export function isRetryableSaveFailure(err: unknown): boolean {
  return !(err instanceof ApiError) || err.status >= 500;
}

/** Copy for a payload that could not even be built (no configured rate). */
export function noRateMessage(currency: string): string {
  return `No exchange rate for ${currency} — set one in Settings.`;
}
