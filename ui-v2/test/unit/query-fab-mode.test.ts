/**
 * FR-UI2-08 / US-UI2-06 — Query FAB dual-mode helpers.
 * Default NL → POST /api/query-graph; Advanced → POST /api/query (raw graph query).
 */
import { describe, expect, it } from 'vitest';
import {
  DEFAULT_QUERY_FAB_MODE,
  buildQueryBody,
  queryEndpoint,
  queryPlaceholder,
  type QueryFabMode,
} from '../../src/lib/query-fab-mode';

describe('query-fab-mode (FR-UI2-08)', () => {
  it('defaults to NL mode for the cheap verb', () => {
    expect(DEFAULT_QUERY_FAB_MODE).toBe('nl');
  });

  it('routes NL mode to /api/query-graph', () => {
    expect(queryEndpoint('nl')).toBe('/api/query-graph');
  });

  it('routes Advanced mode to raw /api/query', () => {
    expect(queryEndpoint('advanced')).toBe('/api/query');
  });

  it('builds NL body with question field', () => {
    expect(buildQueryBody('nl', '  what connects auth to db?  ')).toEqual({
      question: 'what connects auth to db?',
    });
  });

  it('builds Advanced body with query field (preserves original whitespace)', () => {
    const script = '?[a] := *code_elements{qualified_name: a}';
    expect(buildQueryBody('advanced', script)).toEqual({ query: script });
  });

  it('rejects blank NL questions', () => {
    expect(() => buildQueryBody('nl', '   ')).toThrow(/question/i);
  });

  it('rejects blank Advanced queries', () => {
    expect(() => buildQueryBody('advanced', '')).toThrow(/query/i);
  });

  it('shows NL-friendly placeholder by default', () => {
    expect(queryPlaceholder('nl')).toMatch(/connects|natural|question/i);
    expect(queryPlaceholder('advanced')).toMatch(/\?\[|:=/i);
  });

  it('only accepts known modes', () => {
    const modes: QueryFabMode[] = ['nl', 'advanced'];
    for (const m of modes) {
      expect(queryEndpoint(m)).toMatch(/^\/api\//);
    }
  });
});
