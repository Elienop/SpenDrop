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

class ApiClient {
  private async request<T>(
    path: string,
    options?: RequestInit,
  ): Promise<T> {
    const response = await fetch(`${API_BASE_URL}/${path}`, {
      ...options,
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        ...options?.headers,
      },
    });

    if (response.status === 401) {
      throw new ApiError('Unauthorized', 401);
    }

    if (!response.ok) {
      const fallback = `HTTP ${response.status}`;
      const error = await response
        .json()
        .catch(() => null);
      throw new ApiError(
        (error as { error?: string } | null)?.error || fallback,
        response.status,
      );
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

  async upload<T>(path: string, file: File, fieldName = 'file'): Promise<T> {
    const form = new FormData();
    form.append(fieldName, file);

    const response = await fetch(`${API_BASE_URL}/${path}`, {
      method: 'POST',
      credentials: 'include',
      body: form,
      // Do NOT set Content-Type — browser sets it with boundary
    });

    if (response.status === 401) {
      throw new ApiError('Unauthorized', 401);
    }

    if (!response.ok) {
      const fallback = `HTTP ${response.status}`;
      const error = await response
        .json()
        .catch(() => null);
      throw new ApiError(
        (error as { error?: string } | null)?.error || fallback,
        response.status,
      );
    }

    // 204 No Content carries no body (see `request` above) — short-circuit.
    if (response.status === 204) {
      return undefined as T;
    }

    return response.json() as Promise<T>;
  }
}

export const api = new ApiClient();
