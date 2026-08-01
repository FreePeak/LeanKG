/**
 * FR-E37 — export/share: JSON snapshot + shareable URL builders. Pure.
 */
import type { GraphData } from './types';

export interface ExportSnapshot {
  format: 'leankg-graph-ui';
  exportedAt: string;
  stats: { nodeCount: number; edgeCount: number };
  graph: GraphData | null;
}

export function buildSnapshot(graph: GraphData | null): ExportSnapshot {
  return {
    format: 'leankg-graph-ui',
    exportedAt: new Date().toISOString(),
    stats: {
      nodeCount: graph?.nodes.length ?? 0,
      edgeCount: graph?.relationships.length ?? 0,
    },
    graph,
  };
}

/** Trigger a browser download of the JSON snapshot (jsdom-safe no-op in tests). */
export function downloadSnapshot(snapshot: ExportSnapshot): void {
  const blob = new Blob([JSON.stringify(snapshot, null, 2)], {
    type: 'application/json',
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `leankg-graph-${snapshot.exportedAt.slice(0, 10)}.json`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

/** Shareable URL preserving tab + project (FR-E34/E37). */
export function buildShareUrl(
  base: string,
  project: string | undefined,
  tab: string,
): string {
  const url = new URL(base, window.location.origin);
  url.searchParams.set('tab', tab);
  if (project) url.searchParams.set('project', project);
  return url.toString();
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}
