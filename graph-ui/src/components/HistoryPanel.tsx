/**
 * FR-E36 — history/undo panel: bounded selection history with undo/redo.
 */
export interface HistoryEntry {
  id: string;
  label: string;
}

export default function HistoryPanel({
  entries,
  undoEnabled,
  redoEnabled,
  onUndo,
  onRedo,
}: {
  entries: HistoryEntry[];
  undoEnabled: boolean;
  redoEnabled: boolean;
  onUndo: () => void;
  onRedo: () => void;
}) {
  return (
    <section className="panel" data-testid="history-panel" aria-label="History">
      <h2 className="panel-title">History</h2>
      <div className="history-actions">
        <button onClick={onUndo} disabled={!undoEnabled}>
          Undo
        </button>
        <button onClick={onRedo} disabled={!redoEnabled}>
          Redo
        </button>
      </div>
      {entries.length === 0 ? (
        <p className="panel-muted">No selection history yet.</p>
      ) : (
        <ol className="history-list">
          {entries.map((e) => (
            <li key={e.id} className="history-item">
              <span className="mono">{e.label}</span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
