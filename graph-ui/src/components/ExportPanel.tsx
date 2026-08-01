/**
 * FR-E37 — export/share: JSON snapshot download + shareable URL copy.
 */
import { useState } from 'react';
import { buildShareUrl, buildSnapshot, copyText, downloadSnapshot } from '../lib/export';
import type { GraphData } from '../lib/types';

export default function ExportPanel({
  graph,
  project,
  tab,
}: {
  graph: GraphData | null;
  project?: string;
  tab: string;
}) {
  const [copied, setCopied] = useState(false);

  const handleDownload = () => {
    downloadSnapshot(buildSnapshot(graph));
  };

  const handleCopy = async () => {
    const ok = await copyText(buildShareUrl(window.location.href, project, tab));
    setCopied(ok);
    if (ok) setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="panel" data-testid="export-panel" aria-label="Export and share">
      <h2 className="panel-title">Export / share</h2>
      <div className="export-actions">
        <button onClick={handleDownload} disabled={!graph}>
          Download JSON
        </button>
        <button onClick={handleCopy}>Copy share link</button>
      </div>
      {copied && <p className="export-note">Share link copied.</p>}
      {!graph && <p className="panel-muted">Graph not loaded yet.</p>}
    </section>
  );
}
