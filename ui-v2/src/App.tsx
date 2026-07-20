import { useCallback, useEffect, useMemo, useState } from 'react';
import type Graph from 'graphology';
import { Header, LoadingOverlay, StatusBar } from './components/Chrome';
import { FileTreePanel } from './components/FileTreePanel';
import { GraphCanvas } from './components/GraphCanvas';
import { CodePanel } from './components/CodePanel';
import { useGraphFilters, useUrlProject } from './hooks/useGraphFilters';
import {
  buildLayoutGraph,
  type LayoutMode,
  type SigmaNodeAttributes,
  type SigmaEdgeAttributes,
} from './lib/graph-adapter';
import {
  decideSkipGraph,
  parseSkipGraphParam,
  shouldConfirmGraphLoad,
} from './lib/graph-load-decision';
import {
  LARGE_GRAPH_EDGE_THRESHOLD,
  LARGE_GRAPH_NODE_THRESHOLD,
} from './lib/constants';
import type { KnowledgeGraph, GraphNode } from './core/graph/types';
import {
  expandService,
  fetchIndexStatus,
  fetchServiceTopology,
  probeBackend,
  searchCode,
  switchProject,
} from './services/backend-client';

type ViewMode = 'onboarding' | 'loading' | 'exploring' | 'overview';

export default function App() {
  const [viewMode, setViewMode] = useState<ViewMode>('loading');
  const [connected, setConnected] = useState(false);
  const [kg, setKg] = useState<KnowledgeGraph | null>(null);
  const [layoutMode, setLayoutMode] = useState<LayoutMode>('force');
  const [sigmaGraph, setSigmaGraph] = useState<Graph<
    SigmaNodeAttributes,
    SigmaEdgeAttributes
  > | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [leftCollapsed, setLeftCollapsed] = useState(false);
  const [searchTerm, setSearchTerm] = useState('');
  const [highlightIds, setHighlightIds] = useState<Set<string>>(new Set());
  const [statusText, setStatusText] = useState('');
  const [indexLine, setIndexLine] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [skipInfo, setSkipInfo] = useState<{ nodes: number; edges: number } | null>(null);
  const [project, setProject] = useUrlProject();

  const filters = useGraphFilters();

  const selectedNode: GraphNode | null = useMemo(() => {
    if (!kg || !selectedId) return null;
    return kg.nodes.find((n) => n.id === selectedId) ?? null;
  }, [kg, selectedId]);

  const rebuildLayout = useCallback(
    (data: KnowledgeGraph, mode: LayoutMode) => {
      const g = buildLayoutGraph(data, mode);
      setSigmaGraph(g);
    },
    [],
  );

  const loadGraph = useCallback(
    async (force = false) => {
      setViewMode('loading');
      setError(null);
      try {
        const status = await fetchIndexStatus();
        const nodeCount =
          typeof status.element_count === 'number' ? status.element_count : null;
        const edgeCount =
          typeof status.relationship_count === 'number' ? status.relationship_count : null;
        setIndexLine(
          `index: elements=${nodeCount ?? '?'} rels=${edgeCount ?? '?'} path=${status.project_path ?? ''}`,
        );
        if (status.project_path && !project) {
          setProject(String(status.project_path));
        }

        const explicit = parseSkipGraphParam(
          new URLSearchParams(window.location.search).get('skipGraph'),
        );
        const skip = decideSkipGraph({
          explicit: force ? false : explicit,
          nodeCount,
          threshold: LARGE_GRAPH_NODE_THRESHOLD,
          edgeCount,
          edgeThreshold: LARGE_GRAPH_EDGE_THRESHOLD,
        });

        if (skip && !force) {
          setSkipInfo({ nodes: nodeCount ?? 0, edges: edgeCount ?? 0 });
          try {
            const topo = await fetchServiceTopology();
            setKg(topo);
            rebuildLayout(topo, layoutMode);
          } catch {
            setKg({ nodes: [], relationships: [], nodeCount: 0, relationshipCount: 0 });
            setSigmaGraph(null);
          }
          setViewMode('overview');
          setStatusText('Overview (graph skipped — mega-graph gate)');
          return;
        }

        let data: KnowledgeGraph;
        try {
          const params = new URLSearchParams(window.location.search);
          const expandPath = params.get('path');
          const wantExpand = params.get('expand') === '1';
          const topo = await fetchServiceTopology();
          const topoHasSingle = topo.nodes.length === 1;
          if (expandPath) {
            data = await expandService(expandPath, true);
          } else if (topo.nodes.length >= 1 && !(wantExpand || topoHasSingle)) {
            data = topo;
          } else if ((wantExpand || topoHasSingle) && topo.nodes.length >= 1) {
            const root = topo.nodes[0];
            const path = String(root.properties.filePath || '');
            data = await expandService(path, true);
          } else {
            data = await expandService('', true);
          }
        } catch {
          data = await expandService('', true);
        }

        setKg(data);
        rebuildLayout(data, layoutMode);
        setSkipInfo(null);
        setViewMode('exploring');
        setStatusText(`Loaded ${data.nodeCount} nodes`);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : String(err));
        setViewMode('onboarding');
      }
    },
    [layoutMode, project, rebuildLayout, setProject],
  );

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const ok = await probeBackend();
      if (cancelled) return;
      setConnected(ok);
      if (!ok) {
        setViewMode('onboarding');
        setStatusText('Start leankg serve on :8080');
        return;
      }
      if (project) {
        try {
          await switchProject(project);
        } catch {
          // project switch may be unsupported in some builds — continue
        }
      }
      await loadGraph(false);
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!kg) return;
    if (viewMode !== 'exploring' && viewMode !== 'overview') return;
    rebuildLayout(kg, layoutMode);
  }, [layoutMode, kg, rebuildLayout, viewMode]);

  const onSearchSubmit = async () => {
    if (!searchTerm.trim()) {
      setHighlightIds(new Set());
      return;
    }
    try {
      const results = await searchCode(searchTerm.trim());
      const ids = new Set<string>();
      for (const r of results) {
        const row = r as Record<string, unknown>;
        if (typeof row.id === 'string') ids.add(row.id);
        if (typeof row.qualified_name === 'string') ids.add(row.qualified_name);
        if (typeof row.file_path === 'string' && kg) {
          for (const n of kg.nodes) {
            if (n.properties.filePath === row.file_path || n.id.includes(String(row.file_path))) {
              ids.add(n.id);
            }
          }
        }
      }
      // Also fuzzy-match loaded node names
      if (kg) {
        const q = searchTerm.toLowerCase();
        for (const n of kg.nodes) {
          if (
            n.properties.name?.toLowerCase().includes(q) ||
            n.id.toLowerCase().includes(q)
          ) {
            ids.add(n.id);
          }
        }
      }
      setHighlightIds(ids);
      setStatusText(`Search: ${ids.size} matches`);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const onLoadAnyway = () => {
    if (
      skipInfo &&
      shouldConfirmGraphLoad(skipInfo.nodes, LARGE_GRAPH_NODE_THRESHOLD) &&
      !window.confirm(
        `Load full graph (~${skipInfo.nodes} nodes)? This may freeze the browser.`,
      )
    ) {
      return;
    }
    void loadGraph(true);
  };

  return (
    <div className="h-screen w-screen flex flex-col bg-void text-text-primary overflow-hidden">
      <Header
        project={project}
        statusText={statusText}
        searchTerm={searchTerm}
        onSearchChange={setSearchTerm}
        onSearchSubmit={onSearchSubmit}
        connected={connected}
        layoutMode={layoutMode}
        onLayoutMode={setLayoutMode}
      />

      {viewMode === 'onboarding' && (
        <div
          data-testid="onboarding"
          className="flex-1 flex flex-col items-center justify-center gap-4 p-8"
        >
          <h1 className="text-2xl font-semibold">Connect to LeanKG</h1>
          <p className="text-text-secondary text-sm max-w-md text-center">
            Run <code className="text-accent">leankg serve</code> on port 8080, then reload. Dev
            proxy forwards <code>/api</code> to the backend.
          </p>
          {error && <p className="text-red-400 text-sm">{error}</p>}
          <button
            type="button"
            data-testid="retry-connect"
            onClick={() => void loadGraph(false)}
            className="px-4 py-2 rounded-md bg-accent text-white text-sm"
          >
            Retry
          </button>
        </div>
      )}

      {(viewMode === 'exploring' || viewMode === 'overview' || viewMode === 'loading') && (
        <div className="flex-1 flex min-h-0 relative">
          {viewMode === 'loading' && <LoadingOverlay message="Loading graph…" />}
          <FileTreePanel
            collapsed={leftCollapsed}
            onToggle={() => setLeftCollapsed((v) => !v)}
            nodes={kg?.nodes ?? []}
            allNodeTypes={filters.allNodeTypes}
            visibleLabels={filters.visibleLabels}
            visibleEdges={filters.visibleEdgeTypes}
            depthFilter={filters.depthFilter}
            onToggleLabel={filters.toggleLabelVisibility}
            onToggleEdge={filters.toggleEdgeVisibility}
            onDepth={filters.setDepthFilter}
            onResetFilters={filters.resetToStructuralDefaults}
            onSelectNode={setSelectedId}
            selectedId={selectedId}
          />
          <div className="relative flex-1 min-w-0">
            <GraphCanvas
              graph={sigmaGraph}
              visibleLabels={filters.effectiveLabels}
              visibleEdges={filters.effectiveEdges}
              searchTerm={searchTerm}
              highlightIds={highlightIds}
              onNodeSelect={setSelectedId}
              selectedNodeId={selectedId}
            />
            <CodePanel node={selectedNode} onClose={() => setSelectedId(null)} />
            {viewMode === 'overview' && (
              <div
                data-testid="mega-graph-banner"
                className="absolute top-3 left-1/2 -translate-x-1/2 z-20 flex items-center gap-3 px-4 py-2 rounded-lg bg-elevated border border-border-default shadow-glow-soft"
              >
                <span className="text-xs text-text-secondary">
                  Large graph skipped ({skipInfo?.nodes ?? '?'} nodes). Showing topology overview.
                </span>
                <button
                  type="button"
                  data-testid="load-graph-anyway"
                  onClick={onLoadAnyway}
                  className="text-xs px-2 py-1 rounded bg-accent text-white"
                >
                  Load graph anyway
                </button>
              </div>
            )}
          </div>
        </div>
      )}

      <StatusBar
        nodeCount={kg?.nodeCount ?? 0}
        edgeCount={kg?.relationshipCount ?? 0}
        indexStatus={indexLine}
      />
    </div>
  );
}
