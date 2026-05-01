import type { BulkUpdatePatch, BulkUpdateTagsMode } from '../api/types';

/**
 * Form values for the BulkEditDialog. Mirrors the zod schema in §4.2 of the
 * spec. `setDate` gates the date field (per §3.2 — native <input type="date">
 * has no placeholder, so we use a leading checkbox for "no change"). The
 * `'noChange'` sentinel on category_id is the Lidarr-derived idiom for
 * "don't touch this field on submit". `tagsMode` defaults to `'add'` (the
 * least-destructive operation per §3.3).
 */
export interface BulkEditFormValues {
  setDate: boolean;
  date: string;
  description: string;
  category_id: number | 'noChange';
  tags: string;
  tagsMode: BulkUpdateTagsMode;
}

export interface ComputedPatchResult {
  patch: BulkUpdatePatch;
  tagsMode?: BulkUpdateTagsMode;
}

/**
 * Pure helper: walk RHF form values and emit the wire-shape patch.
 *
 * Per §4.3 step 2:
 * - date: included iff `setDate` is true AND `date` is non-empty
 * - description: included iff trimmed value is non-empty
 * - category_id: included iff value !== 'noChange'
 * - tags + tagsMode: included iff trimmed `tags` is non-empty (the radio
 *   alone is a no-op — see footgun guard in §3.2)
 *
 * The `'noChange'` string sentinel is client-side-only and never reaches
 * the wire (§4.2).
 */
export function computePatch(values: BulkEditFormValues): ComputedPatchResult {
  const patch: BulkUpdatePatch = {};
  if (values.setDate && values.date) {
    patch.date = values.date;
  }
  const desc = values.description.trim();
  if (desc) {
    patch.description = desc;
  }
  if (values.category_id !== 'noChange') {
    patch.category_id = values.category_id;
  }
  const tags = values.tags.trim();
  if (tags) {
    patch.tags = tags;
    return { patch, tagsMode: values.tagsMode };
  }
  return { patch };
}

export function isPatchEmpty(p: ComputedPatchResult): boolean {
  return Object.keys(p.patch).length === 0;
}
