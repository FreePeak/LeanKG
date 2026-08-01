import type { GraphNode } from '../lib/types';

/**
 * FR-E03 — node detail panel. Shows element info resolved from /api/graph/data
 * for the selected node id. Falls back to the raw id when metadata is missing.
 */
export default function DetailPanel({
  node,
  degree,
  onClose,
}: {
  node: GraphNode | null;
  degree: number;
  onClose: () => void;
}) {
  if (!node) {
    return (
      <aside className="detail-panel" data-testid="detail-panel">
        <div className="detail-empty">
          Select a node to inspect element details.
        </div>
      </aside>
    );
  }
  return (
    <aside className="detail-panel" data-testid="detail-panel">
      <div className="detail-header">
        <span className="detail-title" data-testid="detail-title">
          {node.properties.name || node.id}
        </span>
        <button className="detail-close" onClick={onClose} aria-label="Close detail">
          ×
        </button>
      </div>
      <dl className="detail-body">
        <dt>Element</dt>
        <dd>{node.properties.elementType || '—'}</dd>
        <dt>File</dt>
        <dd className="mono">{node.properties.filePath || '—'}</dd>
        <dt>Connections</dt>
        <dd>{degree}</dd>
        <dt>ID</dt>
        <dd className="mono">{node.id}</dd>
      </dl>
    </aside>
  );
}
