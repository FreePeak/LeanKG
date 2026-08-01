/**
 * FR-UI2-10 / US-UI2-08 — cluster legend panel (Graphify sidebar parity).
 * Shows communities from /api/graph/clusters with per-cluster show/hide
 * toggles; the visible cluster ids flow to the canvas for node filtering.
 */
import type { ClusterLegendState } from '../hooks/useClusterLegend';
import { clusterColorOf, clusterPathOf } from '../lib/clusters';

export interface ClusterLegendProps {
  legend: ClusterLegendState;
}

export function ClusterLegend({ legend }: ClusterLegendProps) {
  const { clusters, error, loading, visibleIds, visibleCount, totalCount, toggle, showAll, hideAll, reload } = legend;

  return (
    <div className="border-t border-border-subtle pt-2 shrink-0" data-testid="cluster-legend">
      <div className="flex items-center justify-between mb-1">
        <h3 className="text-[11px] uppercase text-text-muted">Clusters</h3>
        <button
          type="button"
          data-testid="cluster-legend-reload"
          onClick={() => void reload()}
          className="text-[10px] text-accent hover:underline"
          disabled={loading}
        >
          {loading ? 'Loading…' : 'Reload'}
        </button>
      </div>
      {clusters && clusters.nodes.length > 0 && (
        <p className="text-[10px] text-text-muted mb-1" data-testid="cluster-legend-summary">
          {visibleCount}/{totalCount} clusters visible
        </p>
      )}
      <div className="flex gap-2 mb-1">
        <button
          type="button"
          data-testid="cluster-legend-show-all"
          onClick={showAll}
          className="text-[10px] text-accent hover:underline"
        >
          Show all
        </button>
        <button
          type="button"
          data-testid="cluster-legend-hide-all"
          onClick={hideAll}
          className="text-[10px] text-accent hover:underline"
        >
          Hide all
        </button>
      </div>
      {error && (
        <p className="text-[11px] text-red-400" data-testid="cluster-legend-error">
          {error}
        </p>
      )}
      {!clusters && !loading && !error && (
        <p className="text-[11px] text-text-muted" data-testid="cluster-legend-empty">
          No cluster data.
        </p>
      )}
      <ul className="space-y-0.5 max-h-56 overflow-y-auto" data-testid="cluster-legend-list">
        {clusters?.nodes.map((node) => {
          const id = node.id;
          const checked = visibleIds.has(id);
          return (
            <li key={id}>
              <label
                className="flex items-center gap-2 text-xs text-text-secondary cursor-pointer hover:text-text-primary"
                data-testid={`cluster-row-${id}`}
              >
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => toggle(id)}
                />
                <span
                  className="w-2 h-2 rounded-full shrink-0"
                  style={{ background: clusterColorOf(id) }}
                />
                <span className="truncate" title={node.label}>
                  {clusterPathOf(node) || node.label}
                </span>
              </label>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
