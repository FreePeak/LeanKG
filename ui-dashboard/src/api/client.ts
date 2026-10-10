// Typed fetch wrapper for /api/dashboard/v1 (plan v4.15 DS-21).
//
// Two sources, one interface: the live server (default; the Vite dev server
// proxies /api to 127.0.0.1:9701) and synthetic fixtures (VITE_FIXTURES=1 or
// a ?fixtures query flag), so `npm run dev` works with no server at all.

import type {
  ConsentRequest,
  Evidence,
  Failures,
  Memory,
  Overview,
  SessionDetail,
  SessionList,
  SessionsQuery,
  Settings,
  Tools,
  Transcript,
} from './types';

export const API_BASE = '/api/dashboard/v1';

/** Error raised for every failed API call. Pages render `message` verbatim. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export function fixturesEnabled(): boolean {
  if (import.meta.env.VITE_FIXTURES === '1') return true;
  try {
    return new URLSearchParams(window.location.search).has('fixtures');
  } catch {
    return false;
  }
}

type Params = Record<string, string | undefined>;

function withQuery(path: string, params?: Params): string {
  if (!params) return path;
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') q.set(k, v);
  }
  const s = q.toString();
  return s ? `${path}?${s}` : path;
}

/**
 * Perform a request and decode the JSON body. Every failure path throws an
 * ApiError with a human message: network loss, non-2xx (uses the server's
 * `error` field when present), empty success bodies are null, and malformed
 * JSON on success.
 */
export async function requestJSON<T>(
  fetchImpl: FetchLike,
  url: string,
  init?: RequestInit,
): Promise<T> {
  let res: Response;
  try {
    res = await fetchImpl(url, init);
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err;
    throw new ApiError(0, 'network', 'Cannot reach the dashboard server. Is `leankg dashboard` running?');
  }

  const text = await res.text().catch(() => '');

  if (!res.ok) {
    let message = res.statusText || `HTTP ${res.status}`;
    let code = `http_${res.status}`;
    try {
      const body = JSON.parse(text) as { error?: unknown; code?: unknown };
      if (typeof body.error === 'string' && body.error) message = body.error;
      if (typeof body.code === 'string' && body.code) code = body.code;
    } catch {
      if (text.trim()) message = text.trim().slice(0, 300);
    }
    throw new ApiError(res.status, code, message);
  }

  if (text.trim() === '') return null as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError(res.status, 'invalid_json', 'The server returned a response that is not valid JSON.');
  }
}

export interface DashboardApi {
  overview(since?: string, signal?: AbortSignal): Promise<Overview>;
  sessions(q?: SessionsQuery, signal?: AbortSignal): Promise<SessionList>;
  session(id: string, signal?: AbortSignal): Promise<SessionDetail>;
  transcript(id: string, signal?: AbortSignal): Promise<Transcript>;
  failures(group: string, signal?: AbortSignal): Promise<Failures>;
  tools(signal?: AbortSignal): Promise<Tools>;
  memory(q?: { bank?: string; since?: string }, signal?: AbortSignal): Promise<Memory>;
  evidence(signal?: AbortSignal): Promise<Evidence>;
  consent(signal?: AbortSignal): Promise<Settings>;
  postConsent(req: ConsentRequest, csrfToken: string, signal?: AbortSignal): Promise<void>;
}

/** Build the live HTTP client over an injected fetch (tests pass a stub). */
export function createHttpApi(fetchImpl: FetchLike = (u, i) => fetch(u, i)): DashboardApi {
  const get = <T,>(path: string, params?: Params, signal?: AbortSignal) =>
    requestJSON<T>(fetchImpl, withQuery(`${API_BASE}${path}`, params), { signal, headers: { Accept: 'application/json' } });

  return {
    overview: (since, signal) => get<Overview>('/overview', { since }, signal),
    sessions: (q, signal) => get<SessionList>('/sessions', { ...q }, signal),
    session: (id, signal) => get<SessionDetail>(`/sessions/${encodeURIComponent(id)}`, undefined, signal),
    transcript: (id, signal) => get<Transcript>(`/sessions/${encodeURIComponent(id)}/transcript`, undefined, signal),
    failures: (group, signal) => get<Failures>('/failures', { group }, signal),
    tools: (signal) => get<Tools>('/tools', undefined, signal),
    memory: (q, signal) => get<Memory>('/memory', { ...q }, signal),
    evidence: (signal) => get<Evidence>('/evidence', undefined, signal),
    consent: (signal) => get<Settings>('/consent', undefined, signal),
    postConsent: async (req, csrfToken, signal) => {
      await requestJSON<unknown>(fetchImpl, `${API_BASE}/consent`, {
        method: 'POST',
        signal,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken, Accept: 'application/json' },
        body: JSON.stringify(req),
      });
    },
  };
}

let active: DashboardApi | null = null;

/** The API the app uses: fixtures when enabled, otherwise the live server. */
export async function getApi(): Promise<DashboardApi> {
  if (active) return active;
  if (fixturesEnabled()) {
    const { createFixtureApi } = await import('@/fixtures');
    active = createFixtureApi();
  } else {
    active = createHttpApi();
  }
  return active;
}

/** Test hook: replace the active API. */
export function setApiForTests(api: DashboardApi | null): void {
  active = api;
}
