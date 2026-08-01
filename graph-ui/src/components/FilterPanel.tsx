import { useMemo } from 'react';
import {
  relationshipTypes,
  totalCount,
  toggleType,
  visibleCount,
  type TypeFilter,
} from '../lib/filters';
import type { GraphData } from '../lib/types';

/**
 * FR-E31 — edge-type filter panel: toggle visibility by relationship type.
 */
export default function FilterPanel({
  graph,
  filter,
  onChange,
}: {
  graph: GraphData | null;
  filter: TypeFilter;
  onChange: (filter: TypeFilter) => void;
}) {
  const types = useMemo(() => relationshipTypes(graph), [graph]);
  if (types.length === 0) {
    return (
      <section className="panel" data-testid="filter-panel" aria-label="Edge filter">
        <h2 className="panel-title">Filter</h2>
        <p className="panel-muted">No relationships loaded.</p>
      </section>
    );
  }
  return (
    <section className="panel" data-testid="filter-panel" aria-label="Edge filter">
      <h2 className="panel-title">Edge filter</h2>
      <p className="filter-hint">
        {visibleCount(filter)} of {totalCount(filter)} types shown
      </p>
      <ul className="filter-list">
        {types.map((type) => (
          <li key={type}>
            <label className="filter-row">
              <input
                type="checkbox"
                checked={filter[type] ?? false}
                onChange={() => onChange(toggleType(filter, type))}
              />
              <span className="mono">{type}</span>
            </label>
          </li>
        ))}
      </ul>
    </section>
  );
}
