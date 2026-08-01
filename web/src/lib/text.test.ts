import { describe, it, expect } from 'vitest';
import { charCount } from './text';
import {
  MAX_API_TOKEN_NAME_LENGTH,
  MAX_CURRENCY_SYMBOL_LENGTH,
  MAX_DESCRIPTION_LENGTH,
} from './constants';

// 2 bytes in UTF-8, 1 character, 1 UTF-16 code unit.
const ARABIC = 'ب';
// 4 bytes in UTF-8, 1 character, 2 UTF-16 code units.
const ASTRAL_EMOJI = '🧾';

describe('charCount', () => {
  it('counts Arabic the same way String.length does', () => {
    // Not a redundant case: this is the half where the naive check LOOKS
    // correct. Every non-emoji script the household writes lands here, which
    // is exactly why a `.length` bug survives testing.
    const value = ARABIC.repeat(300);
    expect(charCount(value)).toBe(300);
    expect(value.length).toBe(300);
  });

  it('counts an astral-plane emoji as one character where String.length says two', () => {
    // This is the whole reason the helper exists. Go's charLen
    // (internal/api/limits.go) counts code points, so the server sees 1 here.
    // A `.length` check would refuse 250 emoji against a 500-character limit.
    expect(charCount(ASTRAL_EMOJI)).toBe(1);
    expect(ASTRAL_EMOJI.length).toBe(2);

    const value = ASTRAL_EMOJI.repeat(MAX_DESCRIPTION_LENGTH);
    expect(charCount(value)).toBe(MAX_DESCRIPTION_LENGTH);
    expect(value.length).toBe(MAX_DESCRIPTION_LENGTH * 2);
  });

  it('counts a combining sequence the way Go does, not the way a reader does', () => {
    // "é" written as e + U+0301 is two code points, and both languages agree
    // on two. Pinned so a future switch to Intl.Segmenter — which would count
    // one grapheme — is a deliberate change rather than a silent divergence
    // from the server.
    expect(charCount('é')).toBe(2);
  });

  it('handles the empty string', () => {
    expect(charCount('')).toBe(0);
  });
});

// The limits below must match `internal/api/limits.go`. There is no build-time
// link between the two files, so these assertions are the link: if the backend
// constant moves and this one does not, a value the server accepts is refused
// in the browser (or vice versa, and the user meets a 400 instead of a field
// error).
describe('text length constants', () => {
  it('matches the backend caps', () => {
    expect(MAX_DESCRIPTION_LENGTH).toBe(500);
    expect(MAX_API_TOKEN_NAME_LENGTH).toBe(100);
    expect(MAX_CURRENCY_SYMBOL_LENGTH).toBe(10);
  });
});
