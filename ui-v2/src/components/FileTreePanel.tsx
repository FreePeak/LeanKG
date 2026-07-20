import { ChevronLeft, ChevronRight } from 'lucide-react';
import { NODE_COLORS, EDGE_STYLES, DEFAULT_NODE_TYPE_ORDER } from '../lib/constants';
import type { GraphNode } from '../core/graph/types';

interface FileTreePanelProps {
  collapsed: boolean;
  onToggle: () => void;
  nodes: GraphNode[];
  allNodeTypes: string[];
  visibleLabels: string[];
  visibleEdges: string[];
  depthFilter: number;
  onToggleLabel: (label: string) => void;
  onToggleEdge: (edge: string) => void;
  onDepth: (d: number) => void;
  onResetFilters: () => void;
  onSelectNode: (id: string) => void;
  selectedId: string | null;
}

function buildTree(nodes: GraphNode[]): { path: string; name: string; id: string }[] {
  return nodes
    .filter((n) => {
      const t = (n.properties.elementType || n.label || '').toLowerCase();
      return t === 'file' || n.label === 'File';
    })
    .map((n) => ({
      id: n.id,
      path: String(n.properties.filePath || n.id),
      name: String(n.properties.name || n.id),
    }))
    .sort((a, b) => a.path.localeCompare(b.path))
    .slice(0, 500);
}

export function FileTreePanel(props: FileTreePanelProps) {
  const {
    collapsed,
    onToggle,
    nodes,
    visibleLabels,
    visibleEdges,
    depthFilter,
    onToggleLabel,
    onToggleEdge,
    onDepth,
    onResetFilters,
    onSelectNode,
    selectedId,
  } = props;

  const files = buildTree(nodes);
  const edgeKeys = Object.keys(EDGE_STYLES);

  if (collapsed) {
    return (
      <aside className="w-12 shrink-0 border-r border-border-subtle bg-deep flex flex-col items-center py-2">
        <button
          type="button"
          data-testid="expand-left-panel"
          onClick={onToggle}
          className="p-2 text-text-muted hover:text-text-primary"
          aria-label="Expand panel"
        >
          <ChevronRight className="w-4 h-4" />
        </button>
      </aside>
    );
  }

  return (
    <aside
      data-testid="file-tree-panel"
      className="w-64 shrink-0 border-r border-border-subtle bg-deep flex flex-col"
    >
      <div className="h-9 px-2 flex items-center justify-between border-b border-border-subtle">
        <span className="text-xs font-medium text-text-secondary uppercase tracking-wider">
          Explore
        </span>
        <button type="button" onClick={onToggle} className="p-1 text-text-muted hover:text-text-primary">
          <ChevronLeft className="w-4 h-4" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto scrollbar-thin p-2 space-y-4">
        <section>
          <div className="flex items-center justify-between mb-1">
            <h3 className="text-[11px] uppercase text-text-muted">Node types</h3>
            <button
              type="button"
              data-testid="reset-filters"
              onClick={onResetFilters}
              className="text-[10px] text-accent hover:underline"
            >
              Reset
            </button>
          </div>
          <div className="space-y-0.5" data-testid="node-type-filters">
            {DEFAULT_NODE_TYPE_ORDER.map((label) => (
              <label
                key={label}
                className="flex items-center gap-2 text-xs text-text-secondary cursor-pointer hover:text-text-primary"
              >
                <input
                  type="checkbox"
                  checked={visibleLabels.includes(label)}
                  onChange={() => onToggleLabel(label)}
                />
                <span
                  className="w-2 h-2 rounded-full"
                  style={{ background: NODE_COLORS[label] || '#888' }}
                />
                {label}
              </label>
            ))}
          </div>
        </section>

        <section>
          <h3 className="text-[11px] uppercase text-text-muted mb-1">Edges</h3>
          <div className="space-y-0.5" data-testid="edge-type-filters">
            {edgeKeys.map((edge) => (
              <label
                key={edge}
                className="flex items-center gap-2 text-xs text-text-secondary cursor-pointer"
              >
                <input
                  type="checkbox"
                  checked={visibleEdges.includes(edge)}
                  onChange={() => onToggleEdge(edge)}
                />
                {edge}
              </label>
            ))}
          </div>
        </section>

        <section>
          <h3 className="text-[11px] uppercase text-text-muted mb-1">Focus depth</h3>
          <input
            data-testid="depth-filter"
            type="range"
            min={0}
            max={5}
            value={depthFilter}
            onChange={(e) => onDepth(Number(e.target.value))}
            className="w-full"
          />
          <div className="text-[10px] text-text-muted">{depthFilter} hops</div>
        </section>

        <section>
          <h3 className="text-[11px] uppercase text-text-muted mb-1">Files</h3>
          <ul className="space-y-0.5 max-h-64 overflow-y-auto" data-testid="file-tree-list">
            {files.map((f) => (
              <li key={f.id}>
                <button
                  type="button"
                  onClick={() => onSelectNode(f.id)}
                  className={`w-full text-left text-[11px] truncate px-1 py-0.5 rounded ${
                    selectedId === f.id
                      ? 'bg-accent/30 text-text-primary'
                      : 'text-text-secondary hover:bg-hover'
                  }`}
                  title={f.path}
                >
                  {f.name}
                </button>
              </li>
            ))}
            {files.length === 0 && (
              <li className="text-[11px] text-text-muted">No file nodes loaded</li>
            )}
          </ul>
        </section>
      </div>
    </aside>
  );
}
