/**
 * API client — envelope unwrap + endpoint shapes for
 * /api/graph/layout3d, /api/graph/data, /api/graph/clusters.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fetchClusters, fetchGraphData, fetchLayout3D } from '../../src/lib/api';

const LAYOUT_ENVELOPE = {
  success: true,
  data: {
    nodes: [{ node_id: 'src/main.rs::main', x: 1, y: 2, z: 3 }],
    bounds: { min_x: -5, max_x: 5, min_y: -5, max_y: 5, min_z: -5, max_z: 5 },
  },
  error: null,
};

const GRAPH_ENVELOPE = {
  success: true,
  data: {
    nodes: [
      {
        id: 'src/main.rs::main',
        label: 'Main',
        properties: { name: 'main', filePath: 'src/main.rs', elementType: 'Function' },
      },
    ],
    relationships: [],
    filtered: null,
    hasMore: false,
  },
  error: null,
};

describe('api client', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('fetchLayout3D hits /api/graph/layout3d with deterministic seed params', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(LAYOUT_ENVELOPE), { status: 200 }),
    );
    const layout = await fetchLayout3D();
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/graph/layout3d?iterations=30&seed=42');
    expect(layout.nodes[0].node_id).toBe('src/main.rs::main');
    expect(layout.bounds.max_x).toBe(5);
  });

  it('fetchGraphData hits /api/graph/data and unwraps the envelope', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(GRAPH_ENVELOPE), { status: 200 }),
    );
    const graph = await fetchGraphData();
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/graph/data');
    expect(graph.nodes[0].properties.filePath).toBe('src/main.rs');
  });

  it('fetchClusters hits /api/graph/clusters', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(GRAPH_ENVELOPE), { status: 200 }),
    );
    await fetchClusters();
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/graph/clusters');
  });

  it('throws on backend error envelope', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ success: false, data: null, error: 'graph not initialized' }),
        { status: 400 },
      ),
    );
    await expect(fetchLayout3D()).rejects.toThrow('graph not initialized');
  });
});
