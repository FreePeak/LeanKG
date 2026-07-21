import { useEffect, useMemo, useState } from 'react';
import { ChevronDown, ChevronLeft, ChevronRight, File, Folder } from 'lucide-react';
import { NODE_COLORS, EDGE_STYLES, DEFAULT_NODE_TYPE_ORDER } from '../lib/constants';
import {
  buildExplorerTree,
  defaultExpandedPaths,
  type ExplorerEntry,
} from '../lib/file-tree';
import type { GraphNode } from '../core/graph/types';

interface FileTreePanelProps {
  collapsed: boolean;
  onToggle: () => void;
  nodes: GraphNode[];
  /** Active project mount — strips absolute paths in the tree. */
  projectPath?: string;
  allNodeTypes: string[];
  visibleLabels: string[];
  visibleEdges: string[];
  depthFilter: number;
  onToggleLabel: (label: string) => void;
  onToggleEdge: (edge: string) => void;
  onDepth: (d: number) => void;
  onResetFilters: () => void;
  onSelectNode: (id: string) => void;
  onOpenFolder?: (path: string, label: string) => void;
  selectedId: string | null;
}

function TreeRow(props: {
  entry: ExplorerEntry;
  depth: number;
  selectedId: string | null;
  expanded: Set<string>;
  onToggleExpand: (path: string) => void;
  onSelectNode: (id: string) => void;
  onOpenFolder?: (path: string, label: string) => void;
}) {
  const { entry, depth, selectedId, expanded, onToggleExpand, onSelectNode, onOpenFolder } =
    props;
  const isFolder = entry.kind === 'folder';
  const isOpen = expanded.has(entry.path);
  const selected = selectedId === entry.id;

  return (
    <li data-testid={isFolder ? `tree-folder-${entry.path}` : `tree-file-${entry.path}`}>
      <div
        className={`flex items-center gap-0.5 rounded ${
          selected ? 'bg-accent/30 text-text-primary' : 'text-text-secondary hover:bg-hover'
        }`}
        style={{ paddingLeft: `${depth * 10 + 2}px` }}
      >
        {isFolder ? (
          <button
            type="button"
            className="p-0.5 shrink-0 text-text-muted"
            aria-label={isOpen ? 'Collapse' : 'Expand'}
            data-testid={`tree-toggle-${entry.path}`}
            onClick={() => onToggleExpand(entry.path)}
          >
            {isOpen ? (
              <ChevronDown className="w-3 h-3" />
            ) : (
              <ChevronRight className="w-3 h-3" />
            )}
          </button>
        ) : (
          <span className="w-4 shrink-0" />
        )}
        <button
          type="button"
          className="flex-1 min-w-0 flex items-center gap-1 text-left text-[11px] truncate py-0.5 pr-1"
          title={
            isFolder
              ? `${entry.path || '.'} (double-click to open in graph)`
              : entry.path
          }
          onClick={() => {
            if (entry.id && !entry.id.startsWith('folder:') && !entry.id.startsWith('file:')) {
              onSelectNode(entry.id);
            }
            if (isFolder) onToggleExpand(entry.path);
          }}
          onDoubleClick={() => {
            if (isFolder && onOpenFolder) {
              onOpenFolder(entry.path || '.', entry.name || entry.path || '.');
            } else if (!isFolder && !entry.id.startsWith('file:')) {
              onSelectNode(entry.id);
            }
          }}
        >
          {isFolder ? (
            <Folder className="w-3 h-3 shrink-0 text-indigo-400" />
          ) : (
            <File className="w-3 h-3 shrink-0 text-blue-400" />
          )}
          <span className="truncate">{entry.name}</span>
        </button>
      </div>
      {isFolder && isOpen && entry.children.length > 0 && (
        <ul>
          {entry.children.map((child) => (
            <TreeRow
              key={`${child.kind}:${child.path}`}
              entry={child}
              depth={depth + 1}
              selectedId={selectedId}
              expanded={expanded}
              onToggleExpand={onToggleExpand}
              onSelectNode={onSelectNode}
              onOpenFolder={onOpenFolder}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

export function FileTreePanel(props: FileTreePanelProps) {
  const {
    collapsed,
    onToggle,
    nodes,
    projectPath,
    visibleLabels,
    visibleEdges,
    depthFilter,
    onToggleLabel,
    onToggleEdge,
    onDepth,
    onResetFilters,
    onSelectNode,
    onOpenFolder,
    selectedId,
  } = props;

  const tree = useMemo(
    () => buildExplorerTree(nodes, { projectRoot: projectPath }),
    [nodes, projectPath],
  );

  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(['src']));
  const [filtersOpen, setFiltersOpen] = useState(false);

  // After expand / load-more, auto-open preferred folders (src, ui-v2) + one child level.
  useEffect(() => {
    const preferred = defaultExpandedPaths(tree);
    if (preferred.length === 0) return;
    setExpanded((prev) => {
      const next = new Set(prev);
      let changed = false;
      for (const p of preferred) {
        if (!next.has(p)) {
          next.add(p);
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [tree]);

  const onToggleExpand = (path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  const edgeKeys = Object.keys(EDGE_STYLES);
  const folderCount = tree.filter((e) => e.kind === 'folder').length;
  const fileLeaves = useMemo(() => {
    let n = 0;
    const walk = (entries: ExplorerEntry[]) => {
      for (const e of entries) {
        if (e.kind === 'file') n += 1;
        else walk(e.children);
      }
    };
    walk(tree);
    return n;
  }, [tree]);

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

      <div className="flex-1 overflow-y-auto scrollbar-thin p-2 flex flex-col gap-3 min-h-0">
        {/* Folders first — primary nav (was buried under filters). */}
        <section className="flex flex-col min-h-0 flex-1">
          <h3 className="text-[11px] uppercase text-text-muted mb-0.5">Folders & files</h3>
          <p className="text-[10px] text-text-muted mb-1" data-testid="tree-summary">
            {folderCount} folders · {fileLeaves} files · dbl-click folder to open
          </p>
          <ul
            className="space-y-0 flex-1 overflow-y-auto min-h-[12rem] max-h-[calc(100vh-14rem)]"
            data-testid="file-tree-list"
          >
            {tree.map((entry) => (
              <TreeRow
                key={`${entry.kind}:${entry.path}`}
                entry={entry}
                depth={0}
                selectedId={selectedId}
                expanded={expanded}
                onToggleExpand={onToggleExpand}
                onSelectNode={onSelectNode}
                onOpenFolder={onOpenFolder}
              />
            ))}
            {tree.length === 0 && (
              <li className="text-[11px] text-text-muted" data-testid="tree-empty">
                No folder/file paths in loaded graph. Expand a service or Load more.
              </li>
            )}
          </ul>
        </section>

        <section className="border-t border-border-subtle pt-2 shrink-0">
          <button
            type="button"
            data-testid="toggle-filters"
            className="flex items-center gap-1 text-[11px] uppercase text-text-muted w-full text-left mb-1"
            onClick={() => setFiltersOpen((v) => !v)}
          >
            {filtersOpen ? (
              <ChevronDown className="w-3 h-3" />
            ) : (
              <ChevronRight className="w-3 h-3" />
            )}
            Filters
          </button>
          {filtersOpen && (
            <div className="space-y-3">
              <div>
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
              </div>

              <div>
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
              </div>

              <div>
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
              </div>
            </div>
          )}
        </section>
      </div>
    </aside>
  );
}
