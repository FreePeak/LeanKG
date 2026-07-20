import { useState } from 'react';
import { Terminal, X } from 'lucide-react';
import { runQuery } from '../services/backend-client';

export function QueryFAB() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [result, setResult] = useState<string>('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const submit = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await runQuery(query);
      setResult(JSON.stringify(data, null, 2));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
      setResult('');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="absolute bottom-4 left-4 z-10">
      {!open && (
        <button
          type="button"
          data-testid="query-fab"
          onClick={() => setOpen(true)}
          className="flex items-center gap-2 px-3 py-2 rounded-full bg-accent text-white text-xs shadow-glow hover:bg-accent-dim"
        >
          <Terminal className="w-3.5 h-3.5" />
          Query
        </button>
      )}
      {open && (
        <div
          data-testid="query-panel"
          className="w-96 max-h-80 bg-elevated border border-border-default rounded-lg shadow-glow-soft flex flex-col"
        >
          <div className="flex items-center justify-between px-3 py-2 border-b border-border-subtle">
            <span className="text-xs font-medium text-text-primary">Raw query</span>
            <button type="button" onClick={() => setOpen(false)} className="text-text-muted">
              <X className="w-4 h-4" />
            </button>
          </div>
          <textarea
            data-testid="query-input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            rows={4}
            className="m-2 bg-surface border border-border-subtle rounded p-2 text-xs font-mono text-text-primary resize-none focus:outline-none focus:border-accent"
            placeholder="?[a, b] := ..."
          />
          <div className="px-2 pb-2 flex gap-2">
            <button
              type="button"
              data-testid="query-run"
              onClick={submit}
              disabled={loading || !query.trim()}
              className="px-3 py-1 text-xs rounded bg-accent text-white disabled:opacity-40"
            >
              {loading ? 'Running…' : 'Run'}
            </button>
          </div>
          {(result || error) && (
            <pre
              data-testid="query-result"
              className="mx-2 mb-2 max-h-32 overflow-auto text-[10px] font-mono bg-void p-2 rounded border border-border-subtle text-text-secondary"
            >
              {error || result}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}
