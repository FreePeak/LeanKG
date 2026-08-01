import { useCallback, useEffect, useMemo, useState } from 'react';
import { fetchClusters, fetchGraphData, fetchLayout3D } from './lib/api';
import type { GraphData, GraphNode, Layout3DResponse } from './lib/types';
import GraphScene from './scene/GraphScene';
import ClusterLegend from './components/ClusterLegend';
import DetailPanel from './components/DetailPanel';

type Status = 'idle' | 'loading' | 'ready' | 'error';

/**
 * FR-E01..E05 — 3D graph explorer.
 * FR-E05: layout only fetched on demand (Load button); /api/graph/data +
 * /api/graph/clusters load eagerly for the legend and detail panel.
 */
export default function GraphExplorer() {
  const [layout, setLayout] = useState<Layout3DResponse | null>(null);
  const [layoutStatus, setLayoutStatus] = useState<Status>('idle');
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [graphError, setGraphError] = useState<string | null>(null);
  const [clusters, setClusters] = useState<GraphData | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    Promise.all([fetchGraphData(), fetchClusters()])
      .then(([g, c]) => {
        if (!cancelled) {
          setGraph(g);
          setClusters(c);
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) setGraphError(String(e instanceof Error ? e.message : e));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const loadLayout = useCallback(async () => {
    setLayoutStatus('loading');
    setError(null);
    try {
      const l = await fetchLayout3D();
      setLayout(l);
      setLayoutStatus('ready');
    } catch (e: unknown) {
      setLayoutStatus('error');
      setError(String(e instanceof Error ? e.message : e));
    }
  }, []);

  const closeDetail = useCallback(() => setSelectedId(null), []);

  const selectedNode = useMemo<GraphNode | null>(
    () => graph?.nodes.find((n) => n.id === selectedId) ?? null,
    [graph, selectedId],
  );
  const selectedDegree = useMemo(
    () =>
      graph?.relationships.filter(
        (r) => r.sourceId === selectedId || r.targetId === selectedId,
      ).length ?? 0,
    [graph, selectedId],
  );

  const edges = useMemo(
    () => (graph?.relationships ?? []).map((r) => [r.sourceId, r.targetId] as [string, string]),
    [graph],
  );

  const clusterNodes = clusters?.nodes ?? [];

  return (
    <div className="explorer">
      <header className="topbar">
        <h1>LeanKG 3D Graph Explorer</h1>
        <div className="topbar-actions">
          <span className="counts">
            {layout?.nodes.length ?? 0} nodes / {edges.length} edges
          </span>
          <button
            onClick={loadLayout}
            disabled={layoutStatus === 'loading' || layoutStatus === 'idle'}
          >
            {layoutStatus === 'loading' ? 'Computing layout…' : 'Recompute layout'}
          </button>
        </div>
      </header>
      {graphError && <div className="banner error">Graph data unavailable: {graphError}</div>}
      {error && <div className="banner error">{error}</div>}
      <div className="stage">
        {layoutStatus === 'idle' && (
          <div className="empty-state">
            <p>3D layout is computed on demand.</p>
            <button className="primary" onClick={loadLayout}>Load 3D layout</button>
          </div>
        )}
        {layout && (
          <GraphScene
            layoutNodes={layout.nodes}
            nodes={graph?.nodes ?? []}
            edges={edges}
            selectedId={selectedId}
            onSelect={setSelectedId}
          />
        )}
        <ClusterLegend nodes={clusterNodes} />
        <DetailPanel node={selectedNode} degree={selectedDegree} onClose={closeDetail} />
      </div>
    </div>
  );
}
