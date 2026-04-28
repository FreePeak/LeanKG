import { useState, useCallback, useMemo } from 'react';
import { DEFAULT_NODE_TYPE_ORDER, DEFAULT_VISIBLE_LABELS, EDGE_STYLES } from '../lib/constants';

export type EdgeType = string;

export type ZoomLevel = 'clusters' | 'modules' | 'files' | 'functions';

export const ZOOM_LEVEL_CONFIG: Record<ZoomLevel, {
  label: string;
  description: string;
  visibleLabels: string[];
}> = {
  clusters: {
    label: 'Clusters',
    description: 'Top-level module clusters',
    visibleLabels: [], // All hidden except conceptual cluster nodes
  },
  modules: {
    label: 'Modules',
    description: 'Folders and packages',
    visibleLabels: ['Folder', 'Package', 'Module'],
  },
  files: {
    label: 'Files',
    description: 'Source files and modules',
    visibleLabels: ['Folder', 'File', 'Module'],
  },
  functions: {
    label: 'Functions',
    description: 'All elements including functions',
    visibleLabels: ['Folder', 'File', 'Module', 'Class', 'Interface', 'Function', 'Method'],
  },
};

export const STRUCTURAL_LABELS = DEFAULT_VISIBLE_LABELS;

export const useGraphFilters = () => {
  const [visibleLabels, setVisibleLabels] = useState<string[]>([...DEFAULT_VISIBLE_LABELS]);
  const [visibleEdgeTypes, setVisibleEdgeTypes] = useState<EdgeType[]>([...Object.keys(EDGE_STYLES)]);
  const [depthFilter, setDepthFilter] = useState<number | null>(null);
  const [zoomLevel, setZoomLevel] = useState<ZoomLevel>('functions');
  const [selectedNode, setSelectedNode] = useState<string | null>(null);

  const toggleLabelVisibility = useCallback((label: string) => {
    setVisibleLabels((prev) =>
      prev.includes(label) ? prev.filter((l) => l !== label) : [...prev, label],
    );
  }, []);

  const toggleEdgeVisibility = useCallback((edgeType: EdgeType) => {
    setVisibleEdgeTypes((prev) =>
      prev.includes(edgeType) ? prev.filter((e) => e !== edgeType) : [...prev, edgeType],
    );
  }, []);

  const cycleZoomLevel = useCallback(() => {
    const levels: ZoomLevel[] = ['clusters', 'modules', 'files', 'functions'];
    const currentIndex = levels.indexOf(zoomLevel);
    const nextIndex = (currentIndex + 1) % levels.length;
    setZoomLevel(levels[nextIndex]);
  }, [zoomLevel]);

  const resetToStructuralDefaults = useCallback(() => {
    setVisibleLabels(STRUCTURAL_LABELS);
  }, []);

  const effectiveLabels = useMemo(() => {
    if (visibleLabels.length > 0) {
      return visibleLabels;
    }
    // When nothing manually selected, return ALL known filterable types (show everything)
    return DEFAULT_NODE_TYPE_ORDER;
  }, [visibleLabels]);

  return {
    visibleLabels,
    setVisibleLabels,
    toggleLabelVisibility,
    visibleEdgeTypes,
    setVisibleEdgeTypes,
    toggleEdgeVisibility,
    depthFilter,
    setDepthFilter,
    zoomLevel,
    setZoomLevel,
    cycleZoomLevel,
    resetToStructuralDefaults,
    selectedNode,
    setSelectedNode,
    effectiveLabels,
  };
};
