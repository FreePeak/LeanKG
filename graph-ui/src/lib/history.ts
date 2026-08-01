/**
 * FR-E36 — history/undo: bounded selection history with undo/redo.
 * Pure state machine, testable without React.
 */
export interface HistoryState<T> {
  past: T[];
  present: T;
  future: T[];
  limit: number;
}

export function createHistory<T>(present: T, limit = 50): HistoryState<T> {
  return { past: [], present, future: [], limit };
}

export function pushHistory<T>(state: HistoryState<T>, next: T): HistoryState<T> {
  const past = [...state.past, state.present];
  if (past.length > state.limit) past.shift();
  return { past, present: next, future: [], limit: state.limit };
}

export function undoHistory<T>(state: HistoryState<T>): HistoryState<T> | null {
  if (state.past.length === 0) return null;
  const past = [...state.past];
  const previous = past.pop()!;
  return {
    past,
    present: previous,
    future: [state.present, ...state.future],
    limit: state.limit,
  };
}

export function redoHistory<T>(state: HistoryState<T>): HistoryState<T> | null {
  if (state.future.length === 0) return null;
  const [next, ...future] = state.future;
  return {
    past: [...state.past, state.present],
    present: next,
    future,
    limit: state.limit,
  };
}

export function canUndo<T>(state: HistoryState<T>): boolean {
  return state.past.length > 0;
}

export function canRedo<T>(state: HistoryState<T>): boolean {
  return state.future.length > 0;
}
