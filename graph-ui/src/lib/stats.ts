import type { GraphData } from './types';

/**
 * FR-E30/E33 — graph summary/stats: node + edge counts, relationship-type
 * breakdown, and per-element-type node counts. Pure functions.
 */
export interface GraphStats {
  nodeCount: number;
  edgeCount: number;
  nodeTypes: Record<string, number>;
  edgeTypes: Record<string, number>;
}

export function computeStats(graph: GraphData | null): GraphStats | null {
  if (!graph) return null;
  const nodeTypes: Record<string, number> = {};
  for (const n of graph.nodes) {
    const t = n.properties.elementType || 'Unknown';
    nodeTypes[t] = (nodeTypes[t] ?? 0) + 1;
  }
  const edgeTypes: Record<string, number> = {};
  for (const r of graph.relationships) {
    edgeTypes[r.type] = (edgeTypes[r.type] ?? 0) + 1;
  }
  return {
    nodeCount: graph.nodes.length,
    edgeCount: graph.relationships.length,
    nodeTypes,
    edgeTypes,
  };
}

export function statsSummary(stats: GraphStats | null): string {
  if (!stats) return 'no data';
  return `${stats.nodeCount} nodes / ${stats.edgeCount} edges`;
}
