/**
 * FR-E31 — edge-type filter panel logic.
 */
import { describe, expect, it } from 'vitest';
import {
  defaultTypeFilter,
  filterRelationships,
  relationshipTypes,
  toggleType,
  visibleCount,
} from '../../src/lib/filters';
import type { GraphData } from '../../src/lib/types';

const GRAPH: GraphData = {
  nodes: [
    {
      id: 'a',
      label: 'A',
      properties: { name: 'a', filePath: 'a.rs', elementType: 'Function' },
    },
    {
      id: 'b',
      label: 'B',
      properties: { name: 'b', filePath: 'b.rs', elementType: 'Function' },
    },
  ],
  relationships: [
    { id: 'e1', sourceId: 'a', targetId: 'b', type: 'calls', confidenceLabel: 'HIGH' },
    { id: 'e2', sourceId: 'a', targetId: 'b', type: 'imports', confidenceLabel: 'HIGH' },
  ],
  filtered: null,
  hasMore: false,
};

describe('edge filter (FR-E31)', () => {
  it('relationshipTypes lists unique types sorted', () => {
    expect(relationshipTypes(GRAPH)).toEqual(['calls', 'imports']);
    expect(relationshipTypes(null)).toEqual([]);
  });

  it('defaultTypeFilter shows every type', () => {
    const f = defaultTypeFilter(GRAPH);
    expect(f).toEqual({ calls: true, imports: true });
    expect(visibleCount(f)).toBe(2);
  });

  it('toggleType flips one type', () => {
    const f = defaultTypeFilter(GRAPH);
    const next = toggleType(f, 'calls');
    expect(next.calls).toBe(false);
    expect(next.imports).toBe(true);
  });

  it('filterRelationships drops edges of hidden types', () => {
    const f = { calls: false, imports: true };
    const out = filterRelationships(GRAPH, f);
    expect(out?.relationships).toHaveLength(1);
    expect(out?.relationships[0].type).toBe('imports');
  });

  it('filterRelationships returns graph unchanged when all types visible', () => {
    const out = filterRelationships(GRAPH, defaultTypeFilter(GRAPH));
    expect(out).toBe(GRAPH);
  });
});
