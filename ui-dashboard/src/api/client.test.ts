import { describe, expect, it, vi } from 'vitest';
import { ApiError, createHttpApi, requestJSON, type FetchLike } from './client';

function response(body: string, init: { status?: number; statusText?: string } = {}): Response {
  return new Response(body, { status: init.status ?? 200, statusText: init.statusText ?? '' });
}

describe('requestJSON error handling', () => {
  it('decodes a JSON success body', async () => {
    const f: FetchLike = vi.fn(async () => response('{"total":2,"sessions":[]}'));
    await expect(requestJSON<{ total: number }>(f, '/x')).resolves.toEqual({ total: 2, sessions: [] });
  });

  it('returns null for an empty success body', async () => {
    const f: FetchLike = async () => response('');
    await expect(requestJSON(f, '/x')).resolves.toBeNull();
  });

  it('throws a network ApiError when fetch rejects', async () => {
    const f: FetchLike = async () => {
      throw new TypeError('Failed to fetch');
    };
    const err = await requestJSON(f, '/x').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(0);
    expect((err as ApiError).code).toBe('network');
    expect((err as ApiError).message).toContain('leankg dashboard');
  });

  it('re-throws an AbortError unchanged so callers can ignore it', async () => {
    const f: FetchLike = async () => {
      throw new DOMException('aborted', 'AbortError');
    };
    await expect(requestJSON(f, '/x')).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('uses the server error field for a non-2xx response', async () => {
    const f: FetchLike = async () => response('{"error":"group must be reason, tool or client"}', { status: 400, statusText: 'Bad Request' });
    const err = await requestJSON(f, '/x').catch((e: unknown) => e) as ApiError;
    expect(err.status).toBe(400);
    expect(err.message).toBe('group must be reason, tool or client');
    expect(err.code).toBe('http_400');
  });

  it('falls back to the status text for a non-JSON error body', async () => {
    const f: FetchLike = async () => response('<html>proxy error</html>', { status: 502, statusText: 'Bad Gateway' });
    const err = await requestJSON(f, '/x').catch((e: unknown) => e) as ApiError;
    expect(err.status).toBe(502);
    expect(err.message).toBe('<html>proxy error</html>');
  });

  it('reports malformed JSON on a 200 response', async () => {
    const f: FetchLike = async () => response('{"total":');
    const err = await requestJSON(f, '/x').catch((e: unknown) => e) as ApiError;
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe('invalid_json');
  });
});

describe('createHttpApi', () => {
  it('builds the consent POST with the CSRF header and a JSON body', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    const f: FetchLike = async (url, init) => {
      calls.push({ url, init });
      return response('{}');
    };
    const api = createHttpApi(f);
    await api.postConsent({ capture: 'metadata', sessions: ['claude-code'], purge: false }, 'tok-123');
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe('/api/dashboard/v1/consent');
    expect(calls[0].init?.method).toBe('POST');
    const headers = calls[0].init?.headers as Record<string, string>;
    expect(headers['X-CSRF-Token']).toBe('tok-123');
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ capture: 'metadata', sessions: ['claude-code'], purge: false });
  });

  it('encodes session ids and adds query parameters only when set', async () => {
    const urls: string[] = [];
    const f: FetchLike = async (url) => {
      urls.push(url);
      return response('{"total":0,"sessions":[]}');
    };
    const api = createHttpApi(f);
    await api.sessions({ client: 'pi', project: '', since: '7d' });
    await api.session('a/b c');
    expect(urls[0]).toBe('/api/dashboard/v1/sessions?client=pi&since=7d');
    expect(urls[1]).toBe('/api/dashboard/v1/sessions/a%2Fb%20c');
  });
});
