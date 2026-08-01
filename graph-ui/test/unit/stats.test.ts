/**
 * FR-E30/E33 — graph summary stats.
 */
import { describe, expect, it } from 'vitest';
import { computeStats, statsSummary } from '../../src/lib/stats';
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
      properties: { name: 'b', filePath: 'b.rs', elementType: 'Class' },
    },
  ],
  relationships: [
    { id: 'e1', sourceId: 'a', targetId: 'b', type: 'calls', confidenceLabel: 'HIGH' },
    { id: 'e2', sourceId: 'a', targetId: 'b', type: 'imports', confidenceLabel: 'HIGH' },
  ],
  filtered: null,
  hasMore: false,
};

describe('graph stats (FR-E30)', () => {
  it('computeStats counts nodes + edges with breakdowns', () => {
    const s = computeStats(GRAPH)!;
    expect(s.nodeCount).toBe(2);
    expect(s.edgeCount).toBe(2);
    expect(s.nodeTypes).toEqual({ Function: 1, Class: 1 });
    expect(s.edgeTypes).toEqual({ calls: 1, imports: 1 });
  });

  it('computeStats returns null for empty graph', () => {
    expect(computeStats(null)).toBeNull();
  });

  it('statsSummary renders counts', () => {
    expect(statsSummary(computeStats(GRAPH))).toBe('2 nodes / 2 edges');
    expect(statsSummary(null)).toBe('no data');
  });
});
