/**
 * FR-UI2-10 / US-UI2-08 — cluster legend helpers.
 *
 * `/api/graph/clusters` returns a GraphData payload where each node has:
 *   id: "cluster:<path>", label: "<dir> (<n>)",
 *   properties: { name: <path>, filePath: <path>, elementType: "Cluster[n files]" }
 */
import type { KnowledgeGraph, GraphNode } from '../core/graph/types';
import { normalizeGraphPayload } from './normalize';
import { fetchClusters } from '../services/backend-client';

/** Directory path of a cluster node ("" for root). */
export function clusterPathOf(node: GraphNode): string {
  const props = String(node.properties?.filePath ?? '');
  return props === 'root' ? '' : props;
}

/** Cluster color from a stable hash of the cluster id. */
export function clusterColorOf(id: string): string {
  const palette = [
    '#ef4444', '#f97316', '#f59e0b', '#84cc16', '#22c55e',
    '#14b8a6', '#06b6d4', '#3b82f6', '#6366f1', '#8b5cf6',
    '#d946ef', '#ec4899',
  ];
  let h = 0;
  for (let i = 0; i < id.length; i += 1) {
    h = (h * 31 + id.charCodeAt(i)) | 0;
  }
  return palette[Math.abs(h) % palette.length];
}

/** Fetch cluster overview and normalize to a KnowledgeGraph (US-UI2-08 seam). */
export async function loadClusterGraph(): Promise<KnowledgeGraph> {
  const data = await fetchClusters();
  return normalizeGraphPayload(data);
}
