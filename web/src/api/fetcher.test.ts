import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, customFetch } from './fetcher';

function respond(status: number, body: string): Response {
  return new Response(body, { status, headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => vi.unstubAllGlobals());

describe('customFetch', () => {
  it('returns the parsed JSON body and sends the session cookie', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(200, '{"generation":42}'));
    vi.stubGlobal('fetch', fetchMock);

    await expect(customFetch<{ generation: number }>('/api/v1/state', { method: 'GET' })).resolves.toEqual({
      generation: 42,
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/state', { credentials: 'same-origin', method: 'GET' });
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
    expect((error as ApiError).problem?.code).toBe('revision_conflict');
    expect((error as ApiError).message).toBe('revision 7 is not active any more');
  });

  it('falls back to the title and then to the status for the message', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(503, '{"title":"Safe mode"}')));
    await expect(customFetch('/x', {})).rejects.toThrow('Safe mode');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(500, '')));
    await expect(customFetch('/x', {})).rejects.toThrow('HTTP 500');
  });
});
