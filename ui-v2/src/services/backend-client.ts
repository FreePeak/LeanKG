import type { ApiEnvelope } from '../lib/normalize';
import { unwrapEnvelope, normalizeGraphPayload } from '../lib/normalize';
import type { KnowledgeGraph } from '../core/graph/types';
import { normalizeExpandPath } from '../lib/camera-fit';

let _baseUrl = '';

export function setBackendBaseUrl(url: string) {
  _baseUrl = url.replace(/\/$/, '');
}

export function getBackendBaseUrl(): string {
  return _baseUrl;
}

function apiUrl(path: string): string {
  if (!_baseUrl) return path;
  return `${_baseUrl}${path.startsWith('/') ? path : `/${path}`}`;
}

async function fetchJson<T>(path: string, init?: RequestInit): Promise<T> {
  const ctrl = new AbortController();
  const timeoutMs = 60_000;
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetch(apiUrl(path), {
      ...init,
      signal: ctrl.signal,
      headers: {
        Accept: 'application/json',
        ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
        ...init?.headers,
      },
    });
    if (!res.ok) {
      let detail = '';
      try {
        const body = (await res.json()) as { error?: unknown };
        if (body?.error != null) detail = String(body.error);
      } catch {
        /* non-JSON error body */
      }
      throw new Error(detail || `HTTP ${res.status} ${path}`);
    }
    return res.json() as Promise<T>;
  } finally {
    clearTimeout(timer);
  }
}

export interface IndexStatus {
  initialized?: boolean;
  index_populated?: boolean;
  element_count?: number;
  relationship_count?: number;
  project_path?: string;
  [key: string]: unknown;
}

export async function fetchIndexStatus(): Promise<IndexStatus> {
  const json = await fetchJson<ApiEnvelope<IndexStatus>>('/api/index/status');
  return unwrapEnvelope(json, 'Failed to load index status');
}

export async function probeBackend(timeoutMs = 2000): Promise<boolean> {
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetch(apiUrl('/api/index/status'), { signal: ctrl.signal });
    return res.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(t);
  }
}

export async function fetchServiceTopology(): Promise<KnowledgeGraph> {
  const json = await fetchJson<ApiEnvelope<{ nodes: unknown[]; relationships: unknown[] }>>(
    '/api/graph/service-topology',
  );
  const data = unwrapEnvelope(json, 'Failed to load service topology');
  return normalizeGraphPayload(data);
}

export async function expandService(
  path: string,
  all = true,
  projectPath?: string,
): Promise<KnowledgeGraph> {
  const q = new URLSearchParams();
  const normalized = normalizeExpandPath(path, projectPath);
  if (normalized) q.set('path', normalized);
  if (all) q.set('all', 'true');
  const json = await fetchJson<ApiEnvelope<{ nodes: unknown[]; relationships: unknown[] }>>(
    `/api/graph/expand-service?${q.toString()}`,
  );
  const data = unwrapEnvelope(json, 'Failed to expand service');
  return normalizeGraphPayload(data);
}

export async function fetchChildren(parent: string): Promise<KnowledgeGraph> {
  const q = new URLSearchParams({ parent });
  const json = await fetchJson<ApiEnvelope<{ nodes: unknown[]; relationships: unknown[] }>>(
    `/api/graph/children?${q.toString()}`,
  );
  const data = unwrapEnvelope(json, 'Failed to load children');
  return normalizeGraphPayload(data);
}

export async function fetchClusters(): Promise<unknown> {
  const json = await fetchJson<ApiEnvelope<unknown>>('/api/graph/clusters');
  return unwrapEnvelope(json, 'Failed to load clusters');
}

export async function searchCode(query: string, limit = 50): Promise<unknown[]> {
  const q = new URLSearchParams({ q: query, limit: String(limit) });
  const json = await fetchJson<ApiEnvelope<unknown>>(`/api/search?${q.toString()}`);
  const data = unwrapEnvelope(json, 'Search failed');
  if (Array.isArray(data)) return data;
  if (data && typeof data === 'object' && Array.isArray((data as { results?: unknown[] }).results)) {
    return (data as { results: unknown[] }).results;
  }
  return [];
}

export async function readFile(path: string): Promise<string> {
  const q = new URLSearchParams({ path });
  const json = await fetchJson<ApiEnvelope<{ content?: string } | string>>(`/api/file?${q.toString()}`);
  const data = unwrapEnvelope(json, 'Failed to read file');
  if (typeof data === 'string') return data;
  if (data && typeof data === 'object' && typeof (data as { content?: string }).content === 'string') {
    return (data as { content: string }).content;
  }
  return JSON.stringify(data, null, 2);
}

export async function runQuery(query: string): Promise<unknown> {
  const json = await fetchJson<ApiEnvelope<unknown>>('/api/query', {
    method: 'POST',
    body: JSON.stringify({ query }),
  });
  return unwrapEnvelope(json, 'Query failed');
}

export async function switchProject(path: string): Promise<void> {
  const json = await fetchJson<ApiEnvelope<unknown>>('/api/project/switch', {
    method: 'POST',
    body: JSON.stringify({ path }),
  });
  unwrapEnvelope(json, 'Project switch failed');
}

export function parseProjectParam(value: string | null | undefined): string | undefined {
  if (value == null || value.trim() === '') return undefined;
  return value.trim();
}

export function writeProjectToUrl(project: string | undefined) {
  const url = new URL(window.location.href);
  if (project) url.searchParams.set('project', project);
  else url.searchParams.delete('project');
  window.history.replaceState({}, '', url.toString());
}
