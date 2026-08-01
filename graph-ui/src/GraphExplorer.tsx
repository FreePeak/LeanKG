import { useCallback, useEffect, useMemo, useState } from 'react';
import { fetchClusters, fetchGraphData, fetchLayout3D, fetchProjects, switchProject } from './lib/api';
import type { GraphData, GraphNode, Layout3DResponse } from './lib/types';
import GraphScene from './scene/GraphScene';
import ClusterLegend from './components/ClusterLegend';
import DetailPanel from './components/DetailPanel';
import GraphSummary from './components/GraphSummary';
import FilterPanel from './components/FilterPanel';
import SettingsPanel, { DEFAULT_SETTINGS, type DisplaySettings } from './components/SettingsPanel';
import HistoryPanel, { type HistoryEntry } from './components/HistoryPanel';
import ExportPanel from './components/ExportPanel';
import SearchPanel from './components/SearchPanel';
import ProjectPanel, { type ProjectInfo } from './components/ProjectPanel';
import ErrorBanner from './components/ErrorBanner';
import LoadingOverlay from './components/LoadingOverlay';
import {
  defaultTypeFilter,
  filterRelationships,
  type TypeFilter,
} from './lib/filters';
import { createHistory, pushHistory, redoHistory, undoHistory } from './lib/history';
import { readUrlState, writeUrlState, type TabId } from './lib/url';
import { matchShortcut, SHORTCUTS } from './lib/keyboard';

type Status = 'idle' | 'loading' | 'ready' | 'error';

/**
 * FR-E01..E05 + FR-E30..E43 — 3D graph explorer with panels.
 * - E30 graph summary/stats
 * - E31 edge-type filter
 * - E32/E38 display settings
 * - E33 project selector + search
 * - E34 URL routing (tab + project survive refresh)
 * - E35 highlight on hover (scene dims non-related nodes)
 * - E36 history/undo
 * - E37 export/share
 * - E39 loading/progress, E40 error states
 * - E41 keyboard shortcuts, E42 responsive layout, E43 a11y
 */
export default function GraphExplorer() {
  const [layout, setLayout] = useState<Layout3DResponse | null>(null);
  const [layoutStatus, setLayoutStatus] = useState<Status>('idle');
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [graphError, setGraphError] = useState<string | null>(null);
  const [clusters, setClusters] = useState<GraphData | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Panels (E30..E38)
  const [urlState, setUrlState] = useState(() => readUrlState(window.location.search));
  const [filter, setFilter] = useState<TypeFilter>({});
  const [settings, setSettings] = useState<DisplaySettings>(DEFAULT_SETTINGS);
  const [history, setHistory] = useState(() =>
    createHistory<HistoryEntry | null>(null),
  );
  const [projects, setProjects] = useState<ProjectInfo[]>([]);
  const [projectsError, setProjectsError] = useState<string | null>(null);
  const [projectLoading, setProjectLoading] = useState(false);

  const [panelOpen, setPanelOpen] = useState({
    summary: true,
    filters: false,
    settings: false,
    history: false,
    search: urlState.tab === 'search',
    export: urlState.tab === 'export',
    projects: false,
    legend: true,
  });

  // E34 — keep URL in sync with tab + project.
  useEffect(() => {
    writeUrlState({ tab: urlState.tab, project: urlState.project });
  }, [urlState]);

  useEffect(() => {
    let cancelled = false;
    Promise.all([fetchGraphData(), fetchClusters()])
      .then(([g, c]) => {
        if (cancelled) return;
        setGraph(g);
        setClusters(c);
        setFilter(defaultTypeFilter(g));
      })
      .catch((e: unknown) => {
        if (!cancelled) setGraphError(String(e instanceof Error ? e.message : e));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // E33 — load projects list once.
  const reloadProjects = useCallback(async () => {
    setProjectLoading(true);
    setProjectsError(null);
    try {
      const list = await fetchProjects();
      setProjects(list);
    } catch (e: unknown) {
      setProjectsError(String(e instanceof Error ? e.message : e));
    } finally {
      setProjectLoading(false);
    }
  }, []);

  useEffect(() => {
    void reloadProjects();
  }, [reloadProjects]);

  const loadLayout = useCallback(async () => {
    setLayoutStatus('loading');
    setError(null);
    try {
      const l = await fetchLayout3D();
      setLayout(l);
      setLayoutStatus('ready');
    } catch (e: unknown) {
      setLayoutStatus('error');
      setError(String(e instanceof Error ? e.message : e));
    }
  }, []);

  // E35/E36 — selection updates history (undo/redo).
  const selectNode = useCallback((id: string | null) => {
    setSelectedId(id);
    setHistory((h) => {
      const label = id ?? '';
      const entry: HistoryEntry | null = id ? { id, label } : null;
      return pushHistory(h, entry);
    });
  }, []);

  const undo = useCallback(() => {
    setHistory((h) => {
      const next = undoHistory(h);
      if (next) {
        setSelectedId(next.present?.id ?? null);
        return next;
      }
      return h;
    });
  }, []);

  const redo = useCallback(() => {
    setHistory((h) => {
      const next = redoHistory(h);
      if (next) {
        setSelectedId(next.present?.id ?? null);
        return next;
      }
      return h;
    });
  }, []);

  // E41 — keyboard shortcuts.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const action = matchShortcut(e);
      if (!action) return;
      e.preventDefault();
      switch (action) {
        case 'toggleFilters':
          setPanelOpen((p) => ({ ...p, filters: !p.filters }));
          break;
        case 'toggleSettings':
          setPanelOpen((p) => ({ ...p, settings: !p.settings }));
          break;
        case 'toggleLegend':
          setPanelOpen((p) => ({ ...p, legend: !p.legend }));
          break;
        case 'toggleHistory':
          setPanelOpen((p) => ({ ...p, history: !p.history }));
          break;
        case 'openSearch':
          setPanelOpen((p) => ({ ...p, search: true }));
          break;
        case 'export':
          setPanelOpen((p) => ({ ...p, export: !p.export }));
          break;
        case 'undo':
          undo();
          break;
        case 'redo':
          redo();
          break;
        case 'closePanel':
          setPanelOpen((p) => ({ ...p, filters: false, settings: false, history: false, search: false, export: false, projects: false }));
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [undo, redo]);

  // E36 — project switch re-fetches graph data for the new project.
  const handleProjectSwitch = useCallback(
    async (path: string) => {
      setProjectLoading(true);
      setProjectsError(null);
      try {
        const switched = await switchProject(path);
        setUrlState((s) => ({ ...s, project: switched.project_path ?? path }));
        const [g, c] = await Promise.all([fetchGraphData(), fetchClusters()]);
        setGraph(g);
        setClusters(c);
        setFilter(defaultTypeFilter(g));
        setLayout(null);
        setLayoutStatus('idle');
        setSelectedId(null);
      } catch (e: unknown) {
        setProjectsError(String(e instanceof Error ? e.message : e));
      } finally {
        setProjectLoading(false);
      }
    },
    [],
  );

  const selectedNode = useMemo<GraphNode | null>(
    () => graph?.nodes.find((n) => n.id === selectedId) ?? null,
    [graph, selectedId],
  );

  // E31 — apply edge-type filter before rendering.
  const filteredGraph = useMemo(() => filterRelationships(graph, filter), [graph, filter]);

  const edges = useMemo(
    () => (filteredGraph?.relationships ?? []).map((r) => [r.sourceId, r.targetId] as [string, string]),
    [filteredGraph],
  );

  const clusterNodes = clusters?.nodes ?? [];
  const graphLoading = graph == null && graphError == null;

  return (
    <div className="explorer" aria-label="LeanKG 3D graph explorer">
      <header className="topbar">
        <h1>LeanKG 3D Graph Explorer</h1>
        <div className="topbar-actions">
          <span className="counts">
            {layout?.nodes.length ?? 0} nodes / {edges.length} edges
          </span>
          <nav className="panel-tabs" aria-label="Panels">
            <button
              className={panelOpen.summary ? 'active' : ''}
              onClick={() => setPanelOpen((p) => ({ ...p, summary: !p.summary }))}
            >
              Summary
            </button>
            <button
              className={panelOpen.filters ? 'active' : ''}
              onClick={() => setPanelOpen((p) => ({ ...p, filters: !p.filters }))}
              aria-label="Toggle edge filter (f)"
            >
              Filter
            </button>
            <button
              className={panelOpen.settings ? 'active' : ''}
              onClick={() => setPanelOpen((p) => ({ ...p, settings: !p.settings }))}
              aria-label="Toggle settings (s)"
            >
              Settings
            </button>
            <button
              className={panelOpen.projects ? 'active' : ''}
              onClick={() => setPanelOpen((p) => ({ ...p, projects: !p.projects }))}
            >
              Projects
            </button>
            <button
              className={panelOpen.search ? 'active' : ''}
              onClick={() => {
                const next = !panelOpen.search;
                setPanelOpen((p) => ({ ...p, search: next }));
                setUrlState((s) => ({ ...s, tab: (next ? 'search' : 'graph') as TabId }));
              }}
              aria-label="Toggle search (/)"
            >
              Search
            </button>
            <button
              className={panelOpen.export ? 'active' : ''}
              onClick={() => {
                const next = !panelOpen.export;
                setPanelOpen((p) => ({ ...p, export: next }));
                setUrlState((s) => ({ ...s, tab: (next ? 'export' : 'graph') as TabId }));
              }}
              aria-label="Toggle export (e)"
            >
              Export
            </button>
            <button
              className={panelOpen.history ? 'active' : ''}
              onClick={() => setPanelOpen((p) => ({ ...p, history: !p.history }))}
              aria-label="Toggle history (h)"
            >
              History
            </button>
          </nav>
          <button
            onClick={loadLayout}
            disabled={layoutStatus === 'loading' || layoutStatus === 'idle'}
          >
            {layoutStatus === 'loading' ? 'Computing layout…' : 'Recompute layout'}
          </button>
        </div>
      </header>
      <ErrorBanner message={graphError} onRetry={() => window.location.reload()} />
      <ErrorBanner message={error} onRetry={loadLayout} onDismiss={() => setError(null)} />
      <ErrorBanner message={projectsError} onDismiss={() => setProjectsError(null)} />
      <LoadingOverlay loading={graphLoading} label="Loading graph…" />
      <div className="stage">
        {layoutStatus === 'idle' && (
          <div className="empty-state">
            <p>3D layout is computed on demand.</p>
            <button className="primary" onClick={loadLayout}>Load 3D layout</button>
          </div>
        )}
        {layout && (
          <GraphScene
            layoutNodes={layout.nodes}
            nodes={filteredGraph?.nodes ?? []}
            edges={edges}
            selectedId={selectedId}
            onSelect={selectNode}
            edgeBrightness={settings.edgeBrightness}
            labelsVisible={settings.labelsVisible}
            densityScale={settings.densityScale}
          />
        )}
        {panelOpen.legend && (
          <ClusterLegend nodes={clusterNodes} />
        )}
        <DetailPanel node={selectedNode} relationships={graph?.relationships ?? []} onClose={() => selectNode(null)} />
        {panelOpen.summary && <GraphSummary graph={filteredGraph} />}
        {panelOpen.filters && (
          <FilterPanel graph={graph} filter={filter} onChange={setFilter} />
        )}
        {panelOpen.settings && (
          <SettingsPanel settings={settings} onChange={setSettings} />
        )}
        {panelOpen.projects && (
          <ProjectPanel
            projects={projects}
            activePath={urlState.project ?? null}
            onSwitch={handleProjectSwitch}
            onRefresh={reloadProjects}
            refreshing={projectLoading}
          />
        )}
        {panelOpen.search && <SearchPanel graph={graph} onSelect={selectNode} />}
        {panelOpen.export && (
          <ExportPanel graph={graph} project={urlState.project} tab={urlState.tab} />
        )}
        {panelOpen.history && (
          <HistoryPanel
            entries={history.past
              .map((e) => e)
              .concat(history.present ? [history.present] : [])
              .filter((e): e is HistoryEntry => e != null)}
            undoEnabled={history.past.length > 0}
            redoEnabled={history.future.length > 0}
            onUndo={undo}
            onRedo={redo}
          />
        )}
        <div className="shortcuts-hint" aria-hidden="true">
          {SHORTCUTS.toggleFilters} filter · {SHORTCUTS.toggleSettings} settings · / search · z/y undo/redo
        </div>
      </div>
    </div>
  );
}
