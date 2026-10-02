// Shared fetch wrapper for the generated Vue Query hooks: same-origin API under /api/v1,
// session cookie for authentication, RFC 9457 problem+json errors.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly problem: { code?: string; title?: string; detail?: string } | undefined,
  ) {
    super(problem?.detail ?? problem?.title ?? `HTTP ${status}`);
  }
}

export const customFetch = async <T>(url: string, options: RequestInit): Promise<T> => {
  const response = await fetch(url, { credentials: 'same-origin', ...options });
  const text = await response.text();
  const body = text ? JSON.parse(text) : undefined;
  if (!response.ok) {
    throw new ApiError(response.status, body);
  }
  return body as T;
};
