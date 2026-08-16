import { describe, test, expect } from 'vitest';
import { ApiError, NetworkError } from '@/api/client';
import {
  isRetryableSaveFailure,
  noClientKeyMessage,
  noRateMessage,
  saveFailureMessage,
} from './save-failure';

describe('saveFailureMessage', () => {
  test('quotes the server when the server answered', () => {
    expect(saveFailureMessage(new ApiError('amount must be positive', 400))).toBe(
      'amount must be positive',
    );
  });

  test('falls back to generic copy when the server answered without words', () => {
    expect(saveFailureMessage(new ApiError('', 500))).toMatch(/failed to save/i);
  });

  test('says the outcome is unknown, and that retrying is safe', () => {
    // The user cannot tell an unanswered request from a rejected one, and the
    // difference decides whether they should go and check the ledger.
    for (const err of [
      new NetworkError('Could not reach the server', 'unreachable'),
      new NetworkError('The server took too long to answer', 'timeout'),
      new TypeError('Failed to fetch'),
    ]) {
      expect(saveFailureMessage(err)).toMatch(/confirm the save/i);
      expect(saveFailureMessage(err)).toMatch(/duplicate/i);
    }
  });
});

describe('isRetryableSaveFailure', () => {
  test('an unanswered request is always retryable', () => {
    expect(
      isRetryableSaveFailure(new NetworkError('offline', 'offline')),
    ).toBe(true);
    expect(isRetryableSaveFailure(new TypeError('Failed to fetch'))).toBe(true);
  });

  test('a server that judged is not retryable, one that broke is', () => {
    // The boundary is exact: 499 is still the server refusing this content,
    // 500 is the server failing to answer for it.
    expect(isRetryableSaveFailure(new ApiError('Unauthorized', 401))).toBe(false);
    expect(isRetryableSaveFailure(new ApiError('bad request', 400))).toBe(false);
    expect(isRetryableSaveFailure(new ApiError('conflict', 499))).toBe(false);
    expect(isRetryableSaveFailure(new ApiError('boom', 500))).toBe(true);
    expect(isRetryableSaveFailure(new ApiError('gateway', 502))).toBe(true);
  });
});

describe('noRateMessage', () => {
  test('names the currency that has no rate', () => {
    expect(noRateMessage('LBP')).toMatch(/LBP/);
    expect(noRateMessage('LBP')).toMatch(/settings/i);
  });
});

describe('noClientKeyMessage', () => {
  test('states the entry was not saved, and names what stopped it', () => {
    // Both halves matter. "Not saved" is the whole point — the user has to be
    // able to tell this from a save that went through. Naming the browser is
    // what keeps them from hunting a network problem that isn't there, the
    // same reason `noRateMessage` names the currency.
    expect(noClientKeyMessage()).toMatch(/not saved/i);
    expect(noClientKeyMessage()).toMatch(/browser/i);
  });

  test('does not borrow the unknown-outcome copy', () => {
    // `saveFailureMessage` says the outcome could not be confirmed and that a
    // Retry is safe. Nothing was sent here, so both sentences would be false:
    // there is no write in flight to confirm, and no Retry to be safe about.
    const msg = noClientKeyMessage();
    expect(msg).not.toMatch(/confirm the save/i);
    expect(msg).not.toMatch(/duplicate/i);
    expect(msg).not.toMatch(/\bretry\b/i);
  });
});
