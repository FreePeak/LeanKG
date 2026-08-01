/**
 * US-UI2-08 / FR-UI2-10 — cluster legend state: fetch clusters, show/hide
 * toggles (Graphify sidebar parity), and the set of visible cluster ids.
 */
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { KnowledgeGraph } from '../core/graph/types';
import { loadClusterGraph } from '../lib/clusters';

export interface ClusterLegendState {
  clusters: KnowledgeGraph | null;
  error: string | null;
  loading: boolean;
  visibleIds: Set<string>;
  visibleCount: number;
  totalCount: number;
  toggle: (id: string) => void;
  showAll: () => void;
  hideAll: () => void;
  reload: () => Promise<void>;
}

export function useClusterLegend(): ClusterLegendState {
  const [clusters, setClusters] = useState<KnowledgeGraph | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [visibleIds, setVisibleIds] = useState<Set<string>>(new Set());

  const reload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const graph = await loadClusterGraph();
      setClusters(graph);
      setVisibleIds(new Set(graph.nodes.map((n) => n.id)));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
      setClusters(null);
      setVisibleIds(new Set());
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const toggle = useCallback((id: string) => {
    setVisibleIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const showAll = useCallback(() => {
    setVisibleIds((prev) => {
      if (!clusters) return prev;
      return new Set(clusters.nodes.map((n) => n.id));
    });
  }, [clusters]);

  const hideAll = useCallback(() => setVisibleIds(new Set()), []);

  return useMemo(
    () => ({
      clusters,
      error,
      loading,
      visibleIds,
      visibleCount: visibleIds.size,
      totalCount: clusters?.nodes.length ?? 0,
      toggle,
      showAll,
      hideAll,
      reload,
    }),
    [clusters, error, loading, visibleIds, toggle, showAll, hideAll, reload],
  );
}
