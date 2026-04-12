// Resolve the API base URL once at module load time. Trailing slashes
// are stripped so callers can always concatenate with `/${path}` without
// worrying about doubling up. In typical deployments the frontend is
// served by the Go binary at the same origin, so the default `/api`
// (relative) is correct; `VITE_API_BASE_URL` lets dev setups and
// reverse-proxied hosts point at an absolute URL.
const API_BASE_URL = (
  (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? '/api'
).replace(/\/+$/, '');

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
      throw new Error('Unauthorized');
    }

    if (!response.ok) {
      const fallback = `HTTP ${response.status}`;
      const error = await response
        .json()
        .catch(() => null);
      throw new Error(
        (error as { error?: string } | null)?.error || fallback,
      );
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

  del(path: string): Promise<void> {
    return this.request(path, { method: 'DELETE' });
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
      throw new Error('Unauthorized');
    }

    if (!response.ok) {
      const fallback = `HTTP ${response.status}`;
      const error = await response
        .json()
        .catch(() => null);
      throw new Error(
        (error as { error?: string } | null)?.error || fallback,
      );
    }

    return response.json() as Promise<T>;
  }
}

export const api = new ApiClient();
