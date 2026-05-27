import type { Category, Currency } from '@/api/types';

/**
 * Result of parsing a single freeform quick-entry line such as
 * `lunch 12.50 #work` or `taxi 20 EUR`. Maps directly onto the transaction
 * entry form fields (minus the date, which the screen defaults to today):
 * the screen renders these as a confirm card and submits via
 * `toCreatePayload`.
 *
 * Parsing is deliberately conservative — it never *guesses* a category from
 * fuzzy synonyms. `categoryId` is only set when the description contains a
 * category's name as a whole word; otherwise it stays null and the screen
 * requires a one-tap pick (the create API requires a category).
 */
export interface ParsedQuickEntry {
  /** Amount in the typed (`currency`) units; null when no amount was found. */
  amount: number | null;
  /** 3-letter currency code. Defaults to `baseCurrency` when none is typed. */
  currency: string;
  /** Remaining text after amount/currency/tags are removed, trimmed. */
  description: string;
  /** Space-separated tag words (without the leading `#`); '' when none. */
  tags: string;
  /** Matched category id, or null when the description matched no category. */
  categoryId: number | null;
}

export interface QuickParseOptions {
  /** Active categories to match the description against (by name). */
  categories: Category[];
  /** Known currencies, used to recognise currency codes and symbols. */
  currencies: Currency[];
  /** Household base currency code; the default when no currency is typed. */
  baseCurrency: string;
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * Try to read a single whitespace token as a positive money amount,
 * tolerating a leading or trailing currency symbol (`$12`, `12€`) and a
 * comma decimal separator (`12,50`). Returns null when the token is not a
 * clean amount. When a recognised unique symbol is attached, its currency
 * code is returned so the caller can adopt it.
 */
function tryParseAmount(
  token: string,
  symbolToCode: Map<string, string>,
): { value: number; currency?: string } | null {
  let s = token;
  let currency: string | undefined;

  for (const [sym, code] of symbolToCode) {
    if (s.startsWith(sym)) {
      currency = code;
      s = s.slice(sym.length);
      break;
    }
    if (s.endsWith(sym)) {
      currency = code;
      s = s.slice(0, -sym.length);
      break;
    }
  }

  if (!/^\d+(?:[.,]\d{1,2})?$/.test(s)) return null;
  const value = Number.parseFloat(s.replace(',', '.'));
  if (!Number.isFinite(value) || value <= 0) return null;
  return { value, currency };
}

/**
 * Find the most specific active category whose full name appears as a
 * whole word (or word sequence) inside the description. Whole-word matching
 * (not loose substring) avoids false positives like "Food" matching
 * "foodie". When several match, the longest name wins (most specific).
 */
function matchCategory(description: string, categories: Category[]): number | null {
  if (!description) return null;
  const hay = ` ${description.toLowerCase()} `;
  let best: { id: number; len: number } | null = null;
  for (const c of categories) {
    if (c.is_active === false) continue;
    const name = c.name.trim().toLowerCase();
    if (!name) continue;
    const re = new RegExp(`(?:^|\\W)${escapeRegExp(name)}(?:\\W|$)`);
    if (re.test(hay) && (best === null || name.length > best.len)) {
      best = { id: c.id, len: name.length };
    }
  }
  return best ? best.id : null;
}

/**
 * Parse a freeform quick-entry line into transaction fields. Pure and
 * side-effect free. Rules, applied per whitespace token:
 *   - `#word` → a tag.
 *   - a known 3-letter currency code → sets the currency.
 *   - the first numeric token (optionally with a `$`/`€` symbol) → the amount.
 *   - everything else → the description.
 * The first numeric token wins as the amount (predictable for the common
 * one-amount line); the rest stays in the description for the user to adjust.
 */
export function parseQuickEntry(
  raw: string,
  opts: QuickParseOptions,
): ParsedQuickEntry {
  const { categories, currencies, baseCurrency } = opts;

  const codeSet = new Set(currencies.map((c) => c.code.toUpperCase()));

  // Only map symbols that unambiguously identify one currency (many use "$").
  const symbolCounts = new Map<string, number>();
  for (const c of currencies) {
    if (c.symbol) symbolCounts.set(c.symbol, (symbolCounts.get(c.symbol) ?? 0) + 1);
  }
  const symbolToCode = new Map<string, string>();
  for (const c of currencies) {
    if (c.symbol && symbolCounts.get(c.symbol) === 1) {
      symbolToCode.set(c.symbol, c.code.toUpperCase());
    }
  }

  const text = (raw ?? '').trim();
  const empty: ParsedQuickEntry = {
    amount: null,
    currency: baseCurrency,
    description: '',
    tags: '',
    categoryId: null,
  };
  if (!text) return empty;

  const tagWords: string[] = [];
  const descWords: string[] = [];
  let amount: number | null = null;
  let currency = baseCurrency;
  let amountConsumed = false;
  let currencyFromToken = false;

  for (const tok of text.split(/\s+/)) {
    const tagMatch = tok.match(/^#(\w[\w-]*)$/);
    if (tagMatch) {
      tagWords.push(tagMatch[1]);
      continue;
    }

    const up = tok.toUpperCase();
    if (!currencyFromToken && codeSet.has(up)) {
      currency = up;
      currencyFromToken = true;
      continue;
    }

    if (!amountConsumed) {
      const parsed = tryParseAmount(tok, symbolToCode);
      if (parsed) {
        amount = parsed.value;
        if (parsed.currency && !currencyFromToken) currency = parsed.currency;
        amountConsumed = true;
        continue;
      }
    }

    descWords.push(tok);
  }

  const description = descWords.join(' ').trim();
  return {
    amount,
    currency,
    description,
    tags: tagWords.join(' '),
    categoryId: matchCategory(description, categories),
  };
}
