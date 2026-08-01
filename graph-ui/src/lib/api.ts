import type { ApiEnvelope, GraphData, Layout3DResponse } from './types';

async function fetchEnvelope<T>(path: string, body?: string): Promise<T> {
  const res =
    body === undefined
      ? await fetch(path)
      : await fetch(path, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body,
        });
  // Backend returns 400 with a body for error envelopes (src/web/mod.rs
  // ApiResponse::into_response), so parse the body before the status check.
  const envelope = (await res.json().catch(() => null)) as ApiEnvelope<T> | null;
  if (!res.ok || envelope == null || !envelope.success || envelope.data == null) {
    throw new Error(envelope?.error ?? `HTTP ${res.status} ${path}`);
  }
  return envelope.data;
}

/** GET /api/graph/layout3d — deterministic seeded 3D layout (FR-E01). */
export function fetchLayout3D(
  iterations = 30,
  seed = 42,
): Promise<Layout3DResponse> {
  return fetchEnvelope<Layout3DResponse>(
    `/api/graph/layout3d?iterations=${iterations}&seed=${seed}`,
  );
}

/** GET /api/graph/data — element nodes + edges (FR-E03 detail source). */
export function fetchGraphData(): Promise<GraphData> {
  return fetchEnvelope<GraphData>('/api/graph/data');
}

/** GET /api/graph/clusters — directory clusters for FR-E04 coloring. */
export function fetchClusters(): Promise<GraphData> {
  return fetchEnvelope<GraphData>('/api/graph/clusters');
}

/** GET /api/file?path=… — file content for the FR-E30 code snippet. */
export async function fetchFileSnippet(path: string, maxLines = 40): Promise<string> {
  const res = await fetch(`/api/file?path=${encodeURIComponent(path)}`);
  const envelope = (await res.json().catch(() => null)) as ApiEnvelope<{
    content: string;
  } | null>;
  if (!res.ok || envelope == null || !envelope.success || envelope.data == null) {
    throw new Error(envelope?.error ?? `HTTP ${res.status} /api/file`);
  }
  return envelope.data.content.split('\n').slice(0, maxLines).join('\n');
}

/** GET /api/projects — registry + LEANKG_PROJECT_DIRS (FR-E33/E36). */
export async function fetchProjects(): Promise<
  { name: string; path: string; element_count?: number; last_indexed?: string }[]
> {
  return fetchEnvelope('/api/projects');
}

/** GET /api/index/status — element/relationship counts for stats (FR-E33). */
export async function fetchIndexStatus(): Promise<{
  element_count?: number;
  relationship_count?: number;
  project_path?: string;
  is_indexing?: boolean;
}> {
  return fetchEnvelope('/api/index/status');
}

/** POST /api/project/switch — switch active project (FR-E36). */
export async function switchProject(path: string): Promise<{ project_path?: string }> {
  return fetchEnvelope('/api/project/switch', JSON.stringify({ path, reindex: false }));
}
