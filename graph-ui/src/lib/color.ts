/**
 * FR-E04 cluster coloring — stable color per directory cluster id.
 * Mirrors the backend cluster rule (src/web/handlers.rs api_graph_clusters):
 * parent directory of filePath, else "root".
 */
export const CLUSTER_COLORS: readonly string[] = [
  '#60a5fa', // blue
  '#f472b6', // pink
  '#34d399', // green
  '#fbbf24', // amber
  '#a78bfa', // violet
  '#f87171', // red
  '#2dd4bf', // teal
  '#fb923c', // orange
];

export function clusterIdOf(filePath: string): string {
  if (!filePath) return 'root';
  const idx = filePath.lastIndexOf('/');
  return idx >= 0 ? filePath.slice(0, idx) : 'root';
}

/** Stable hash so a cluster always maps to the same color (ordinal 0-based). */
export function clusterColorOf(clusterId: string): string {
  let hash = 0;
  for (let i = 0; i < clusterId.length; i += 1) {
    hash = (hash * 31 + clusterId.charCodeAt(i)) | 0;
  }
  return CLUSTER_COLORS[Math.abs(hash) % CLUSTER_COLORS.length];
}
