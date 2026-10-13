import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, UnreachableError, customFetch, onReachable, onUnauthorized, onUnavailable, onUnreachable, setCsrfToken } from './fetcher';

function respond(status: number, body: string): Response {
  return new Response(body, { status, headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => {
  vi.unstubAllGlobals();
  setCsrfToken(undefined);
});

describe('customFetch', () => {
  it('returns the parsed JSON body and sends the session cookie', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(200, '{"generation":42}'));
    vi.stubGlobal('fetch', fetchMock);

    await expect(customFetch<{ generation: number }>('/api/v1/state', { method: 'GET' })).resolves.toEqual({
      generation: 42,
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/state', { credentials: 'same-origin', method: 'GET' });
  });

  it('puts the generated paths under /api/v1', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(200, '{}'));
    vi.stubGlobal('fetch', fetchMock);
    await customFetch('/devices?limit=5', { method: 'GET' });
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/devices?limit=5');
  });

  it('sends the CSRF token on unsafe methods only', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(respond(200, '{}')));
    vi.stubGlobal('fetch', fetchMock);
    setCsrfToken('tok-1');
    await customFetch('/revisions', { method: 'POST', headers: { 'Content-Type': 'application/json' } });
    await customFetch('/state', { method: 'GET' });
    const post = new Headers(fetchMock.mock.calls[0]?.[1].headers);
    expect(post.get('X-CSRF-Token')).toBe('tok-1');
    expect(post.get('Content-Type')).toBe('application/json');
    expect(fetchMock.mock.calls[1]?.[1].headers).toBeUndefined();
  });

  it('returns undefined for an empty body (204)', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    await expect(customFetch('/api/v1/reset', { method: 'POST' })).resolves.toBeUndefined();
  });

  it('throws an ApiError that carries the problem+json document', async () => {
    const problem = { code: 'revision_conflict', title: 'Conflict', detail: 'revision 7 is not active any more' };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(409, JSON.stringify(problem))));

    const error = await customFetch('/api/v1/revisions', { method: 'POST' }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(409);
    expect((error as ApiError).code).toBe('revision_conflict');
    expect((error as ApiError).message).toBe('revision 7 is not active any more');
  });

  it('falls back to the title and then to the status for the message', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(503, '{"title":"Safe mode"}')));
    await expect(customFetch('/x', {})).rejects.toThrow('Safe mode');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(500, '')));
    await expect(customFetch('/x', {})).rejects.toThrow('HTTP 500');
  });

  it('tells whether the gateway answered', async () => {
    const reachable = vi.fn();
    const unreachable = vi.fn();
    const off = [onReachable(reachable), onUnreachable(unreachable)];
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    await expect(customFetch('/state', {})).rejects.toBeInstanceOf(UnreachableError);
    expect(unreachable).toHaveBeenCalledTimes(1);
    // a proxy without its backend is no answer either
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(502, '')));
    await expect(customFetch('/state', {})).rejects.toBeInstanceOf(UnreachableError);
    expect(unreachable).toHaveBeenCalledTimes(2);
    // an error of the API is an answer
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(404, '{"code":"not_found"}')));
    await expect(customFetch('/devices/x', {})).rejects.toBeInstanceOf(ApiError);
    expect(reachable).toHaveBeenCalledTimes(1);
    off.forEach((f) => f());
  });

  it('reports an ended session, but not a wrong password', async () => {
    const unauthorized = vi.fn();
    const off = onUnauthorized(unauthorized);
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(respond(401, '{"code":"unauthorized"}'))));
    await expect(customFetch('/auth/login', { method: 'POST' })).rejects.toBeInstanceOf(ApiError);
    await expect(customFetch('/auth/session', {})).rejects.toBeInstanceOf(ApiError);
    expect(unauthorized).not.toHaveBeenCalled();
    await expect(customFetch('/devices', {})).rejects.toBeInstanceOf(ApiError);
    expect(unauthorized).toHaveBeenCalledTimes(1);
    off();
  });

  it('tells about a 503 unavailable (setup open, safe mode), not about other 503s', async () => {
    const unavailable = vi.fn();
    const off = onUnavailable(unavailable);
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(respond(503, '{"code":"too_many_streams"}'))));
    await expect(customFetch('/state', {})).rejects.toBeInstanceOf(ApiError);
    expect(unavailable).not.toHaveBeenCalled();
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(respond(503, '{"code":"unavailable"}'))));
    await expect(customFetch('/state', {})).rejects.toBeInstanceOf(ApiError);
    expect(unavailable).toHaveBeenCalledTimes(1);
    off();
  });

  it('passes an abort through as it is', async () => {
    const abort = new DOMException('aborted', 'AbortError');
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(abort));
    await expect(customFetch('/devices', {})).rejects.toBe(abort);
  });
});
