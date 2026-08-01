/**
 * FR-UI2-10 / US-UI2-08 — cluster legend lib + hook.
 */
import React from 'react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { clusterPathOf, clusterColorOf, loadClusterGraph } from '../../src/lib/clusters';
import { useClusterLegend } from '../../src/hooks/useClusterLegend';
import { ClusterLegend } from '../../src/components/ClusterLegend';

const fetchClusters = vi.fn();
vi.mock('../../src/services/backend-client', () => ({
  fetchClusters: (...args: unknown[]) => fetchClusters(...args),
}));

const CLUSTER_PAYLOAD = {
  nodes: [
    {
      id: 'cluster:src/core',
      label: 'core (3)',
      properties: { name: 'src/core', filePath: 'src/core', elementType: 'Cluster[3 files]' },
    },
    {
      id: 'cluster:src/web',
      label: 'web (2)',
      properties: { name: 'src/web', filePath: 'src/web', elementType: 'Cluster[2 files]' },
    },
  ],
  relationships: [],
};

describe('cluster lib (FR-UI2-10)', () => {
  it('clusterPathOf maps node filePath to dir path, root to empty', () => {
    expect(clusterPathOf({ id: 'cluster:x', label: 'x', properties: { name: 'x', filePath: 'src/core' } })).toBe('src/core');
    expect(clusterPathOf({ id: 'cluster:root', label: 'root', properties: { name: 'root', filePath: 'root' } })).toBe('');
  });

  it('clusterColorOf is stable per id', () => {
    expect(clusterColorOf('cluster:src/core')).toBe(clusterColorOf('cluster:src/core'));
  });

  it('loadClusterGraph normalizes the /api/graph/clusters payload', async () => {
    fetchClusters.mockResolvedValue(CLUSTER_PAYLOAD);
    const graph = await loadClusterGraph();
    expect(graph.nodeCount).toBe(2);
    expect(graph.nodes[0].id).toBe('cluster:src/core');
    expect(graph.nodes[0].properties.filePath).toBe('src/core');
    expect(graph.nodes[0].properties.elementType).toMatch(/^Cluster\[/);
    expect(fetchClusters).toHaveBeenCalledOnce();
  });
});

describe('useClusterLegend + ClusterLegend', () => {
  beforeEach(() => {
    fetchClusters.mockReset();
    fetchClusters.mockResolvedValue(CLUSTER_PAYLOAD);
  });

  it('loads clusters on mount and renders legend rows', async () => {
    function Harness() {
      const legend = useClusterLegend();
      return <ClusterLegend legend={legend} />;
    }
    render(<Harness />);
    await waitFor(() => {
      expect(screen.getByTestId('cluster-legend-summary').textContent).toBe('2/2 clusters visible');
    });
    expect(screen.getByTestId('cluster-row-cluster:src/core')).toBeTruthy();
    expect(screen.getByTestId('cluster-row-cluster:src/web')).toBeTruthy();
  });

  it('toggling a cluster row updates the visible set', async () => {
    function Harness() {
      const legend = useClusterLegend();
      return <ClusterLegend legend={legend} />;
    }
    render(<Harness />);
    await waitFor(() => {
      expect(screen.getByTestId('cluster-legend-summary').textContent).toBe('2/2 clusters visible');
    });
    fireEvent.click(screen.getByTestId('cluster-row-cluster:src/core'));
    await waitFor(() => {
      expect(screen.getByTestId('cluster-legend-summary').textContent).toBe('1/2 clusters visible');
    });
    fireEvent.click(screen.getByTestId('cluster-row-cluster:src/web'));
    await waitFor(() => {
      expect(screen.getByTestId('cluster-legend-summary').textContent).toBe('0/2 clusters visible');
    });
  });

  it('show all / hide all set the visible set', async () => {
    function Harness() {
      const legend = useClusterLegend();
      return <ClusterLegend legend={legend} />;
    }
    render(<Harness />);
    await waitFor(() => {
      expect(screen.getByTestId('cluster-legend-summary').textContent).toBe('2/2 clusters visible');
    });
    fireEvent.click(screen.getByTestId('cluster-legend-hide-all'));
    await waitFor(() => {
      expect(screen.getByTestId('cluster-legend-summary').textContent).toBe('0/2 clusters visible');
    });
    fireEvent.click(screen.getByTestId('cluster-legend-show-all'));
    await waitFor(() => {
      expect(screen.getByTestId('cluster-legend-summary').textContent).toBe('2/2 clusters visible');
    });
  });

  it('renders error state when fetch fails', async () => {
    fetchClusters.mockRejectedValue(new Error('backend down'));
    function Harness() {
      const legend = useClusterLegend();
      return <ClusterLegend legend={legend} />;
    }
    render(<Harness />);
    await waitFor(() => {
      expect(screen.getByTestId('cluster-legend-error').textContent).toMatch(/backend down/);
    });
  });
});
