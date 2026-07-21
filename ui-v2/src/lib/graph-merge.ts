import type { KnowledgeGraph } from '../core/graph/types';
import { toKnowledgeGraph } from '../core/graph/types';

/**
 * Merge an expand-service page into the current graph by node/edge id.
 * Existing nodes win; new edges append when id is unique.
 */
export function mergeKnowledgeGraphs(
  base: KnowledgeGraph,
  page: KnowledgeGraph,
): KnowledgeGraph {
  const nodeById = new Map(base.nodes.map((n) => [n.id, n]));
  for (const n of page.nodes) {
    if (!nodeById.has(n.id)) nodeById.set(n.id, n);
  }

  const edgeById = new Map<string, (typeof base.relationships)[number]>();
  for (const e of base.relationships) {
    const id = e.id ?? `${e.sourceId}|${e.type}|${e.targetId}`;
    edgeById.set(id, e.id ? e : { ...e, id });
  }
  for (const e of page.relationships) {
    const id = e.id ?? `${e.sourceId}|${e.type}|${e.targetId}`;
    if (!edgeById.has(id)) {
      edgeById.set(id, e.id ? e : { ...e, id });
    }
  }

  return toKnowledgeGraph([...nodeById.values()], [...edgeById.values()]);
}

/** Advance pagination cursor by the requested page size (not rendered count). */
export function nextExpandOffset(offset: number, pageLimit: number): number {
  return offset + pageLimit;
}
