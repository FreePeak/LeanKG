import type { GraphData, GraphNode } from './types';

export interface SelectedDetail {
  node: GraphNode;
  degree: number;
}

/** FR-E03 — resolve selected node id against /api/graph/data nodes + degree. */
export function selectedDetailOf(
  graph: GraphData | null,
  selectedId: string | null,
): SelectedDetail | null {
  if (!graph || !selectedId) return null;
  const node = graph.nodes.find((n) => n.id === selectedId);
  if (!node) return null;
  const degree = graph.relationships.filter(
    (r) => r.sourceId === selectedId || r.targetId === selectedId,
  ).length;
  return { node, degree };
}
