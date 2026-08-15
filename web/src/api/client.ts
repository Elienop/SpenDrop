// Resolve the API base URL once at module load time. Trailing slashes
// are stripped so callers can always concatenate with `/${path}` without
// worrying about doubling up. In typical deployments the frontend is
// served by the Go binary at the same origin, so the default `/api`
// (relative) is correct; `VITE_API_BASE_URL` lets dev setups and
// reverse-proxied hosts point at an absolute URL.
const API_BASE_URL = (
  (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? '/api'
).replace(/\/+$/, '');

// Error thrown for every non-OK API response. It extends the built-in
// Error additively: `.message` keeps the legacy contract (the bare string
// 'Unauthorized' for 401s, the server `{error}` body otherwise) so existing
// message-based checks keep working, while `.status` exposes the raw HTTP
// status so callers can branch on it robustly instead of matching strings.
export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

/**
 * Why a transport failure has its own class:
 *
 * `ApiError` means the server ANSWERED and said no. A dropped radio, a
 * captive portal, or a request that hung until it was aborted means the
 * server never answered at all — and the two are not interchangeable:
 *
 *  - the auth bootstrap must sign the user out on a real 401 and must NOT on
 *    an unanswered request (that is the "logged out when I left the house"
 *    bug);
 *  - the offline replay queue counts an `ApiError` as "the server rejected
 *    THIS row" and gives up after MAX_ATTEMPTS. A timeout misfiled as an
 *    `ApiError` would strand a real purchase behind a permanent "Not synced"
 *    badge (see offline-queue.ts).
 *
 * `fetch` rejects with a bare `TypeError` for a network failure and with a
 * `DOMException` for an abort — neither is one of ours, so every caller
 * would have to guess. Converting at this single edge is the fix, not a
 * cosmetic detail: it means `err instanceof ApiError === false` reliably
 * implies "we do not know whether the server saw this request".
 */
export type NetworkErrorKind =
  /** `navigator.onLine === false` when the request failed. */
  | 'offline'
  /** The request was aborted — nothing came back within REQUEST_TIMEOUT_MS. */
  | 'timeout'
  /** The device believes it is online but the server could not be reached. */
  | 'unreachable';

export class NetworkError extends Error {
  readonly kind: NetworkErrorKind;

  constructor(
    message: string,
    kind: NetworkErrorKind,
    options?: { cause?: unknown },
  ) {
    super(message, options);
    this.name = 'NetworkError';
    this.kind = kind;
  }
}

// A request that never completes is indistinguishable from one that failed,
// except that it also never lets the caller recover: the auth bootstrap would
// sit on `loading` forever and the offline replay would never move on. A dead
// mobile radio does exactly this, so every JSON request carries a deadline.
// Uploads are deliberately exempt (see `upload`).
const REQUEST_TIMEOUT_MS = 20_000;

function isOffline(): boolean {
  return typeof navigator !== 'undefined' && navigator.onLine === false;
}

function isAbort(err: unknown): boolean {
  // A timeout signal aborts with `TimeoutError`; an explicit
  // AbortController.abort() with `AbortError`. happy-dom/jsdom may raise a
  // plain Error rather than a DOMException, so match on `name` either way.
  const name =
    err instanceof DOMException || err instanceof Error ? err.name : '';
  return name === 'AbortError' || name === 'TimeoutError';
}

function toNetworkError(err: unknown): NetworkError {
  if (err instanceof NetworkError) return err;
  if (isAbort(err)) {
    return new NetworkError(
      'The server took too long to answer',
      'timeout',
      { cause: err },
    );
  }
  if (isOffline()) {
    return new NetworkError('The device is offline', 'offline', { cause: err });
  }
  return new NetworkError('Could not reach the server', 'unreachable', {
    cause: err,
  });
}

/**
 * A deadline signal, or `undefined` where `AbortSignal.timeout` is missing
 * (older browsers, some test environments) — the request then behaves exactly
 * as it did before this was introduced rather than failing outright.
 */
function timeoutSignal(ms: number): AbortSignal | undefined {
  return typeof AbortSignal !== 'undefined' &&
    typeof AbortSignal.timeout === 'function'
    ? AbortSignal.timeout(ms)
    : undefined;
}

/**
 * The `ApiError` for a non-OK response, carrying the server's own sentence
 * whenever it sent one.
 *
 * KEY ORDER IS THE CONTRACT: `error`, then `message`, then the bare status.
 * Most of the API answers a 4xx with `{"error": "..."}` (helpers.go's
 * `writeError`), and that stays first so nothing about existing endpoints
 * moves. The import preview's per-field rejections answer with
 * `{code, field, message}` instead — the same sentence the preview shows as a
 * row flag, which the client routes to one cell — and reading only `error`
 * put the string "HTTP 400" in that cell, on top of a correct flag. A status
 * code is the one thing a validation message must never be.
 *
 * Widened HERE rather than by hand-rolling that one endpoint's `fetch` in
 * `api/import.ts`: the 20s deadline, the NetworkError conversion and the 401
 * mapping all live in `request`, and each is a guarantee (documented above)
 * that a fetch-direct copy would quietly drop for that endpoint. The wider
 * blast radius is one-directional and small — a body carrying `message` and
 * no `error` now shows the server's words instead of a status code.
 *
 * Shared by `request` and `upload` because they had the same block twice, and
 * two copies of an extraction rule is how one of them ends up a key behind.
 */
async function apiErrorFrom(response: Response): Promise<ApiError> {
  const fallback = `HTTP ${response.status}`;
  const body = (await response.json().catch(() => null)) as
    | { error?: string; message?: string }
    | null;
  return new ApiError(body?.error || body?.message || fallback, response.status);
}

class ApiClient {
  private async request<T>(
    path: string,
    options?: RequestInit,
  ): Promise<T> {
    let response: Response;
    try {
      response = await fetch(`${API_BASE_URL}/${path}`, {
        signal: timeoutSignal(REQUEST_TIMEOUT_MS),
        ...options,
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          ...options?.headers,
        },
      });
    } catch (err) {
      // The server never answered. Never let this reach a caller as an
      // ApiError — see the NetworkError doc comment above.
      throw toNetworkError(err);
    }

    if (response.status === 401) {
      throw new ApiError('Unauthorized', 401);
    }

    if (!response.ok) {
      throw await apiErrorFrom(response);
    }

    // 204 No Content carries no body — `response.json()` would reject with a
    // SyntaxError on the empty stream, surfacing a successful request as an
    // error (e.g. DELETE /push/subscriptions). Short-circuit before parsing.
    if (response.status === 204) {
      return undefined as T;
    }

    return response.json() as Promise<T>;
  }

  get<T>(path: string): Promise<T> {
    return this.request<T>(path);
  }

  post<T>(path: string, body?: unknown): Promise<T> {
    return this.request<T>(path, {
      method: 'POST',
      body: JSON.stringify(body),
    });
  }

  put<T>(path: string, body?: unknown): Promise<T> {
    return this.request<T>(path, {
      method: 'PUT',
      body: JSON.stringify(body),
    });
  }

  patch<T>(path: string, body?: unknown): Promise<T> {
    return this.request<T>(path, {
      method: 'PATCH',
      body: JSON.stringify(body),
    });
  }

  // Generic over the response body so handlers that return a
  // meaningful payload (e.g. DELETE /transactions/trash returns
  // `{purged: N}`) can be called as `api.del<Shape>(path)` and still
  // typecheck. Callers that don't care pass `api.del(path)` — the
  // default `T = void` keeps the original ergonomics.
  del<T = void>(path: string, body?: unknown): Promise<T> {
    return this.request<T>(path, {
      method: 'DELETE',
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    });
  }

  // No REQUEST_TIMEOUT_MS here on purpose: a spreadsheet import is a
  // legitimately long upload on a slow link, and a 20s deadline would abort
  // healthy transfers. Transport failures are still converted, so callers can
  // discriminate them from a server rejection.
  async upload<T>(path: string, file: File, fieldName = 'file'): Promise<T> {
    const form = new FormData();
    form.append(fieldName, file);

    let response: Response;
    try {
      response = await fetch(`${API_BASE_URL}/${path}`, {
        method: 'POST',
        credentials: 'include',
        body: form,
        // Do NOT set Content-Type — browser sets it with boundary
      });
    } catch (err) {
      throw toNetworkError(err);
    }

    if (response.status === 401) {
      throw new ApiError('Unauthorized', 401);
    }

    if (!response.ok) {
      throw await apiErrorFrom(response);
    }

    // 204 No Content carries no body (see `request` above) — short-circuit.
    if (response.status === 204) {
      return undefined as T;
    }

    return response.json() as Promise<T>;
  }
}

export const api = new ApiClient();
