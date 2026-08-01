import { useEffect, useMemo, useState } from 'react';
import { fetchFileSnippet } from '../lib/api';
import type { GraphNode } from '../lib/types';

/**
 * FR-E03 + FR-E30 — node detail panel: qualified name, element type, file
 * path, relationship counts by type, and a code context snippet fetched from
 * /api/file (falls back gracefully when the file is not readable).
 */
export interface RelationshipCounts {
  total: number;
  byType: Record<string, number>;
}

export function countRelationships(
  relationships: { sourceId: string; targetId: string; type: string }[],
  nodeId: string,
): RelationshipCounts {
  const byType: Record<string, number> = {};
  let total = 0;
  for (const r of relationships) {
    if (r.sourceId === nodeId || r.targetId === nodeId) {
      total += 1;
      byType[r.type] = (byType[r.type] ?? 0) + 1;
    }
  }
  return { total, byType };
}

export default function DetailPanel({
  node,
  relationships,
  onClose,
}: {
  node: GraphNode | null;
  relationships: { sourceId: string; targetId: string; type: string }[];
  onClose: () => void;
}) {
  const [snippet, setSnippet] = useState<string | null>(null);
  const [snippetState, setSnippetState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle');

  const counts = useMemo(
    () => (node ? countRelationships(relationships, node.id) : { total: 0, byType: {} }),
    [node, relationships],
  );

  useEffect(() => {
    setSnippet(null);
    setSnippetState('idle');
    if (!node || !node.properties.filePath) return;
    let cancelled = false;
    setSnippetState('loading');
    fetchFileSnippet(node.properties.filePath)
      .then((text) => {
        if (!cancelled) {
          setSnippet(text);
          setSnippetState('ready');
        }
      })
      .catch(() => {
        if (!cancelled) setSnippetState('error');
      });
    return () => {
      cancelled = true;
    };
  }, [node]);

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
        <dd>{counts.total}</dd>
        <dt>ID</dt>
        <dd className="mono">{node.id}</dd>
      </dl>
      {Object.keys(counts.byType).length > 0 && (
        <div className="detail-rels">
          <h4>By type</h4>
          <ul>
            {Object.entries(counts.byType)
              .sort((a, b) => b[1] - a[1])
              .map(([type, count]) => (
                <li key={type}>
                  <span className="mono">{type}</span>
                  <span className="breakdown-count">{count}</span>
                </li>
              ))}
          </ul>
        </div>
      )}
      {snippetState === 'loading' && <p className="panel-muted">Loading source…</p>}
      {snippetState === 'ready' && snippet && (
        <pre className="detail-snippet">{snippet}</pre>
      )}
      {snippetState === 'error' && (
        <p className="panel-muted">Source not readable.</p>
      )}
    </aside>
  );
}
