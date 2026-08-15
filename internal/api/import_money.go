package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/elienop/spendrop/internal/database"
)

// importCurrencies is one snapshot of the currencies table, taken once per
// resolution pass (upload, PATCH, GET, and the confirm insert) and matched
// case-insensitively on code.
//
// A snapshot rather than a per-row query for two reasons. A sheet is up to
// MaxImportRows long and every row would otherwise re-read the same handful of
// currency rows on a pool pinned to one connection. And more importantly, a
// pass has to resolve every row against ONE view of the table: an admin
// editing LBP's rate halfway through a confirm would otherwise split the batch
// across two rates, with nothing in the result saying where the seam fell.
//
// The rate a resolved row BOOKS never comes from here — that is the sheet's
// own Rate cell. The snapshot's rate is only ever OFFERED, inside the
// rate_missing message, as the number the user may choose to apply.
type importCurrencies struct {
	// byCode is keyed on the lower-cased, trimmed code; the value is the row
	// as the table holds it, so callers read the CANONICAL code off it.
	byCode map[string]database.Currency
}

// newImportCurrencies builds the snapshot from an already-read currencies
// slice. Split from loadImportCurrencies so the resolver's matrix test can
// state its own three-currency fixture without a database.
func newImportCurrencies(rows []database.Currency) importCurrencies {
	byCode := make(map[string]database.Currency, len(rows))
	for _, c := range rows {
		byCode[importCurrencyKey(c.Code)] = c
	}
	return importCurrencies{byCode: byCode}
}

// loadImportCurrencies reads the currencies table for one resolution pass.
// The queries handle is the caller's choice of pool or transaction: the
// confirm pass passes its qtx so the snapshot is the same view the inserts
// commit against.
func loadImportCurrencies(ctx context.Context, q *database.Queries) (importCurrencies, error) {
	rows, err := q.ListCurrencies(ctx)
	if err != nil {
		return importCurrencies{}, fmt.Errorf("load currencies for import: %w", err)
	}
	return newImportCurrencies(rows), nil
}

// importCurrencyKey normalises a code for lookup. Trim, because a sheet cell
// is whatever the user typed; lower-case, because "lbp" and "LBP" name the
// same currency to a human and the household's table holds exactly one of
// them.
func importCurrencyKey(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

// lookup resolves a sheet's currency cell to the household's row for it.
// ok=false means the household has no such currency — which BLOCKS the row
// rather than falling back to anything, because a currency SpenDrop does not
// know has no rate, no symbol and no place in any report.
//
// The returned row carries the canonical code, and that is the point of
// returning the row instead of a bool: the stored value must be the table's
// spelling, never the sheet's. store.go's foreignMagnitudeUnchanged compares
// the code EXACTLY when it decides whether a later edit may keep the booked
// rate, so a row stored as "lbp" could never freeze against a "LBP" edit.
func (c importCurrencies) lookup(code string) (database.Currency, bool) {
	key := importCurrencyKey(code)
	if key == "" {
		return database.Currency{}, false
	}
	cur, ok := c.byCode[key]
	return cur, ok
}

// importMoney is what one row will STORE, plus the one fact the preview needs
// that storage does not record: whether the base amount was COMPUTED.
//
// It mirrors the write path's fields (resolvedMoney over the API, the same
// four columns on CreateTransactionParams) so the import processor assigns
// them across without re-deciding anything. Derived is preview-only: the
// ledger cannot tell a computed amount from a typed one, and does not need
// to — but a user looking at a preview row does.
type importMoney struct {
	AmountCents         int64
	OriginalAmountCents sql.NullInt64
	OriginalCurrency    sql.NullString // canonical code
	BookedRate          sql.NullFloat64
	Derived             bool // AmountCents came from original ÷ rate
}

// resolveImportMoney applies the design's per-row money matrix to one row and
// returns exactly one of two things: the money the row will store, or the flag
// that blocks it.
//
// It is THE definition of a row's money. All four preview surfaces and the
// confirm insert call this one function, so a row cannot be shown one amount
// and store another, and a row the preview passed cannot be refused at insert
// for a money reason the preview never mentioned.
//
// It divides nothing itself: every conversion goes through convertForeignMoney,
// the single divisor shared with manual entry, so "1,500,000 LBP at 89,000" is
// 1685 cents whichever door it came through.
//
// Three returns, and at most one of the last two is ever set:
//
//	(money, nil, "")            the row is usable and money is what it stores
//	(zero, *fieldError, reason) the row is blocked; the error carries the
//	                            server-authored sentence for every surface,
//	                            and the reason is the LAST-DITCH skip label
//	                            that guards confirm if a stale session
//	                            reaches it anyway
//
// The blocked case returns a zero importMoney deliberately: a partially filled
// one beside a flag is an invitation for some future path to insert it.
//
// Order of the checks is a decision, not an accident:
//
//  1. An unusable rate CELL is judged first, on its own terms — it needs no
//     currency to be wrong, and its remedy is the one cell the preview can
//     edit in place.
//  2. Then the currency, because nothing else about the row's money can be
//     decided without knowing which currency it is: base and foreign go
//     different ways from here, and an unknown code has neither.
//  3. Then the base-currency branch, in parity with resolveCurrency's
//     IsBase branch — the row IS base money, so it stores no original.
//  4. Then the foreign branches, where the rate decides whether the row is a
//     label (#2) or a conversion (#3/#4).
//
// sign_mismatch and zero_amount are NOT decided here. The sign gate runs
// before this function (a contradictory pair is a fault about the row, not
// about its rate), and "no money at all" is the caller's reading of an
// AmountCents of zero — see preCategorySkipReason.
func resolveImportMoney(row importRow, cur importCurrencies) (importMoney, *importFieldError, importSkipReason) {
	blocked := func(field, message string, reason importSkipReason) (importMoney, *importFieldError, importSkipReason) {
		return importMoney{}, &importFieldError{
			RowID:   row.RowID,
			Field:   field,
			Message: message,
		}, reason
	}

	// #9. RawRate is what the cell held; Rate is what parseImportRate made of
	// it. A non-empty raw with no usable rate is the one shape that means
	// "the user typed a rate and it is wrong" — which is a different fix from
	// #5's "there is no rate here at all", and the reason the raw string is
	// carried on the row instead of being thrown away at parse time.
	if strings.TrimSpace(row.RawRate) != "" && !importRateIsUsable(row.Rate) {
		return blocked(importFieldRate, importRateInvalidMessage(), skipReasonRateInvalid)
	}
	hasRate := importRateIsUsable(row.Rate)

	usdCents := dollarsToCents(row.Amount)
	origCents := dollarsToCents(row.OriginalAmount)
	code := strings.TrimSpace(row.OriginalCurrency)

	if code == "" {
		// #10, reached from the side where the rate has no currency naming
		// what it converts. A populated column has to mean something: a rate
		// silently ignored is a row stored at a value the sheet contradicts.
		if hasRate {
			return blocked(importFieldRate, importRateWithoutCurrencyMessage(), skipReasonRateWithoutCurrency)
		}
		// #1, and the long-standing shape where a sheet carries an original
		// amount with no currency beside it. Both store the USD cell as it
		// stands; the second keeps its unlabelled original, exactly as it has
		// always been stored.
		money := importMoney{AmountCents: usdCents}
		if origCents != 0 {
			money.OriginalAmountCents = sql.NullInt64{Int64: origCents, Valid: true}
		}
		return money, nil, ""
	}

	// #6. Blocks whatever else the row carries, and is resolved OUTSIDE this
	// session: adding the currency in Settings and re-fetching the preview
	// clears it without a re-upload, because every surface re-resolves against
	// the table as it stands.
	currency, known := cur.lookup(code)
	if !known {
		return blocked(importFieldOriginalCurrency, importUnknownCurrencyMessage(code), skipReasonUnknownCurrency)
	}

	if currency.IsBase {
		// #8. A rate of exactly 1 on the base currency is a no-op the sheet is
		// welcome to state; any other rate claims a conversion that cannot
		// have happened, and applying it would silently restate the amount.
		if hasRate && row.Rate != 1 {
			return blocked(importFieldRate, importRateOnBaseMessage(currency.Code), skipReasonRateOnBase)
		}
		// #7. Parity with resolveCurrency's IsBase branch: base money stores
		// no original pair and no rate. This is a deliberate change from the
		// old behaviour of storing the label verbatim — a row whose original
		// currency IS the base currency has no foreign side to record, and
		// storing one would make every base row look converted.
		return importMoney{AmountCents: usdCents}, nil, ""
	}

	// #12, the ORIGINAL CELL — judged here, before anything is decided about
	// the rate, because the answer changes what every branch below means.
	//
	// Two shapes, one condition, one message. The upload parser ZEROES an
	// Original Amount it cannot use (`parseImportAmount` refuses anything past
	// MaxTransactionAmount in its own currency, and anything unparseable), so
	// a sheet stating 2,000,000,000 LBP arrives here with origCents == 0 and
	// only RawOriginalAmount to say a figure was ever there. Read without the
	// raw cell, that row looks like "no original at all" and gets diagnosed as
	// rate_without_currency — "nothing to convert", about a sheet that plainly
	// has both halves — or, once the user clears the rate to try to fix it,
	// silently skipped as zero_amount. The second shape is the same fault
	// arriving from a caller that did not go through the parser, and it is
	// checked on the whole foreign branch rather than only before the
	// division, because a LABEL row (#2) would otherwise store an
	// out-of-range original_amount_cents that nothing downstream re-bounds.
	if origCents == 0 {
		if strings.TrimSpace(row.RawOriginalAmount) != "" {
			return blocked(importFieldAmount, importOriginalAmountInvalidMessage(), skipReasonAmountInvalid)
		}
	} else if err := validateMoneyAmount(row.OriginalAmount, "original_amount"); err != nil {
		return blocked(importFieldAmount, importOriginalAmountInvalidMessage(), skipReasonAmountInvalid)
	}

	if origCents == 0 {
		// #10 from the other side: a rate, a currency, and nothing to divide.
		if hasRate {
			return blocked(importFieldRate, importRateWithoutCurrencyMessage(), skipReasonRateWithoutCurrency)
		}
		// A currency named with no foreign money behind it. It COLLAPSES, like
		// #7: the row is base money, and the code alone records nothing a
		// reader could act on. Storing it would create the half-pair shape —
		// original_currency set beside a NULL original_amount_cents — that the
		// app already treats as corruption and strips on the next save, so
		// import must not manufacture rows that will silently change the first
		// time anyone edits them.
		return importMoney{AmountCents: usdCents}, nil, ""
	}

	if !hasRate {
		// #5. No rate was quoted and there is no USD to fall back on, so
		// there is no honest amount to store. NEVER today's rate silently: a
		// booked rate is one-way (freeze-on-edit), so a manufactured one is
		// permanent, and back-dated rows are the whole reason this stage
		// exists. The message OFFERS today's rate; the user applies it.
		if usdCents == 0 {
			return blocked(importFieldRate, importRateMissingMessage(row.OriginalAmount, currency), skipReasonRateMissing)
		}
		// #2. The sheet stated both halves but quoted no rate, so the pair is
		// a LABEL: the USD is what the row is worth and booked_rate stays
		// NULL, because no rate was ever quoted to book.
		return importMoney{
			AmountCents:         usdCents,
			OriginalAmountCents: sql.NullInt64{Int64: origCents, Valid: true},
			OriginalCurrency:    sql.NullString{String: currency.Code, Valid: true},
		}, nil, ""
	}

	// #3, #4 and #12's second half. One divisor for the whole app.
	//
	// The original was bounded above, on its own, rather than left to the
	// converter to report. convertForeignMoney validates the original BEFORE
	// it looks at the rate, so a row bad in both fields comes back with the
	// amount error and the rate fault is invisible — reading a row's fault off
	// which error the helper happened to return is inferring a field from an
	// internal check ORDER. It would also make the message lie in a case the
	// helper handles perfectly well: an out-of-range original divided by a
	// large rate yields a value that IS storable, so a sentence blaming the
	// quotient would be arithmetically false.
	converted, err := convertForeignMoney(row.OriginalAmount, row.Rate)
	if err != nil {
		// Unreachable from here — importRateIsUsable has already refused
		// every rate convertForeignMoney refuses — but reported as the rate
		// fault it is rather than folded into the amount family, so the two
		// can never diverge silently if either predicate is widened. errors.Is
		// is the only safe discriminator: the sentinel's TEXT is not ours and
		// has already been reworded once.
		if errors.Is(err, errRateInvalid) {
			return blocked(importFieldRate, importRateInvalidMessage(), skipReasonRateInvalid)
		}
		// What is left is the CONVERTED value falling outside what a row may
		// hold — a small rate multiplies an in-range original out of range, a
		// large one rounds it away to nothing — because the original was
		// bounded a few lines above. That is what lets this message state the
		// division as the thing that failed.
		return blocked(importFieldAmount, importAmountInvalidMessage(row.OriginalAmount, row.Rate), skipReasonAmountInvalid)
	}
	derivedCents := dollarsToCents(converted)

	// #4. Compared on signed CENTS, which is the quantity that will be
	// stored — comparing dollars would fail on floats that agree to the cent.
	// A zero USD cell is absence, not a disagreement: that row is #3.
	if usdCents != 0 && usdCents != derivedCents {
		return blocked(importFieldAmount,
			importAmountDisagreesMessage(row.Amount, row.OriginalAmount, row.Rate, converted),
			skipReasonAmountDisagrees)
	}

	return importMoney{
		AmountCents:         derivedCents,
		OriginalAmountCents: sql.NullInt64{Int64: origCents, Valid: true},
		OriginalCurrency:    sql.NullString{String: currency.Code, Valid: true},
		// The rate the SHEET quoted, not the table's. That is the whole point
		// of the column: a back-dated row is worth what it was worth on the
		// day, and re-reading the currency row would book today's number
		// against yesterday's money.
		BookedRate: sql.NullFloat64{Float64: row.Rate, Valid: true},
		Derived:    true,
	}, nil, ""
}

// importRateIsUsable reports whether a rate can divide. It is the same class
// convertForeignMoney refuses (errRateInvalid) stated as a predicate, so the
// resolver can tell an ABSENT rate from an unusable one before it ever calls
// the converter.
func importRateIsUsable(rate float64) bool {
	return rate > 0 && !math.IsInf(rate, 0) && !math.IsNaN(rate)
}

// parseImportRate converts a Rate cell into a divisor.
//
// The empty string is the load-bearing case, and it is NOT an error: it means
// the sheet quoted no rate for this row. Every spreadsheet that exists today
// has no Rate column at all, so "absent" has to travel as a value the resolver
// can read (0, no error) rather than a fault — otherwise adding the column
// would flag every legacy row.
//
// Everything else that is not a finite positive number IS an error, and the
// caller keeps the raw cell so it can tell the two apart: rate_missing (#5)
// and rate_invalid (#9) carry different fixes.
//
// Formatting tolerance matches the amount cells (stripCurrencyFormat), so
// "89,000" parses — but note that accounting parentheses make a rate NEGATIVE
// and therefore invalid, which is correct: a negative rate would flip a
// purchase into a refund.
func parseImportRate(s string) (float64, error) {
	cleaned := stripCurrencyFormat(s)
	if cleaned == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, fmt.Errorf("parse rate %q: %w", s, err)
	}
	if !importRateIsUsable(parsed) {
		return 0, fmt.Errorf("rate must be a finite positive number: %q", s)
	}
	return parsed, nil
}

// --- messages -------------------------------------------------------------
//
// One string per condition, composed HERE and rendered verbatim by every
// surface — the preview's field_errors on upload, PATCH and GET, the PATCH
// 400 for a rate the user just typed, and confirm's 409. Same rationale as
// importFieldLengthMessage: a condition reachable four ways must not read two
// different ways depending on how the user got there, and a sentence composed
// on the client would match only by coincidence and only until either copy was
// edited.
//
// Each one names the remedy the preview can actually honour. The rate cell is
// editable, so its messages say "enter" and "clear"; a currency is not, so #6
// points at Settings; an original amount is not, so #12 points at the sheet.

// importRateInvalidMessage explains a Rate cell that cannot divide (#9). It
// takes no arguments on purpose: the same sentence answers the PATCH 400 for
// a rate the user typed a moment ago, where echoing their input back would add
// nothing they cannot see in the cell.
//
// It says positive AND finite because both halves are reachable from a
// spreadsheet: 0 and -5 are the obvious ones, and a cell reading 1e999 parses
// to +Inf, which is positive — "not a positive number" would be a false
// statement about a real cell. An infinite rate makes every amount round to
// zero, so it has to report as a rate fault rather than sending the user to
// fix a figure that is fine.
//
// It is deliberately OUR sentence rather than errRateInvalid's text. That
// sentinel belongs to the shared converter, its wording has already changed
// once, and a user-facing string that tracks another package's error text
// changes whenever that package is edited.
func importRateInvalidMessage() string {
	return "That rate is not a positive, finite number. Enter the rate this row was booked at, or clear the cell."
}

// importRateMissingMessage explains a foreign original with no rate (#5) and
// OFFERS the household's current rate as the value to apply.
//
// The offer is dropped when the currency's own rate cannot divide — the table
// permits a rate of 0, and "apply today's 0" would send the user to apply a
// divisor the converter refuses. The sentence still names what is missing,
// which is the part they can act on.
func importRateMissingMessage(original float64, currency database.Currency) string {
	if !importRateIsUsable(currency.RateToBase) {
		return fmt.Sprintf("No rate for %s %s — enter the rate this row was booked at.",
			formatImportQuantity(original), currency.Code)
	}
	return fmt.Sprintf("No rate for %s %s — enter the rate this row was booked at, or apply today's %s.",
		formatImportQuantity(original), currency.Code, formatImportQuantity(currency.RateToBase))
}

// importUnknownCurrencyMessage explains a code the household has not set up
// (#6). It names the code as the sheet spelled it, because that is the string
// the user has to find in their file — bounded and stripped of control
// characters by importCurrencyLabel, since the cell is untrusted input of
// unbounded length.
func importUnknownCurrencyMessage(code string) string {
	return fmt.Sprintf("%s isn't set up — add it under Settings → Currencies.", importCurrencyLabel(code))
}

// importRateOnBaseMessage explains a rate quoted against the base currency
// (#8), and names both ways out: the rate is wrong, or the currency is.
func importRateOnBaseMessage(code string) string {
	return fmt.Sprintf("%s is the base currency, so a rate does nothing here. Clear the rate, or name the currency this row was really in.", code)
}

// importRateWithoutCurrencyMessage explains a rate with nothing to convert
// (#10). One sentence covers both shapes it arrives in — a rate with no
// original amount, and a rate with no currency — because the remedy is the
// same either way: complete the pair, or clear the rate.
func importRateWithoutCurrencyMessage() string {
	return "This row has a rate but nothing to convert — a rate needs both an original amount and an original currency. Add them, or clear the rate."
}

// importAmountDisagreesMessage explains a sheet whose own USD cell contradicts
// its rate (#4). It shows the arithmetic rather than asserting a mismatch,
// because the user has three cells in front of them and only the numbers say
// which one is wrong.
func importAmountDisagreesMessage(usd, original, rate, derived float64) string {
	return fmt.Sprintf("%s ≠ %s ÷ %s = %s. Fix the amount, the original or the rate — SpenDrop stores what the rate produces.",
		formatImportDollars(usd), formatImportQuantity(original), formatImportQuantity(rate), formatImportDollars(derived))
}

// importOriginalAmountInvalidMessage explains an ORIGINAL amount that is not a
// storable figure in the first place (#12's first half) — before any rate is
// applied to it, and therefore without mentioning one. Sharing
// importAmountInvalidMessage's sentence would state a division that either did
// not happen or did not fail.
//
// It takes no arguments, and that is forced rather than chosen: the reachable
// shape of this fault is a cell the parser already ZEROED, so there is no
// figure left to quote. Quoting the raw cell instead would put an unbounded,
// unsanitised sheet value into a sentence rendered on four surfaces, to say
// something the user can already see in the cell the flag points at.
//
// It names the same band as the converted-amount message, because it is the
// same band — every money figure on a row, foreign or base — and says "in its
// own currency" because that is the trap: 2,000,000,000 LBP is about $22,000,
// which sounds storable until you notice the bound applies to the figure as
// written.
func importOriginalAmountInvalidMessage() string {
	return fmt.Sprintf("That original amount is not a figure SpenDrop can store — it has to be at least one cent and no more than %s in its own currency. Fix the original amount.",
		formatImportQuantity(MaxTransactionAmount))
}

// importAmountInvalidMessage explains a CONVERSION that lands outside what a
// row may hold (#12's second half) — in either direction, since a small rate
// multiplies an in-range original out of range and a large one rounds it away
// to nothing. One sentence states the whole legal band rather than guessing
// which end was hit.
//
// It can state the division as the thing that failed because the original was
// bounded before the division ran; see resolveImportMoney.
func importAmountInvalidMessage(original, rate float64) string {
	return fmt.Sprintf("%s ÷ %s is not an amount SpenDrop can store — it has to be at least one cent and no more than %s. Fix the original amount or the rate.",
		formatImportQuantity(original), formatImportQuantity(rate), formatImportQuantity(MaxTransactionAmount))
}

// importAmountOutOfRangeMessage explains a base amount that parses but is more
// than a row may hold. It states the bound in both directions because the cap
// is on MAGNITUDE — a refund of -2,000,000,000 is refused by the same gate —
// and because a message that named only the ceiling would read as though the
// sign were the problem.
func importAmountOutOfRangeMessage() string {
	return fmt.Sprintf("That amount is outside what SpenDrop can store — a row may not exceed %s in either direction.",
		formatImportQuantity(MaxTransactionAmount))
}

// importCurrencyLabel renders an unresolved currency cell for a message.
//
// The cell is untrusted and unbounded — nothing caps it, because an unknown
// currency has never been stored before now — so a pathological sheet could
// otherwise put a megabyte of text into one flag and repeat it per row.
// Control characters are dropped rather than blanked: a currency code has no
// legitimate use for them, and they would corrupt the line the message is
// rendered on.
func importCurrencyLabel(code string) string {
	code = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(code))

	const maxRunes = 12 // real codes are three; this is room for a typo
	if utf8.RuneCountInString(code) > maxRunes {
		return string([]rune(code)[:maxRunes]) + "…"
	}
	return code
}

// formatImportDollars renders a base-currency amount for a message: always two
// decimals, because that is how the ledger holds it and how every other
// surface shows it.
func formatImportDollars(f float64) string {
	return groupImportThousands(strconv.FormatFloat(f, 'f', 2, 64))
}

// formatImportQuantity renders an original amount or a rate for a message.
//
// Unlike dollars these are not cent-denominated: LBP runs to whole millions
// and a EUR rate is 0.92, so trailing zeros are noise at one end and precision
// is meaningful at the other. Decimals are capped at six — beyond that a rate
// is stating precision no household quoted, and the number stops being
// readable.
func formatImportQuantity(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if dot := strings.IndexByte(s, '.'); dot >= 0 && len(s)-dot-1 > 6 {
		s = strings.TrimRight(strconv.FormatFloat(f, 'f', 6, 64), "0")
		s = strings.TrimSuffix(s, ".")
	}
	return groupImportThousands(s)
}

// groupImportThousands inserts thousands separators into the integer part of
// an already-formatted decimal string. Written here rather than pulled from a
// locale package because these strings are English-only server copy, and the
// household's numbers are grouped in threes.
func groupImportThousands(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	intPart, frac := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, frac = s[:dot], s[dot:]
	}

	var b strings.Builder
	for i := 0; i < len(intPart); i++ {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(intPart[i])
	}
	return sign + b.String() + frac
}
