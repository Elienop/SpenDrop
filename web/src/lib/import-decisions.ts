import { STORAGE_KEYS } from '@/lib/storage-keys';

/**
 * The category decisions an admin has made about an import session: which
 * spreadsheet name goes to which category, and the category for rows whose
 * Category cell is empty.
 *
 * WHY THESE ARE PERSISTED AT ALL. The import card unmounts whenever the
 * Settings section changes — `TabsContent` mounts only the active panel — and
 * the preview itself already survives that, because the session id is in
 * localStorage and the card resumes from it. The decisions did not: they were
 * `useState` in the card, so a trip to Settings → Currencies (the remedy the
 * unknown-currency flag POINTS AT) came back to a preview with every manual
 * mapping gone and only the automatic name matches restored. The user is sent
 * away by our own link, so losing their work on the way is not an edge case.
 *
 * Keyed by import_id inside ONE record rather than one key per session: a
 * per-session key would accumulate in localStorage for every upload ever made,
 * and there is only ever one session. A record whose id does not match the
 * session being resumed is ignored — which is what stops a previous upload's
 * mapping from being applied to a new file that happens to share a name.
 *
 * Every access is wrapped: localStorage throws in private modes and when the
 * quota is full, and losing a mapping is not a reason to break the import.
 */
export interface ImportDecisions {
  /** Spreadsheet category name → category id, as strings (Select values). */
  categoryMap: Record<string, string>;
  /** Category for rows with an empty Category cell; null if unchosen. */
  defaultCategoryId: number | null;
}

interface StoredImportDecisions extends ImportDecisions {
  import_id: string;
}

export function saveImportDecisions(
  importID: string,
  decisions: ImportDecisions,
): void {
  const payload: StoredImportDecisions = { import_id: importID, ...decisions };
  try {
    localStorage.setItem(STORAGE_KEYS.importDecisions, JSON.stringify(payload));
  } catch {
    /* private mode / quota — the decisions stay in component state */
  }
}

/**
 * The decisions stored for THIS session, or null.
 *
 * Validated field by field rather than cast: this is data from the browser's
 * own storage, which a user can edit and a previous version of this app may
 * have written in another shape. A cast would hand `undefined` to a `<Select>`
 * value or a string to `defaultCategoryId`, and the failure would land far
 * from here.
 */
export function loadImportDecisions(importID: string): ImportDecisions | null {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(STORAGE_KEYS.importDecisions);
  } catch {
    return null;
  }
  if (!raw) return null;

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null) return null;

  const record = parsed as Record<string, unknown>;
  if (record.import_id !== importID) return null;

  const categoryMap: Record<string, string> = {};
  const storedMap = record.categoryMap;
  if (typeof storedMap === 'object' && storedMap !== null) {
    for (const [name, id] of Object.entries(
      storedMap as Record<string, unknown>,
    )) {
      if (typeof id === 'string' && id !== '') categoryMap[name] = id;
    }
  }

  const storedDefault = record.defaultCategoryId;
  const defaultCategoryId =
    typeof storedDefault === 'number' && Number.isFinite(storedDefault)
      ? storedDefault
      : null;

  return { categoryMap, defaultCategoryId };
}

export function clearImportDecisions(): void {
  try {
    localStorage.removeItem(STORAGE_KEYS.importDecisions);
  } catch {
    /* ignore */
  }
}
