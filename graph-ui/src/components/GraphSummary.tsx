import { useMemo } from 'react';
import { computeStats, type GraphStats } from '../lib/stats';
import type { GraphData } from '../lib/types';

/**
 * FR-E30/E33 — graph summary/stats panel: node/edge counts and breakdowns
 * by element type and relationship type.
 */
export default function GraphSummary({ graph }: { graph: GraphData | null }) {
  const stats: GraphStats | null = useMemo(() => computeStats(graph), [graph]);
  if (!stats) {
    return (
      <section className="panel" data-testid="graph-summary" aria-label="Graph summary">
        <h2 className="panel-title">Summary</h2>
        <p className="panel-muted">No graph data loaded.</p>
      </section>
    );
  }
  return (
    <section className="panel" data-testid="graph-summary" aria-label="Graph summary">
      <h2 className="panel-title">Summary</h2>
      <dl className="summary-grid">
        <dt>Nodes</dt>
        <dd data-testid="summary-nodes">{stats.nodeCount}</dd>
        <dt>Edges</dt>
        <dd data-testid="summary-edges">{stats.edgeCount}</dd>
      </dl>
      <h3 className="panel-subtitle">By element type</h3>
      <ul className="breakdown-list">
        {Object.entries(stats.nodeTypes)
          .sort((a, b) => b[1] - a[1])
          .map(([type, count]) => (
            <li key={type}>
              <span>{type}</span>
              <span className="breakdown-count">{count}</span>
            </li>
          ))}
      </ul>
      <h3 className="panel-subtitle">By relationship type</h3>
      <ul className="breakdown-list">
        {Object.entries(stats.edgeTypes)
          .sort((a, b) => b[1] - a[1])
          .map(([type, count]) => (
            <li key={type}>
              <span className="mono">{type}</span>
              <span className="breakdown-count">{count}</span>
            </li>
          ))}
      </ul>
    </section>
  );
}
