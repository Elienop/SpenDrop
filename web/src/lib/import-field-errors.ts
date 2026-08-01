import type { ImportFieldErrorField, PatchRowRequest } from '@/api/types';

/**
 * Frontend-side facts about import field errors. Note what is NOT here:
 * the length bounds themselves, and any sentence describing one.
 *
 * The server sends a `message` with every field error and reuses the
 * same string for `validateImportField`'s PATCH 400, so there is one
 * owner for the wording and it is not this side. An earlier version of
 * this file mirrored `MaxDescriptionLength` / `MaxTagsLength` /
 * `MaxNotesLength` from internal/api/limits.go in order to compose that
 * copy; that mirror was a standing invitation for the Go constant to
 * move while the UI kept confidently stating the old bound to the user.
 *
 * WHY NOBODY SHOULD ADD A CHARACTER COUNT HERE. It looks like an
 * obvious improvement and it is wrong three times over. The backend
 * compares with Go's `len()` over BYTES while every message calls them
 * characters, and in UTF-8 those are the same thing only for ASCII: a
 * bound of 500 bytes is reached at roughly 250 Arabic characters, or
 * nearer 170 for some accented French — both in daily use in this
 * household. A count computed in JS would be a third quantity again,
 * since JS string length is UTF-16 code units. So the server's bound,
 * the user's characters and anything we could measure here are three
 * different numbers. Naming the bound is true in any script; naming an
 * overage is not. The server's messages follow that rule, which is
 * another reason to render them rather than reword them.
 *
 * (The byte figures above are arithmetic about UTF-8, not a statement
 * of SpenDrop's current limits — deliberately, so this explanation
 * cannot go stale when a bound in internal/api/limits.go moves.)
 */

/**
 * Whether a flagged field can be fixed inside the preview table. This
 * is a property of OUR table, not of the API: `description` is the only
 * over-lengthable column the preview renders as editable, so it is the
 * only field whose error has a cell to hang a message on. `tags` and
 * `notes` are not rendered at all, so their errors are shown at row
 * level with Skip as the fix.
 *
 * Typed as a predicate narrowing to `EditableImportField`, the
 * intersection of "can be reported too long" and "the PATCH endpoint
 * accepts". That is not decoration: a cell error is keyed by a
 * `PatchRowRequest['field']` because committing the cell PATCHes it, so
 * a field that could produce a cell error the PATCH cannot fix would be
 * a dead end for the user. The compiler now refuses that combination
 * instead of leaving it to review.
 *
 * Keep the runtime check in step with `EditableField` in
 * ImportPreviewTable — if a future preview gains a Notes column, this
 * is the switch that moves its errors from the row detail into a cell,
 * and the type widens on its own once PATCH accepts the field.
 */
export type EditableImportField = Extract<
  ImportFieldErrorField,
  PatchRowRequest['field']
>;

export function isEditableInPreview(
  field: ImportFieldErrorField,
): field is EditableImportField {
  return field === 'description';
}

/**
 * Last-resort text for a field error that arrives without a `message`.
 * The server populates it on all four surfaces today, so this should
 * never render; it exists because `message` is optional on the wire and
 * a response that omitted it would otherwise draw an empty red box
 * under a cell.
 *
 * Worded to be OBVIOUSLY a fallback rather than a plausible copy of the
 * real thing. That is the point: an almost-identical second version of
 * the server's sentence would drift invisibly, because nothing renders
 * it and so nothing catches it, whereas "This value is too long" on
 * screen is a visible signal that a response arrived malformed.
 *
 * States no bound, for the same reason. A number here would recreate
 * the mirror this module exists to be rid of, in the one place where
 * nobody would ever see it was wrong.
 */
export function fallbackFieldTooLongMessage(
  field: ImportFieldErrorField,
): string {
  return isEditableInPreview(field)
    ? 'This value is too long for SpenDrop. Shorten it here, or skip this row.'
    : 'This value is too long for SpenDrop. Skip this row, or shorten it in your spreadsheet and upload again.';
}
