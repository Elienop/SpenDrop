package api

import (
	"context"
	"fmt"
	"sort"
)

// importCurrencySummary is one row of the household's currencies table as the
// preview reports it: the rate SpenDrop would apply TODAY.
//
// It rides on the preview rather than being read from the currencies endpoint
// because the number the user is OFFERED and the number the import RECORDS
// have to be the same one. "Apply today's 89,000" turns into a PATCH carrying
// that literal value, which is then stored as the row's booked_rate; reading
// the rate from a second source would let the two disagree for exactly as long
// as one cache was staler than the other.
type importCurrencySummary struct {
	Code       string  `json:"code"`
	RateToBase float64 `json:"rate_to_base"`
	IsBase     bool    `json:"is_base"`
}

// summaries renders the snapshot for the wire, sorted by code so the response
// is deterministic — three surfaces return this array for the same session and
// they are compared byte for byte.
func (c importCurrencies) summaries() []importCurrencySummary {
	out := make([]importCurrencySummary, 0, len(c.byCode))
	for _, cur := range c.byCode {
		out = append(out, importCurrencySummary{
			Code:       cur.Code,
			RateToBase: cur.RateToBase,
			IsBase:     cur.IsBase,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// importPreviewRow is a session row as the preview reports it.
//
// `amount` on the wire is the value the row WILL STORE — the derived one for a
// row whose USD came from its rate — while the session entry keeps the sheet's
// own USD cell, so a row whose two halves disagree can still be told what it
// said. AmountDerived is how a reader tells the two apart without re-deriving
// anything: a derived amount is not a number the user typed, and the table
// shows the original and the rate it came from beneath it.
type importPreviewRow struct {
	importRow
	AmountDerived bool `json:"amount_derived,omitempty"`
}

// importPreview is THE preview snapshot. Every surface that shows a session
// returns this exact shape: the upload response, every PATCH response, and the
// GET resume — and the confirm gate re-derives its field_errors and collision
// groups from the same functions.
//
// One builder, because these three handlers used to build the same map by hand
// and a map built three times is a map that differs three ways. The PATCH copy
// omitted import_id, row_count, columns and unique_categories; the frontend
// spreads the response into state, so import_id landed as undefined and every
// edit after the first went to /import/undefined/rows/N and 404'd — silently,
// including un-checking the Skip box. A struct cannot lose a field that way.
//
// Every slice is emitted non-nil. `null` and `[]` decode differently on the
// client, and a preview that reported `null` collision groups would be read as
// "unknown" rather than "none".
type importPreview struct {
	ImportID             string                  `json:"import_id"`
	RowCount             int                     `json:"row_count"`
	Rows                 []importPreviewRow      `json:"rows"`
	Columns              []string                `json:"columns"`
	UniqueCategories     []string                `json:"unique_categories"`
	CollisionGroups      []collisionGroup        `json:"collision_groups"`
	FieldErrors          []importFieldError      `json:"field_errors"`
	UnresolvedCategories []unresolvedCategory    `json:"unresolved_categories"`
	Currencies           []importCurrencySummary `json:"currencies"`
}

// buildImportPreview computes the whole snapshot for one session.
//
// The caller MUST already hold entry.mu: every field here is a function of
// entry.Rows, and a PATCH from another tab mutating the slice midway would
// produce a response whose rows and collision groups describe two different
// sessions. handleImportPatchRow and handleImportGetSession hold it across
// their whole handler; handleImportUpload takes it around this call.
//
// Category lookups are passed in rather than loaded here because confirm's own
// gate needs them for a different question (the user's chosen map), and one
// ListAllCategories per request is the existing shape.
//
// The currencies snapshot is loaded HERE, once per call, which is what makes
// the unknown-currency flag self-healing: a currency added in Settings clears
// the flag on the next GET, with no re-upload, because the preview is a
// function of the table as it stands rather than of what the table said at
// upload time.
func (h *Handler) buildImportPreview(
	ctx context.Context,
	importID string,
	entry *importEntry,
	catNameToID map[string]int64,
	catIDToName map[int64]string,
) (importPreview, error) {
	currencies, err := loadImportCurrencies(ctx, h.queries)
	if err != nil {
		return importPreview{}, fmt.Errorf("build import preview: %w", err)
	}

	// field_errors carries two families in one array: the length errors, then
	// the money errors. Both are recomputed from the CURRENT rows on every
	// surface, so a row edited back inside the limit — or a currency added in
	// Settings — drops out of the array with no client-side bookkeeping.
	fieldErrors := checkImportRowLengths(entry.Rows)

	rows := make([]importPreviewRow, 0, len(entry.Rows))
	for _, row := range entry.Rows {
		money, moneyErr, _ := resolveImportMoney(row, currencies)
		previewRow := importPreviewRow{importRow: row}
		switch {
		case moneyErr != nil:
			// Skipped rows are exempt, exactly as they are from the length
			// gate: skipping IS the remedy the flag offers, so a skipped row
			// must not go on blocking the confirm it was skipped to unblock.
			// The row still shows the sheet's own amount — a blocked row has
			// no resolved value to show, and showing a derived one would erase
			// the disagreement the user has to resolve.
			if !row.Skip {
				fieldErrors = append(fieldErrors, *moneyErr)
			}
		case money.Derived:
			previewRow.Amount = centsToDollars(money.AmountCents)
			previewRow.AmountDerived = true
		}
		rows = append(rows, previewRow)
	}

	// Stable by row_id: the table renders top to bottom and the scroll-to-
	// first-blocker behaviour follows this order. Within a row the order is
	// the order the checks ran — the three length fields, then the one money
	// condition — which a stable sort preserves rather than re-alphabetising.
	sort.SliceStable(fieldErrors, func(i, j int) bool {
		return fieldErrors[i].RowID < fieldErrors[j].RowID
	})

	// nil/0 for the category choices, on every preview surface: at preview
	// time the user has decided nothing, so this is the full list of what
	// WOULD need deciding. The frontend marks entries resolved against its own
	// local choices, which is how its gate keeps the same shape as confirm's
	// without reimplementing which rows are eligible.
	groups, err := buildCollisionGroups(ctx, h.queries, entry.Rows, nil, 0, catNameToID, catIDToName, currencies)
	if err != nil {
		return importPreview{}, fmt.Errorf("build import preview: %w", err)
	}

	return importPreview{
		ImportID:             importID,
		RowCount:             len(entry.Rows),
		Rows:                 rows,
		Columns:              entry.Columns,
		UniqueCategories:     uniqueCategoriesFromRows(entry.Rows),
		CollisionGroups:      groups,
		FieldErrors:          fieldErrors,
		UnresolvedCategories: unresolvedImportCategories(entry.Rows, nil, catNameToID, 0, currencies),
		Currencies:           currencies.summaries(),
	}, nil
}

// importMoneyFieldErrors reports every money condition blocking a session,
// for the confirm gate.
//
// It is the money half of what buildImportPreview computes, split out because
// confirm needs the answer without the rest of a preview it is not going to
// return — and because the two must be the same answer: a row the preview
// flagged and confirm did not would be a 409 with an empty list, and the
// reverse would be a refusal the user never saw coming.
//
// Skipped rows are exempt, for the same reason they are exempt from the
// preview and from the length gate.
func importMoneyFieldErrors(rows []importRow, cur importCurrencies) []importFieldError {
	fieldErrors := []importFieldError{}
	for _, row := range rows {
		if row.Skip {
			continue
		}
		if _, moneyErr, _ := resolveImportMoney(row, cur); moneyErr != nil {
			fieldErrors = append(fieldErrors, *moneyErr)
		}
	}
	return fieldErrors
}
