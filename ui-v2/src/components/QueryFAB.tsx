import { useState } from 'react';
import { Terminal, X } from 'lucide-react';
import { runQuery, runQueryGraph } from '../services/backend-client';
import {
  DEFAULT_QUERY_FAB_MODE,
  queryPlaceholder,
  type QueryFabMode,
} from '../lib/query-fab-mode';

export function QueryFAB() {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<QueryFabMode>(DEFAULT_QUERY_FAB_MODE);
  const [query, setQuery] = useState('');
  const [result, setResult] = useState<string>('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const submit = async () => {
    setLoading(true);
    setError(null);
    try {
      const data =
        mode === 'nl' ? await runQueryGraph(query) : await runQuery(query);
      setResult(JSON.stringify(data, null, 2));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
      setResult('');
    } finally {
      setLoading(false);
    }
  };

  const title = mode === 'nl' ? 'Natural language query' : 'Raw graph query';

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
          <div className="flex items-center justify-between px-3 py-2 border-b border-border-subtle gap-2">
            <span
              data-testid="query-panel-title"
              className="text-xs font-medium text-text-primary truncate"
            >
              {title}
            </span>
            <button type="button" onClick={() => setOpen(false)} className="text-text-muted shrink-0">
              <X className="w-4 h-4" />
            </button>
          </div>
          <div className="flex gap-1 px-2 pt-2">
            <button
              type="button"
              data-testid="query-mode-nl"
              aria-pressed={mode === 'nl'}
              onClick={() => setMode('nl')}
              className={`px-2 py-1 text-[10px] rounded border ${
                mode === 'nl'
                  ? 'bg-accent text-white border-accent'
                  : 'bg-surface text-text-secondary border-border-subtle'
              }`}
            >
              NL
            </button>
            <button
              type="button"
              data-testid="query-mode-advanced"
              aria-pressed={mode === 'advanced'}
              onClick={() => setMode('advanced')}
              className={`px-2 py-1 text-[10px] rounded border ${
                mode === 'advanced'
                  ? 'bg-accent text-white border-accent'
                  : 'bg-surface text-text-secondary border-border-subtle'
              }`}
            >
              Advanced
            </button>
          </div>
          <textarea
            data-testid="query-input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            rows={4}
            className="m-2 bg-surface border border-border-subtle rounded p-2 text-xs font-mono text-text-primary resize-none focus:outline-none focus:border-accent"
            placeholder={queryPlaceholder(mode)}
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
