import { useEffect } from 'react';
import { useSigma } from '../hooks/useSigma';
import { createSigmaGraph, filterGraphByDepth } from '../lib/graph-adapter';
import type { KGNode, KGEdge } from '../lib/graph-adapter';
import { CodeViewer } from './CodeViewer';
import { ZoomIn, ZoomOut, Maximize } from 'lucide-react';
import type { ZoomLevel } from '../hooks/useGraphFilters';

interface GraphViewerProps {
  data: { nodes: KGNode[]; relationships: KGEdge[] } | null;
  loading: boolean;
  error: string | null;
  searchTerm?: string;
  visibleEdgeTypes: string[];
  depthFilter: number | null;
  visibleLabels: string[];
  zoomLevel?: ZoomLevel;
  onNodeClick?: (nodeId: string) => void;
  onNodeDoubleClick?: (nodeId: string) => void;
}

export const GraphViewer = ({
  data,
  loading,
  error,
  searchTerm,
  visibleEdgeTypes,
  depthFilter,
  visibleLabels,
  zoomLevel = 'functions',
  onNodeClick,
  onNodeDoubleClick,
}: GraphViewerProps) => {
  const {
    containerRef,
    setGraph,
    zoomIn,
    zoomOut,
    resetZoom,
    selectedNode,
    setSelectedNode,
    sigmaRef,
  } = useSigma({
    visibleEdgeTypes,
    searchTerm,
    onNodeClick,
    onNodeDoubleClick,
  });

  useEffect(() => {
    if (!data || data.nodes.length === 0) return;
    const graph = createSigmaGraph(data.nodes, data.relationships);
    setGraph(graph, true); // Always skip animation - positions are pre-calculated
  }, [data, setGraph]);

  useEffect(() => {
    if (sigmaRef.current && data) {
      const g = sigmaRef.current.getGraph();
      const labels =
        visibleLabels.length > 0
          ? visibleLabels
          : Array.from(new Set(data.nodes.map((n) => String((n.properties as Record<string, unknown>)?.elementType || n.label || ''))));
      filterGraphByDepth(g as unknown as Parameters<typeof filterGraphByDepth>[0], selectedNode, depthFilter, labels);
      sigmaRef.current.refresh();
    }
  }, [depthFilter, selectedNode, visibleLabels, sigmaRef, data, zoomLevel]);

  return (
    <div className="relative w-full h-full bg-[#0A0F24] overflow-hidden flex">
      <div ref={containerRef} className="absolute inset-0 outline-none" />

      {loading && (
        <div className="absolute inset-0 flex flex-col items-center justify-center text-slate-400 bg-[#0A0F24] z-20">
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-slate-400 mb-4"></div>
          <p>Analyzing Knowledge Graph...</p>
        </div>
      )}

      {error && (
        <div className="absolute inset-0 flex flex-col items-center justify-center text-red-400 bg-[#0A0F24] z-20">
          <p className="text-xl mb-2">Failed to load graph</p>
          <p className="text-sm opacity-80">{error}</p>
        </div>
      )}

      <div className="absolute top-4 left-4 z-10">
        <div className="bg-[#12182b]/95 backdrop-blur-md border border-slate-700/50 rounded-lg shadow-xl flex flex-col p-1 gap-1">
          <button
            onClick={zoomIn}
            className="p-2 text-slate-400 hover:text-cyan-400 hover:bg-slate-800 rounded-md transition-colors"
            title="Zoom In"
          >
            <ZoomIn className="h-4 w-4" />
          </button>
          <div className="h-px w-full bg-slate-700/50" />
          <button
            onClick={zoomOut}
            className="p-2 text-slate-400 hover:text-cyan-400 hover:bg-slate-800 rounded-md transition-colors"
            title="Zoom Out"
          >
            <ZoomOut className="h-4 w-4" />
          </button>
          <div className="h-px w-full bg-slate-700/50" />
          <button
            onClick={resetZoom}
            className="p-2 text-slate-400 hover:text-cyan-400 hover:bg-slate-800 rounded-md transition-colors"
            title="Fit to screen"
          >
            <Maximize className="h-4 w-4" />
          </button>
        </div>
      </div>

      {selectedNode && (() => {
        const node = data?.nodes.find(n => n.id === selectedNode);
        const elementType = (node?.properties?.elementType as string)?.toLowerCase() || '';
        if (elementType === 'service' || elementType === 'folder') {
          return null;
        }
        return (
          <CodeViewer
            selectedNode={selectedNode}
            graphData={data}
            onClose={() => setSelectedNode(null)}
          />
        );
      })()}
    </div>
  );
};