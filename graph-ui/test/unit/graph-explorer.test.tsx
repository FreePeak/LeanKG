import React from 'react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import GraphExplorer from '../../src/GraphExplorer';
import DetailPanel from '../../src/components/DetailPanel';
import ClusterLegend from '../../src/components/ClusterLegend';
import { selectedDetailOf } from '../../src/lib/selection';
import type { GraphData, GraphNode } from '../../src/lib/types';

const LAYOUT = {
  nodes: [
    { node_id: 'src/main.rs::main', x: 1, y: 2, z: 3 },
    { node_id: 'src/lib.rs::helper', x: -1, y: 0, z: 1 },
  ],
  bounds: { min_x: -5, max_x: 5, min_y: -5, max_y: 5, min_z: -5, max_z: 5 },
};

const GRAPH: GraphData = {
  nodes: [
    {
      id: 'src/main.rs::main',
      label: 'Main',
      properties: { name: 'main', filePath: 'src/main.rs', elementType: 'Function' },
    },
    {
      id: 'src/lib.rs::helper',
      label: 'Helper',
      properties: { name: 'helper', filePath: 'src/lib.rs', elementType: 'Function' },
    },
  ],
  relationships: [
    {
      id: 'e1',
      sourceId: 'src/main.rs::main',
      targetId: 'src/lib.rs::helper',
      type: 'calls',
      confidenceLabel: 'HIGH',
    },
  ],
  filtered: null,
  hasMore: false,
};

const CLUSTERS = {
  nodes: [
    {
      id: 'cluster:src',
      label: 'src (2)',
      properties: { name: 'src', filePath: 'src', elementType: 'Cluster[2 files]' },
    },
  ],
  relationships: [],
  filtered: null,
  hasMore: false,
};

function envelope(payload: unknown, success = true) {
  return new Response(
    JSON.stringify({ success, data: success ? payload : null, error: success ? null : 'boom' }),
    { status: success ? 200 : 400 },
  );
}

describe('GraphExplorer (FR-E01..E05)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    const fetchMock = vi
      .spyOn(globalThis, 'fetch')
      // eager: /api/graph/data + /api/graph/clusters (order of Promise.all)
      .mockResolvedValueOnce(envelope(GRAPH))
      .mockResolvedValueOnce(envelope(CLUSTERS))
      .mockImplementation((url: RequestInfo | URL) => {
        if (String(url).startsWith('/api/graph/layout3d')) {
          return Promise.resolve(envelope(LAYOUT));
        }
        return Promise.resolve(new Response('{}', { status: 500 }));
      });
    void fetchMock;
  });

  const layout3dCalls = () =>
    vi.mocked(globalThis.fetch).mock.calls.filter((c) => String(c[0]).startsWith('/api/graph/layout3d'));

  it('FR-E05: layout3d is not fetched on mount; only after Load button', async () => {
    render(<GraphExplorer />);
    await waitFor(() => expect(screen.getByText(/0 nodes \/ 1 edges/)).toBeTruthy());
    expect(layout3dCalls()).toHaveLength(0);

    await userEvent.click(screen.getByRole('button', { name: 'Load 3D layout' }));
    await waitFor(() => expect(layout3dCalls()).toHaveLength(1));
    await waitFor(() => expect(screen.getByText(/2 nodes \/ 1 edges/)).toBeTruthy());
  });

  it('FR-E05: recompute button refetches layout3d', async () => {
    render(<GraphExplorer />);
    await userEvent.click(screen.getByRole('button', { name: 'Load 3D layout' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Recompute layout' })).toBeTruthy());
    await userEvent.click(screen.getByRole('button', { name: 'Recompute layout' }));
    expect(layout3dCalls()).toHaveLength(2);
  });

  it('FR-E04: cluster legend renders rows from /api/graph/clusters', async () => {
    render(<GraphExplorer />);
    await waitFor(() => expect(screen.getByTestId('cluster-legend')).toBeTruthy());
    expect(screen.getByText('src')).toBeTruthy();
    expect(screen.getByText('1')).toBeTruthy();
  });
});

describe('selection → detail (FR-E03)', () => {
  const MAIN: GraphNode = GRAPH.nodes[0];

  it('selectedDetailOf maps selected id to node + incident-edge degree', () => {
    const detail = selectedDetailOf(GRAPH, 'src/main.rs::main');
    expect(detail?.node.id).toBe('src/main.rs::main');
    expect(detail?.degree).toBe(1);

    expect(selectedDetailOf(GRAPH, null)).toBeNull();
    expect(selectedDetailOf(null, 'x')).toBeNull();
    expect(selectedDetailOf(GRAPH, 'missing')).toBeNull();
  });

  it('DetailPanel renders element info for the selected node', () => {
    render(<DetailPanel node={MAIN} degree={1} onClose={() => {}} />);
    expect(screen.getByTestId('detail-title').textContent).toBe('main');
    expect(screen.getByText('src/main.rs')).toBeTruthy();
    expect(screen.getByText('Function')).toBeTruthy();
  });

  it('DetailPanel shows empty prompt when nothing selected', () => {
    render(<DetailPanel node={null} degree={0} onClose={() => {}} />);
    expect(screen.getByText('Select a node to inspect element details.')).toBeTruthy();
  });
});
