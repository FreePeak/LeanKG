import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  DEFAULT_VISIBLE_LABELS,
  DEFAULT_VISIBLE_EDGES,
  DEFAULT_NODE_TYPE_ORDER,
  EDGE_STYLES,
} from '../lib/constants';
import { parseProjectParam } from '../services/backend-client';

export function useGraphFilters() {
  const [visibleLabels, setVisibleLabels] = useState<string[]>([...DEFAULT_VISIBLE_LABELS]);
  const [visibleEdgeTypes, setVisibleEdgeTypes] = useState<string[]>([...DEFAULT_VISIBLE_EDGES]);
  const [depthFilter, setDepthFilter] = useState(2);

  const toggleLabelVisibility = useCallback((label: string) => {
    setVisibleLabels((prev) =>
      prev.includes(label) ? prev.filter((l) => l !== label) : [...prev, label],
    );
  }, []);

  const toggleEdgeVisibility = useCallback((edge: string) => {
    setVisibleEdgeTypes((prev) =>
      prev.includes(edge) ? prev.filter((e) => e !== edge) : [...prev, edge],
    );
  }, []);

  const resetToStructuralDefaults = useCallback(() => {
    setVisibleLabels([...DEFAULT_VISIBLE_LABELS]);
    setVisibleEdgeTypes([...DEFAULT_VISIBLE_EDGES]);
    setDepthFilter(2);
  }, []);

  const effectiveLabels = useMemo(() => visibleLabels, [visibleLabels]);
  const effectiveEdges = useMemo(() => visibleEdgeTypes, [visibleEdgeTypes]);

  return {
    visibleLabels,
    visibleEdgeTypes,
    depthFilter,
    setDepthFilter,
    toggleLabelVisibility,
    toggleEdgeVisibility,
    resetToStructuralDefaults,
    effectiveLabels,
    effectiveEdges,
    allNodeTypes: DEFAULT_NODE_TYPE_ORDER,
    allEdgeTypes: Object.keys(EDGE_STYLES),
  };
}

export function useUrlProject(): [string | undefined, (p: string | undefined) => void] {
  const [project, setProjectState] = useState<string | undefined>(() => {
    if (typeof window === 'undefined') return undefined;
    return parseProjectParam(new URLSearchParams(window.location.search).get('project'));
  });

  useEffect(() => {
    const url = new URL(window.location.href);
    if (project) url.searchParams.set('project', project);
    else url.searchParams.delete('project');
    window.history.replaceState({}, '', url.toString());
  }, [project]);

  return [project, setProjectState];
}
