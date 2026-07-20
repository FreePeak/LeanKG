import { useEffect, useRef } from 'react';
import type Graph from 'graphology';
import { useSigma } from '../hooks/useSigma';
import type { SigmaNodeAttributes, SigmaEdgeAttributes } from '../lib/graph-adapter';
import { filterGraphByLabels } from '../lib/graph-adapter';
import { QueryFAB } from './QueryFAB';

declare global {
  interface Window {
    sig?: unknown;
  }
}

interface GraphCanvasProps {
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes> | null;
  visibleLabels: string[];
  visibleEdges: string[];
  searchTerm: string;
  highlightIds: Set<string>;
  onNodeSelect: (nodeId: string | null) => void;
  selectedNodeId: string | null;
  layoutMode: 'force' | 'tree' | 'circles';
}

export function GraphCanvas({
  graph,
  visibleLabels,
  visibleEdges,
  searchTerm,
  highlightIds,
  onNodeSelect,
  selectedNodeId,
  layoutMode,
}: GraphCanvasProps) {
  const {
    containerRef,
    sigmaRef,
    setGraph,
    zoomIn,
    zoomOut,
    resetZoom,
    focusNode,
    setSelectedNode,
  } = useSigma({
    onNodeClick: (id) => {
      onNodeSelect(id);
      return true;
    },
    onStageClick: () => onNodeSelect(null),
    visibleEdgeTypes: visibleEdges,
    searchTerm,
    layoutMode,
  });

  const appliedRef = useRef<{ graph: Graph | null; mode: string }>({ graph: null, mode: '' });

  useEffect(() => {
    if (!graph) return;
    // Re-apply when layout mode changes even if the same KnowledgeGraph object is reused.
    if (appliedRef.current.graph === graph && appliedRef.current.mode === layoutMode) return;
    appliedRef.current = { graph, mode: layoutMode };
    const clone = graph.copy();
    filterGraphByLabels(clone, visibleLabels);
    setGraph(clone);
  }, [graph, setGraph, visibleLabels, layoutMode]);

  useEffect(() => {
    const sigma = sigmaRef.current;
    if (!graph || !sigma) return;
    const g = sigma.getGraph() as Graph<SigmaNodeAttributes, SigmaEdgeAttributes>;
    filterGraphByLabels(g, visibleLabels);
    g.forEachEdge((edge, attrs) => {
      const rel = String(attrs.relationType || '').toUpperCase();
      const show = visibleEdges.length === 0 || visibleEdges.includes(rel);
      g.setEdgeAttribute(edge, 'hidden', !show);
    });
    sigma.refresh();
  }, [visibleLabels, visibleEdges, graph, sigmaRef]);

  useEffect(() => {
    if (selectedNodeId) {
      setSelectedNode(selectedNodeId);
      focusNode(selectedNodeId);
    }
  }, [selectedNodeId, setSelectedNode, focusNode]);

  useEffect(() => {
    if (sigmaRef.current) {
      window.sig = sigmaRef.current;
    }
    return () => {
      if (window.sig === sigmaRef.current) delete window.sig;
    };
  }, [sigmaRef, graph]);

  useEffect(() => {
    const sigma = sigmaRef.current;
    if (!sigma || highlightIds.size === 0) return;
    const g = sigma.getGraph();
    g.forEachNode((id) => {
      g.setNodeAttribute(id, 'highlighted', highlightIds.has(id));
    });
    sigma.refresh();
  }, [highlightIds, sigmaRef]);

  return (
    <div className="relative flex-1 min-w-0 min-h-0 h-full w-full bg-void" data-testid="graph-canvas">
      <div
        ref={containerRef}
        className="sigma-container absolute inset-0 w-full h-full"
        style={{ minHeight: '100%' }}
      />
      {!graph && (
        <div className="absolute inset-0 flex items-center justify-center text-text-muted text-sm">
          No graph loaded
        </div>
      )}
      <div className="absolute bottom-4 right-4 flex flex-col gap-1 z-10">
        <button
          type="button"
          onClick={zoomIn}
          className="w-8 h-8 rounded bg-elevated border border-border-subtle text-text-secondary hover:text-text-primary"
        >
          +
        </button>
        <button
          type="button"
          onClick={zoomOut}
          className="w-8 h-8 rounded bg-elevated border border-border-subtle text-text-secondary hover:text-text-primary"
        >
          −
        </button>
        <button
          type="button"
          onClick={resetZoom}
          className="w-8 h-8 rounded bg-elevated border border-border-subtle text-text-secondary text-[10px]"
        >
          Fit
        </button>
      </div>
      <QueryFAB />
    </div>
  );
}
