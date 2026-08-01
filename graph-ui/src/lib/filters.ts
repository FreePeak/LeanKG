import type { GraphData } from './types';

/**
 * FR-E31 — edge-type filter panel: toggle visibility by relationship type.
 * Pure functions so the filter state is testable without React.
 */

export function relationshipTypes(graph: GraphData | null): string[] {
  if (!graph) return [];
  const seen = new Set<string>();
  for (const r of graph.relationships) seen.add(r.type);
  return [...seen].sort();
}

export type TypeFilter = Record<string, boolean>;

/** Default: every relationship type visible. */
export function defaultTypeFilter(graph: GraphData | null): TypeFilter {
  const filter: TypeFilter = {};
  for (const t of relationshipTypes(graph)) filter[t] = true;
  return filter;
}

export function toggleType(filter: TypeFilter, type: string): TypeFilter {
  return { ...filter, [type]: !filter[type] };
}

export function visibleCount(filter: TypeFilter): number {
  return Object.values(filter).filter(Boolean).length;
}

export function totalCount(filter: TypeFilter): number {
  return Object.keys(filter).length;
}

/** Filter relationships to visible types only. */
export function filterRelationships(graph: GraphData | null, filter: TypeFilter): GraphData | null {
  if (!graph) return null;
  if (visibleCount(filter) === totalCount(filter)) return graph;
  return { ...graph, relationships: graph.relationships.filter((r) => filter[r.type]) };
}

/** Filter relationships to those incident on a selected node (E35 dim). */
export function relationshipsForNode(graph: GraphData | null, nodeId: string | null): number {
  if (!graph || !nodeId) return 0;
  return graph.relationships.filter(
    (r) => r.sourceId === nodeId || r.targetId === nodeId,
  ).length;
}
