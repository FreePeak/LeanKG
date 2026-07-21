import { describe, expect, it } from 'vitest';
import { mergeKnowledgeGraphs, nextExpandOffset } from '../../src/lib/graph-merge';
import type { KnowledgeGraph } from '../../src/core/graph/types';

function kg(
  nodes: { id: string }[],
  edges: { id: string; sourceId: string; targetId: string; type?: string }[],
): KnowledgeGraph {
  return {
    nodes: nodes.map((n) => ({
      id: n.id,
      label: 'File',
      properties: { name: n.id, filePath: n.id },
    })),
    relationships: edges.map((e) => ({
      id: e.id,
      sourceId: e.sourceId,
      targetId: e.targetId,
      type: e.type ?? 'CONTAINS',
    })),
    nodeCount: nodes.length,
    relationshipCount: edges.length,
  };
}

describe('mergeKnowledgeGraphs (FR-UI2-13)', () => {
  it('extends node/edge counts without replacing existing ids', () => {
    const base = kg(
      [{ id: 'a' }, { id: 'b' }],
      [{ id: 'e1', sourceId: 'a', targetId: 'b' }],
    );
    const page = kg(
      [{ id: 'b' }, { id: 'c' }],
      [
        { id: 'e1', sourceId: 'a', targetId: 'b' },
        { id: 'e2', sourceId: 'b', targetId: 'c' },
      ],
    );
    const merged = mergeKnowledgeGraphs(base, page);
    expect(merged.nodeCount).toBe(3);
    expect(merged.relationshipCount).toBe(2);
    expect(merged.nodes.map((n) => n.id).sort()).toEqual(['a', 'b', 'c']);
  });

  it('advances offset by page limit (500 → 700 with +200)', () => {
    expect(nextExpandOffset(0, 500)).toBe(500);
    expect(nextExpandOffset(500, 200)).toBe(700);
  });
});
