/**
 * FR-E33 search — local filter over loaded graph nodes (qualified name,
 * element type, file path). Instant, no extra backend round-trip.
 */
import { useMemo, useState } from 'react';
import type { GraphData, GraphNode } from '../lib/types';

export function matchNode(node: GraphNode, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    node.id.toLowerCase().includes(q) ||
    node.properties.name.toLowerCase().includes(q) ||
    node.properties.filePath.toLowerCase().includes(q) ||
    node.properties.elementType.toLowerCase().includes(q)
  );
}

export interface SearchResult {
  node: GraphNode;
  index: number;
}

export default function SearchPanel({
  graph,
  onSelect,
}: {
  graph: GraphData | null;
  onSelect: (id: string) => void;
}) {
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<string | null>(null);

  const results: SearchResult[] = useMemo(() => {
    if (!graph) return [];
    return graph.nodes
      .map((node, index) => ({ node, index }))
      .filter(({ node }) => matchNode(node, query))
      .slice(0, 50);
  }, [graph, query]);

  return (
    <section className="panel" data-testid="search-panel" aria-label="Search">
      <h2 className="panel-title">Search</h2>
      <input
        className="search-input"
        type="search"
        placeholder="Filter nodes by name, file, type…"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setSelected(null);
        }}
        aria-label="Search graph nodes"
      />
      <ul className="search-results">
        {results.map(({ node, index }) => (
          <li key={node.id}>
            <button
              className={`search-result${selected === node.id ? ' selected' : ''}`}
              onClick={() => {
                setSelected(node.id);
                onSelect(node.id);
              }}
            >
              <span className="search-name">{node.properties.name || node.id}</span>
              <span className="search-file">{node.properties.filePath}</span>
              <span className="search-type">{node.properties.elementType}</span>
              <span className="search-rank">{index + 1}</span>
            </button>
          </li>
        ))}
      </ul>
      {results.length === 0 && query && <p className="panel-muted">No matches.</p>}
    </section>
  );
}
