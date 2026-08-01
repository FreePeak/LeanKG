/** FR-UI2-08 / US-UI2-06 — Query FAB dual-mode helpers. */

export type QueryFabMode = 'nl' | 'advanced';

/** Default is NL so humans get the same cheap verb as agents (`query_graph`). */
export const DEFAULT_QUERY_FAB_MODE: QueryFabMode = 'nl';

export function queryEndpoint(mode: QueryFabMode): string {
  return mode === 'nl' ? '/api/query-graph' : '/api/query';
}

export function buildQueryBody(
  mode: QueryFabMode,
  text: string,
): { question: string } | { query: string } {
  if (mode === 'nl') {
    const question = text.trim();
    if (!question) {
      throw new Error('question must not be empty');
    }
    return { question };
  }
  const query = text; // preserve Cozo whitespace; only reject fully empty
  if (!query.trim()) {
    throw new Error('query must not be empty');
  }
  return { query };
}

export function queryPlaceholder(mode: QueryFabMode): string {
  return mode === 'nl'
    ? 'Natural language question, e.g. what connects auth to the database?'
    : '?[a, b] := ...';
}
