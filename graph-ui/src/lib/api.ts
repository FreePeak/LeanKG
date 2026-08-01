import type { ApiEnvelope, GraphData, Layout3DResponse } from './types';

async function fetchEnvelope<T>(path: string): Promise<T> {
  const res = await fetch(path);
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
