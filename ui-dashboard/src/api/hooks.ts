import { useCallback, useEffect, useState } from 'react';
import { getApi, type DashboardApi } from './client';
import { ApiError } from './client';

export interface AsyncState<T> {
  data: T | undefined;
  error: ApiError | Error | undefined;
  loading: boolean;
  reload: () => void;
}

/**
 * Load data through the API on mount and whenever `deps` change. Stale
 * responses are dropped and the request is aborted on unmount. `data` keeps
 * the previous value while a new load is running, so filters do not blank
 * the page.
 */
export function useAsync<T>(
  load: (api: DashboardApi, signal: AbortSignal) => Promise<T>,
  deps: readonly unknown[],
): AsyncState<T> {
  const [tick, setTick] = useState(0);
  const [state, setState] = useState<{ data?: T; error?: ApiError | Error; loading: boolean }>({ loading: true });

  useEffect(() => {
    const controller = new AbortController();
    setState((s) => ({ data: s.data, error: undefined, loading: true }));
    getApi()
      .then((api) => load(api, controller.signal))
      .then((data) => {
        if (!controller.signal.aborted) setState({ data, loading: false });
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        const error = err instanceof Error ? err : new Error(String(err));
        setState((s) => ({ data: s.data, error, loading: false }));
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tick, ...deps]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data: state.data, error: state.error, loading: state.loading, reload };
}
