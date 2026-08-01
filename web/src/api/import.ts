import { api } from './client';
import type {
  CollisionGroup,
  ImportFieldError,
  ImportPreview,
  ImportResult,
  PatchRowRequest,
  PatchRowResponse,
} from './types';

/**
 * Resolves the API base URL from Vite env config, matching `ApiClient`'s
 * logic in `client.ts`. Extracted so the two `fetch`-direct endpoints
 * below (`getImportSession`, `confirmImport`) share exactly one
 * resolver — this prevents dev-vs-prod URL drift between them and the
 * rest of the ApiClient callsites. If `client.ts` ever starts doing
 * additional normalization (trailing-slash handling, CORS fallbacks,
 * version prefixes), update this helper to match rather than bypassing.
 */
function apiBaseURL(): string {
  const base = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? '/api';
  return base.replace(/\/+$/, '');
}

/**
 * Thrown when `POST /api/import/confirm` returns 409 UNRESOLVED_COLLISIONS.
 * Carries the full `collision_groups` payload from the server so the
 * caller (useImportSession hook) can update its local collision state
 * without a second round-trip.
 *
 * We subclass Error rather than returning a `Result<T, E>` because React
 * hook state flow already threads through `try/catch` on every async
 * call — a typed error keeps the happy-path signature clean.
 */
export class UnresolvedCollisionsError extends Error {
  readonly collision_groups: CollisionGroup[];

  constructor(collision_groups: CollisionGroup[]) {
    super('Import has unresolved collisions');
    // Restore prototype chain in ES5-transpiled output (Vite targets
    // ES2020 by default so this is defensive, but costs nothing).
    Object.setPrototypeOf(this, UnresolvedCollisionsError.prototype);
    this.name = 'UnresolvedCollisionsError';
    this.collision_groups = collision_groups;
  }
}

/**
 * Thrown when `GET /api/import/{importID}` returns 404 — i.e. the
 * server-side session has expired (60-minute idle TTL) or never
 * existed. The hook's mount effect uses `err instanceof NotFoundError`
 * to silently drop the stale localStorage key without surfacing an
 * error banner (an expired session is a normal user journey after
 * a coffee break, not a failure state).
 *
 * Typed separately from `UnresolvedCollisionsError` because the two
 * have different semantics and the caller wants to distinguish them
 * with a single `instanceof` check. Using plain `Error` with a magic
 * string match (`err.message.includes('not found')`) is fragile: it
 * couples the UI's silence logic to an exact backend error message,
 * and any backend change (or localization) would quietly turn the
 * expected-404 path into a visible error banner.
 */
/**
 * Thrown when `POST /api/import/confirm` returns 409 FIELD_TOO_LONG —
 * at least one row the user asked to import carries a field longer
 * than SpenDrop stores. Carries the server's full `field_errors` array
 * so the hook can refresh the flags without a second round-trip.
 *
 * A sibling of `UnresolvedCollisionsError` on purpose: both are 409s
 * that mean "your selection is not importable yet, here is precisely
 * what to fix", and both resolve through the same edit-or-skip loop.
 * Keeping the shapes parallel is what lets the hook's 409 handling and
 * the table's scroll-to-the-problem effect cover the new case by
 * extension rather than by a second mechanism.
 */
export class FieldTooLongError extends Error {
  readonly field_errors: ImportFieldError[];

  constructor(field_errors: ImportFieldError[]) {
    super('Import has fields that are too long');
    Object.setPrototypeOf(this, FieldTooLongError.prototype);
    this.name = 'FieldTooLongError';
    this.field_errors = field_errors;
  }
}

export class NotFoundError extends Error {
  readonly importID: string;

  constructor(importID: string) {
    super('Import session not found or expired');
    Object.setPrototypeOf(this, NotFoundError.prototype);
    this.name = 'NotFoundError';
    this.importID = importID;
  }
}

/**
 * Uploads a file and returns the initial preview. Thin wrapper over
 * the existing `api.upload` helper — the only reason this lives in a
 * dedicated module is to keep all five import endpoints co-located
 * with their types and error class.
 */
export function uploadImport(file: File): Promise<ImportPreview> {
  return api.upload<ImportPreview>('import/upload', file);
}

/**
 * Resumes an existing session. Called on component mount if a valid
 * import_id is in localStorage. On 404 (session expired or never
 * existed), throws `NotFoundError` carrying the attempted importID
 * — the hook catches it via `instanceof` and silently drops the
 * localStorage key.
 *
 * Bypasses `api.get` for the same reason `confirmImport` bypasses
 * `api.post`: `ApiClient.request` discards the status code on non-200
 * responses and throws a flat `Error`, so the caller cannot reliably
 * distinguish 404 from 500 without string-matching the error message
 * (fragile). Hitting fetch directly lets us type the 404 branch.
 */
export async function getImportSession(importID: string): Promise<ImportPreview> {
  const response = await fetch(
    `${apiBaseURL()}/import/${encodeURIComponent(importID)}`,
    { credentials: 'include' },
  );

  if (response.status === 404) {
    throw new NotFoundError(importID);
  }

  if (!response.ok) {
    const fallback = `HTTP ${response.status}`;
    const error = (await response.json().catch(() => null)) as
      | { error?: string }
      | null;
    throw new Error(error?.error || fallback);
  }

  return (await response.json()) as ImportPreview;
}

/**
 * Patches a single field on a single row. The backend returns the
 * FULL session snapshot (shape: PatchRowResponse = ImportPreview) so
 * the caller does not need to stitch together partial updates.
 */
export function patchImportRow(
  importID: string,
  rowID: number,
  body: PatchRowRequest,
): Promise<PatchRowResponse> {
  return api.patch<PatchRowResponse>(
    `import/${encodeURIComponent(importID)}/rows/${rowID}`,
    body,
  );
}

/**
 * Confirms the import. On 409 UNRESOLVED_COLLISIONS, throws an
 * `UnresolvedCollisionsError` carrying the full collision_groups array
 * so the hook can re-render collision state without a second GET.
 *
 * Bypasses `api.post` because the shared ApiClient.request method
 * discards the error body on non-200 responses (see Step 15.1). We
 * hit fetch directly for this one endpoint to keep the 409 body
 * intact; all other non-200 status codes (including 401, 403, 500)
 * fall through to the generic `!response.ok` branch, which extracts
 * `error.error` from the body — same contract as `ApiClient.request`.
 */
export async function confirmImport(payload: {
  import_id: string;
  default_category_id?: number;
  category_map: Record<string, number>;
}): Promise<ImportResult> {
  const response = await fetch(`${apiBaseURL()}/import/confirm`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });

  if (response.status === 409) {
    const body = (await response.json().catch(() => null)) as
      | {
          code?: string;
          collision_groups?: CollisionGroup[];
          field_errors?: ImportFieldError[];
          error?: string;
        }
      | null;
    // Each recognised code carries the payload the caller can act on.
    // Any other 409 (current or future — e.g. duplicate session,
    // optimistic-lock conflict) would leave both payloads empty, which
    // would look to the hook like "the problem resolved itself while we
    // waited" and silently retry. Fall those through to the generic
    // error branch so the user sees the server's actual `error` message
    // instead of a confusing no-op retry loop.
    if (body?.code === 'UNRESOLVED_COLLISIONS') {
      throw new UnresolvedCollisionsError(body.collision_groups ?? []);
    }
    if (body?.code === 'FIELD_TOO_LONG') {
      throw new FieldTooLongError(body.field_errors ?? []);
    }
    throw new Error(body?.error || `HTTP 409`);
  }

  if (!response.ok) {
    const fallback = `HTTP ${response.status}`;
    const error = (await response.json().catch(() => null)) as
      | { error?: string }
      | null;
    throw new Error(error?.error || fallback);
  }

  return (await response.json()) as ImportResult;
}

/**
 * Cancels an in-flight preview session, freeing the server-side slot.
 * Swallows errors — cancel is best-effort and the UI should drop the
 * client-side state regardless of whether the DELETE landed.
 */
export function cancelImport(importID: string): Promise<void> {
  return api.del(`import/${encodeURIComponent(importID)}`).catch(() => {});
}
