import { useMemo } from 'react';
import { CLUSTER_COLORS, clusterColorOf, clusterIdOf } from '../lib/color';
import type { GraphNode } from '../lib/types';

/** FR-E04 — legend of cluster colors derived from /api/graph/clusters nodes. */
export default function ClusterLegend({ nodes }: { nodes: GraphNode[] }) {
  const clusters = useMemo(() => {
    const byId = new Map<string, number>();
    for (const n of nodes) {
      // Backend cluster nodes: id = "cluster:<dir>", filePath = the dir itself.
      const id = n.id.startsWith('cluster:')
        ? n.id.slice('cluster:'.length)
        : clusterIdOf(n.properties.filePath);
      byId.set(id, (byId.get(id) ?? 0) + 1);
    }
    return [...byId.entries()]
      .sort((a, b) => b[1] - a[1])
      .map(([id, count]) => ({ id, count, color: clusterColorOf(id) }));
  }, [nodes]);

  if (clusters.length === 0) return null;
  return (
    <div className="cluster-legend" data-testid="cluster-legend">
      <span className="legend-title">Clusters</span>
      {clusters.map((c) => (
        <span key={c.id} className="legend-row">
          <span className="legend-swatch" style={{ background: c.color }} />
          <span className="legend-name">{c.id || 'root'}</span>
          <span className="legend-count">{c.count}</span>
        </span>
      ))}
      <span className="legend-total">{CLUSTER_COLORS.length} colors max</span>
    </div>
  );
}
