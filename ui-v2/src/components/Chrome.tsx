import type { LayoutMode } from '../lib/graph-adapter';
import { Database, Loader2 } from 'lucide-react';

interface HeaderProps {
  project?: string;
  statusText: string;
  searchTerm: string;
  onSearchChange: (v: string) => void;
  onSearchSubmit: () => void;
  connected: boolean;
  layoutMode: LayoutMode;
  onLayoutMode: (m: LayoutMode) => void;
}

export function Header({
  project,
  statusText,
  searchTerm,
  onSearchChange,
  onSearchSubmit,
  connected,
  layoutMode,
  onLayoutMode,
}: HeaderProps) {
  return (
    <header className="h-12 shrink-0 border-b border-border-subtle bg-deep flex items-center gap-3 px-4">
      <Database className="w-5 h-5 text-accent" />
      <span className="font-semibold text-text-primary tracking-wide">LeanKG</span>
      <span className="text-xs text-text-muted truncate max-w-[240px]" title={project}>
        {project || 'local project'}
      </span>
      <div className="flex-1" />
      <div className="flex items-center gap-1 rounded-lg bg-surface border border-border-subtle p-0.5">
        {(['force', 'tree', 'circles'] as LayoutMode[]).map((m) => (
          <button
            key={m}
            type="button"
            data-testid={`layout-${m}`}
            onClick={() => onLayoutMode(m)}
            className={`px-2.5 py-1 text-xs rounded-md capitalize ${
              layoutMode === m
                ? 'bg-accent text-white'
                : 'text-text-secondary hover:text-text-primary'
            }`}
          >
            {m}
          </button>
        ))}
      </div>
      <form
        className="flex items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          onSearchSubmit();
        }}
      >
        <input
          data-testid="header-search"
          value={searchTerm}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search…"
          className="w-48 bg-surface border border-border-default rounded-md px-2 py-1 text-sm text-text-primary placeholder:text-text-muted focus:outline-none focus:border-accent"
        />
      </form>
      <span
        data-testid="connection-status"
        className={`text-xs ${connected ? 'text-emerald-400' : 'text-amber-400'}`}
      >
        {connected ? 'connected' : 'disconnected'}
      </span>
      <span className="text-xs text-text-muted hidden sm:inline">{statusText}</span>
    </header>
  );
}

export function LoadingOverlay({ message }: { message: string }) {
  return (
    <div className="absolute inset-0 z-20 flex flex-col items-center justify-center bg-void/80">
      <Loader2 className="w-8 h-8 text-accent animate-spin mb-3" />
      <p className="text-text-secondary text-sm">{message}</p>
    </div>
  );
}

export function StatusBar({
  nodeCount,
  edgeCount,
  indexStatus,
}: {
  nodeCount: number;
  edgeCount: number;
  indexStatus: string;
}) {
  return (
    <footer
      data-testid="status-bar"
      className="h-7 shrink-0 border-t border-border-subtle bg-deep px-3 flex items-center gap-4 text-[11px] text-text-muted"
    >
      <span>
        nodes: <strong className="text-text-secondary">{nodeCount}</strong>
      </span>
      <span>
        edges: <strong className="text-text-secondary">{edgeCount}</strong>
      </span>
      <span className="truncate">{indexStatus}</span>
    </footer>
  );
}
