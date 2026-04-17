import { useEffect, useState, useCallback, useMemo, useRef } from 'react';
import { GraphViewer } from './components/GraphViewer';
import { FileDetailPanel } from './components/FileDetailPanel';
import { Database, Search, ChevronRight, Home, Loader2 } from 'lucide-react';
import { useGraphFilters } from './hooks/useGraphFilters';
import { EDGE_STYLES, DEFAULT_NODE_TYPE_ORDER, NODE_COLORS } from './lib/constants';
import type { KGNode, KGEdge } from './lib/graph-adapter';

interface BreadcrumbItem {
  label: string;
  path: string;
}

function App() {
  const [data, setData] = useState<{ nodes: KGNode[]; relationships: KGEdge[] } | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [breadcrumbs, setBreadcrumbs] = useState<BreadcrumbItem[]>([{ label: 'Root', path: '' }]);
  const [selectedFileId, setSelectedFileId] = useState<string | null>(null);

  const {
    visibleEdgeTypes,
    toggleEdgeVisibility,
    depthFilter,
    setDepthFilter,
    visibleLabels,
    toggleLabelVisibility,
    effectiveLabels,
    resetToStructuralDefaults,
  } = useGraphFilters();
  const [searchTerm, setSearchTerm] = useState('');
  const initialLoadRef = useRef(false);

  const discoveredNodeTypes = useMemo(() => {
    return DEFAULT_NODE_TYPE_ORDER;
  }, []);

  const discoveredEdgeTypes = useMemo(() => {
    if (!data) return Object.keys(EDGE_STYLES);
    const types = new Set<string>();
    data.relationships.forEach(e => {
      const t = ((e.type || e.rel_type || '') as string).toUpperCase();
      if (t) types.add(t);
    });
    const ordered = Object.keys(EDGE_STYLES).filter(t => types.has(t));
    const rest = Array.from(types).filter(t => !(t in EDGE_STYLES)).sort();
    return [...ordered, ...rest];
  }, [data]);

  const loadChildren = useCallback(async (parent: string): Promise<boolean> => {
    setLoading(true);
    setError(null);
    try {
      if (!parent || parent === '') {
        const topoRes = await fetch('/api/graph/service-topology');
        const topoJson = await topoRes.json();
        if (topoJson.success && topoJson.data && topoJson.data.nodes && topoJson.data.nodes.length > 1) {
          setData({
            nodes: topoJson.data.nodes,
            relationships: topoJson.data.relationships || [],
          });
          setLoading(false);
          return true;
        }
      }

      const encodedParent = encodeURIComponent(parent);
      const res = await fetch(`/api/graph/children?parent=${encodedParent}`);
      const json = await res.json();

      if (json.success && json.data) {
        const nodes = json.data.nodes.map((n: KGNode) => ({
          id: n.id,
          label: n.label,
          properties: n.properties,
        }));
        setData({
          nodes,
          relationships: json.data.relationships || [],
        });
        return nodes.length > 0;
      } else {
        setError(json.error || 'Failed to load children');
        return false;
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.toString() : String(err));
      return false;
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    // Guard against Strict Mode double-invocation
    if (initialLoadRef.current) return;
    initialLoadRef.current = true;

    // Multi-repo: check service-topology first, if services found show them as root nodes
    // Single-repo: fallback to expandService with all=true to load all nodes (including functions)
    (async () => {
      setLoading(true);
      try {
        const topoRes = await fetch('/api/graph/service-topology');
        const topoJson = await topoRes.json();
        if (topoJson.success && topoJson.data && topoJson.data.nodes && topoJson.data.nodes.length > 1) {
          // Multi-project: show services as root nodes
          setData({
            nodes: topoJson.data.nodes,
            relationships: topoJson.data.relationships || [],
          });
          setLoading(false);
          return;
        }
      } catch {
        // service-topology not available, fall through to single-repo mode
      }
      // Single-repo: load all nodes including functions
      expandService('', 'Root', true);
    })();
  }, []);

  const handleNodeClick = useCallback(async (nodeId: string) => {
    const node = data?.nodes.find(n => n.id === nodeId);
    if (!node) return false;

    const elementType = (node.properties?.elementType as string)?.toLowerCase() || '';

    if (elementType === 'service' || nodeId.startsWith('service:')) {
      return false;
    }
    if (elementType === 'folder' || elementType === 'directory' || nodeId.startsWith('folder:')) {
      return false;
    }
    return true;
  }, [data]);

  const expandService = useCallback(async (servicePath: string, _label: string, loadAll: boolean = false) => {
    setLoading(true);
    setError(null);
    try {
      const encodedPath = encodeURIComponent(servicePath);
      const allParam = loadAll ? '&all=true' : '';
      const res = await fetch(`/api/graph/expand-service?path=${encodedPath}${allParam}`);
      const json = await res.json();

      if (json.success && json.data) {
        setData({
          nodes: json.data.nodes || [],
          relationships: json.data.relationships || [],
        });
        setLoading(false);
        return true;
      }
      setLoading(false);
      return false;
    } catch (err: unknown) {
      setError(err instanceof Error ? err.toString() : String(err));
      setLoading(false);
      return false;
    }
  }, []);

  const handleNodeDoubleClick = useCallback(async (nodeId: string) => {
    const node = data?.nodes.find(n => n.id === nodeId);
    if (!node) return;

    const elementType = (node.properties?.elementType as string)?.toLowerCase() || '';

    if (elementType === 'service' || nodeId.startsWith('service:')) {
      const servicePath = (node.properties?.filePath as string) || '';
      if (servicePath) {
        const label = (node.properties?.name as string) || node.label || nodeId;
        setBreadcrumbs([{ label: 'Root', path: '' }, { label, path: servicePath }]);
        await expandService(servicePath, label, true);  // loadAll=true to load all content
        resetToStructuralDefaults();
        setSelectedFileId(null);
      }
    } else if (elementType === 'folder' || elementType === 'directory' || nodeId.startsWith('folder:')) {
      const currentBreadcrumb = breadcrumbs[breadcrumbs.length - 1];
      const currentPath = currentBreadcrumb.path;
      const nodePath = (node.properties?.filePath as string) || nodeId.replace('folder:', '');

      let newPath: string;
      if (nodeId === '.' || nodeId === './') {
        newPath = '';
      } else {
        const normalizedNodePath = nodePath.startsWith('./') ? nodePath.slice(2) : nodePath;
        const normalizedCurrentPath = currentPath === './' ? '' : currentPath;
        newPath = normalizedCurrentPath ? `${normalizedCurrentPath}/${normalizedNodePath}` : normalizedNodePath;
      }

      const existingIndex = breadcrumbs.findIndex(b => b.path === newPath);
      if (existingIndex >= 0) {
        setBreadcrumbs(prev => prev.slice(0, existingIndex + 1));
      } else {
        const label = (node.properties?.name as string) || node.label || nodeId;
        setBreadcrumbs(prev => [...prev, { label, path: newPath }]);
      }

      // In single-repo, expand folder to show ALL content (functions, classes, etc.)
      // Use expandService with all=true to load all nested content
      const folderPath = newPath === '' ? '' : './' + newPath;
      const label = (node.properties?.name as string) || node.label || (newPath === '' ? 'Root' : nodeId);
      await expandService(folderPath, label, true);
      resetToStructuralDefaults();
      setSelectedFileId(null);
    } else if (elementType === 'file' || elementType === 'document' || elementType === 'config_file') {
      setSelectedFileId(nodeId);
    } else if (elementType === 'function' || elementType === 'method' || elementType === 'class') {
      setSelectedFileId(nodeId);
    }
  }, [data, breadcrumbs, loadChildren, expandService, resetToStructuralDefaults]);

  const handleBreadcrumbClick = useCallback(async (index: number) => {
    const crumb = breadcrumbs[index];
    setBreadcrumbs(prev => prev.slice(0, index + 1));
    setSelectedFileId(null);
    await loadChildren(crumb.path);
  }, [breadcrumbs, loadChildren]);

  const handleCloseFileDetail = useCallback(() => {
    setSelectedFileId(null);
  }, []);

  const handleNavigateToFile = useCallback((fileId: string) => {
    const node = data?.nodes.find(n => n.id === fileId);
    if (node && ((node.properties?.elementType as string)?.toLowerCase() === 'file')) {
      setSelectedFileId(fileId);
    }
  }, [data]);

  return (
    <div className="flex h-screen w-full bg-[var(--color-background)] text-[var(--color-text)] overflow-hidden">
      <aside className="w-64 flex-shrink-0 border-r border-slate-800 bg-[#0A0F24] p-6 flex flex-col gap-6 relative z-10 shadow-[rgba(0,0,0,0.5)_4px_0_24px_-4px] overflow-y-auto">
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg bg-blue-600 flex items-center justify-center shadow-[0_0_20px_rgba(37,99,235,0.4)]">
            <Database className="w-6 h-6 text-white" />
          </div>
          <h1 className="text-2xl font-bold bg-clip-text text-transparent bg-gradient-to-r from-blue-400 to-amber-500">
            LeanKG
          </h1>
        </div>

        <nav className="flex flex-col gap-2 mt-4">
          <button className="w-full flex items-center gap-3 px-4 py-3 rounded-lg transition-colors duration-200 bg-blue-600/10 text-blue-400">
            <Database className="w-5 h-5" />
            <span className="font-medium">Explorer</span>
          </button>
        </nav>

        {/* Breadcrumb Navigation */}
        <div className="flex flex-col gap-1 mt-2">
          <div className="flex items-center gap-1 text-xs text-slate-400 uppercase tracking-wider mb-1 px-2">
            <Home className="h-3 w-3" />
            <span>Navigation</span>
          </div>
          <div className="flex flex-wrap gap-1">
            {breadcrumbs.map((crumb, index) => (
              <div key={index} className="flex items-center">
                {index > 0 && <ChevronRight className="h-3 w-3 text-slate-600 mx-0.5" />}
                <button
                  onClick={() => handleBreadcrumbClick(index)}
                  className={`px-2 py-1 text-xs rounded transition-colors ${
                    index === breadcrumbs.length - 1
                      ? 'bg-blue-600/20 text-blue-400'
                      : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800'
                  }`}
                >
                  {crumb.label}
                </button>
              </div>
            ))}
          </div>
        </div>

        {/* Search */}
        <div className="relative">
          <Search className="absolute left-2.5 top-2 h-4 w-4 text-slate-400" />
          <input
            type="text"
            placeholder="Search node..."
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
            className="w-full bg-slate-900/50 border border-slate-700 text-slate-200 text-sm rounded-lg pl-9 pr-3 py-1.5 focus:outline-none focus:border-cyan-500 transition-colors"
          />
        </div>

        {/* Node Types */}
        <div className="p-4 rounded-xl bg-slate-800/50 border border-slate-700/50">
          <div className="flex items-center gap-2 mb-3 text-slate-300 font-medium text-xs uppercase tracking-wider">
            <Database className="h-4 w-4 text-slate-400" />
            Node Types
          </div>
          <div className="flex flex-col gap-2">
            {discoveredNodeTypes.map((type) => {
              const isActive = visibleLabels.includes(type);
              const color = NODE_COLORS[type] || '#666';
              return (
                <button
                  key={type}
                  onClick={() => toggleLabelVisibility(type)}
                  className={`w-full px-2 py-1.5 flex items-center gap-3 rounded-md border text-xs transition-colors ${
                    isActive
                      ? 'bg-slate-800 border-slate-600 text-slate-200'
                      : 'bg-transparent border-slate-800/80 text-slate-500'
                  }`}
                >
                  <div className="w-2.5 h-2.5 rounded-full" style={{ backgroundColor: color }}></div>
                  {type}
                </button>
              );
            })}
          </div>
        </div>

        {/* Edge Types */}
        <div className="p-4 rounded-xl bg-slate-800/50 border border-slate-700/50">
          <div className="flex items-center gap-2 mb-3 text-slate-300 font-medium text-xs uppercase tracking-wider">
            <span className="h-4 w-4 text-slate-400">→</span>
            Edge Types
          </div>
          <div className="flex flex-col gap-2">
            {discoveredEdgeTypes.map((type) => {
              const isActive =
                visibleEdgeTypes.length === 0 || visibleEdgeTypes.includes(type);
              const style = EDGE_STYLES[type] || { color: '#4a4a5a' };
              return (
                <button
                  key={type}
                  onClick={() => toggleEdgeVisibility(type)}
                  className={`w-full px-2 py-1.5 flex items-center gap-3 rounded-md border text-xs transition-colors ${
                    isActive
                      ? 'bg-slate-800 border-slate-600 text-slate-200'
                      : 'bg-transparent border-slate-800/80 text-slate-500'
                  }`}
                >
                  <div className="w-2.5 h-2.5 rounded-full" style={{ backgroundColor: style.color }}></div>
                  {type}
                </button>
              );
            })}
          </div>
        </div>

        {/* Focus Depth */}
        <div className="p-4 rounded-xl bg-slate-800/50 border border-slate-700/50">
          <div className="flex items-center gap-2 mb-2 text-slate-300 font-medium text-xs uppercase tracking-wider">
            <span className="h-4 w-4 text-slate-400">◉</span>
            Focus Depth
          </div>
          <p className="mb-3 text-[11px] text-slate-500">
            Show nodes within N hops of selection
          </p>
          <div className="flex flex-wrap gap-1.5">
            {[
              { value: null, label: 'All' },
              { value: 1, label: '1 hop' },
              { value: 2, label: '2 hops' },
              { value: 3, label: '3 hops' },
              { value: 5, label: '5 hops' },
            ].map(({ value, label }) => (
              <button
                key={label}
                onClick={() => setDepthFilter(value)}
                className={`rounded px-2 py-1 border text-xs transition-colors ${
                  depthFilter === value
                    ? 'bg-blue-600 border-blue-500 text-white'
                    : 'bg-transparent border-slate-700 text-slate-400 hover:bg-slate-800 hover:text-slate-200'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
        </div>

        {/* Graph Stats */}
        <div className="mt-auto pt-4">
          <div className="p-4 rounded-xl bg-slate-800/50 border border-slate-700/50">
            <h4 className="text-xs font-mono uppercase tracking-wider text-slate-500 mb-2">Graph Stats</h4>
            {data ? (
              <div className="flex flex-col gap-1">
                <div className="flex justify-between font-mono text-sm">
                  <span className="text-slate-400">Nodes</span>
                  <span className="text-blue-400 font-bold">{data.nodes.length}</span>
                </div>
                <div className="flex justify-between font-mono text-sm">
                  <span className="text-slate-400">Relationships</span>
                  <span className="text-amber-400 font-bold">{data.relationships.length}</span>
                </div>
              </div>
            ) : (
              <p className="text-xs text-slate-500">Loading...</p>
            )}
          </div>
        </div>
      </aside>

      <main className="flex-1 relative">
        {loading && (
          <div className="absolute inset-0 flex items-center justify-center bg-[var(--color-background)] z-20">
            <div className="text-center">
              <Loader2 className="w-12 h-12 border-4 border-slate-800 border-t-amber-500 rounded-full animate-spin mx-auto mb-4" />
              <p className="text-slate-400 font-mono">Loading Graph Engine...</p>
            </div>
          </div>
        )}

        {error && (
          <div className="absolute inset-0 flex items-center justify-center bg-[var(--color-background)] z-20">
            <div className="p-6 bg-red-900/20 border border-red-500/50 rounded-xl max-w-lg text-center">
              <h2 className="text-red-400 font-bold mb-2 text-xl">Connection Error</h2>
              <p className="text-slate-300 font-mono text-sm">{error}</p>
            </div>
          </div>
        )}

        {!loading && !error && data && (
          <GraphViewer
            data={data}
            loading={loading}
            error={error}
            searchTerm={searchTerm}
            visibleEdgeTypes={visibleEdgeTypes}
            depthFilter={depthFilter}
            visibleLabels={effectiveLabels}
            onNodeClick={handleNodeClick}
            onNodeDoubleClick={handleNodeDoubleClick}
          />
        )}

        {/* File Detail Panel */}
        {selectedFileId && data && (
          <FileDetailPanel
            selectedFileId={selectedFileId}
            graphData={data}
            onClose={handleCloseFileDetail}
            onNavigateToFile={handleNavigateToFile}
          />
        )}
      </main>
    </div>
  );
}

export default App;