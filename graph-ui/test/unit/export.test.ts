/**
 * FR-E37 — export/share: JSON snapshot + shareable URL.
 */
import { describe, expect, it } from 'vitest';
import { buildShareUrl, buildSnapshot } from '../../src/lib/export';
import type { GraphData } from '../../src/lib/types';

const GRAPH: GraphData = {
  nodes: [
    {
      id: 'a',
      label: 'A',
      properties: { name: 'a', filePath: 'a.rs', elementType: 'Function' },
    },
  ],
  relationships: [],
  filtered: null,
  hasMore: false,
};

describe('export (FR-E37)', () => {
  it('buildSnapshot captures graph + counts', () => {
    const snap = buildSnapshot(GRAPH);
    expect(snap.format).toBe('leankg-graph-ui');
    expect(snap.stats).toEqual({ nodeCount: 1, edgeCount: 0 });
    expect(snap.graph).toEqual(GRAPH);
  });

  it('buildShareUrl keeps tab + project params', () => {
    const url = buildShareUrl('http://localhost/3d', '/workspace', 'search');
    const parsed = new URL(url);
    expect(parsed.searchParams.get('tab')).toBe('search');
    expect(parsed.searchParams.get('project')).toBe('/workspace');
  });
});
