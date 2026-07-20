import { useEffect, useMemo, useState } from 'react';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { X } from 'lucide-react';
import { readFile } from '../services/backend-client';
import type { GraphNode } from '../core/graph/types';
import { isContainerNode, isContentBearingNode, nodeElementType } from '../lib/node-kinds';

interface CodePanelProps {
  node: GraphNode | null;
  onClose: () => void;
}

function langFromPath(path: string): string {
  if (path.endsWith('.rs')) return 'rust';
  if (path.endsWith('.ts') || path.endsWith('.tsx')) return 'typescript';
  if (path.endsWith('.js') || path.endsWith('.jsx')) return 'javascript';
  if (path.endsWith('.py')) return 'python';
  if (path.endsWith('.go')) return 'go';
  if (path.endsWith('.java')) return 'java';
  return 'text';
}

export function CodePanel({ node, onClose }: CodePanelProps) {
  const [content, setContent] = useState<string>('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const filePath = useMemo(
    () => (node ? String(node.properties.filePath || '') : ''),
    [node],
  );
  const container = node ? isContainerNode(node) : false;
  const contentBearing = node ? isContentBearingNode(node) : false;

  useEffect(() => {
    if (!node || !filePath || !contentBearing) {
      setContent('');
      setError(null);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    readFile(filePath)
      .then((text) => {
        if (!cancelled) setContent(text);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [node, filePath, contentBearing]);

  if (!node) return null;

  const typeLabel = nodeElementType(node) || node.label || 'node';

  return (
    <div
      data-testid="code-panel"
      className="absolute left-0 top-0 bottom-0 w-[min(480px,45%)] z-10 border-r border-border-subtle bg-surface/95 backdrop-blur flex flex-col shadow-glow-soft"
    >
      <div className="h-9 px-3 flex items-center justify-between border-b border-border-subtle">
        <div className="min-w-0">
          <div className="text-xs font-medium text-text-primary truncate">
            {String(node.properties.name || node.label)}
          </div>
          <div className="text-[10px] text-text-muted truncate">{filePath || node.id}</div>
        </div>
        <button
          type="button"
          data-testid="close-code-panel"
          onClick={onClose}
          className="p-1 text-text-muted hover:text-text-primary"
        >
          <X className="w-4 h-4" />
        </button>
      </div>
      <div className="flex-1 overflow-auto text-xs">
        {container && (
          <div className="p-3 space-y-2 text-text-secondary" data-testid="code-panel-container-hint">
            <p>
              <span className="text-text-muted uppercase tracking-wider text-[10px]">{typeLabel}</span>
            </p>
            <p>
              This is a folder/service root, not a source file. Double-click the node to replace the
              graph with its subgraph.
            </p>
            {filePath ? (
              <p className="text-text-muted font-mono break-all text-[10px]">{filePath}</p>
            ) : null}
          </div>
        )}
        {!container && !contentBearing && (
          <p className="p-3 text-text-muted">No source preview for this node type.</p>
        )}
        {contentBearing && loading && <p className="p-3 text-text-muted">Loading…</p>}
        {contentBearing && error && <p className="p-3 text-red-400">{error}</p>}
        {contentBearing && !loading && !error && content && (
          <SyntaxHighlighter
            language={langFromPath(filePath)}
            style={vscDarkPlus}
            customStyle={{ margin: 0, background: 'transparent', fontSize: 11 }}
            showLineNumbers
          >
            {content}
          </SyntaxHighlighter>
        )}
      </div>
    </div>
  );
}
