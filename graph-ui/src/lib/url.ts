/**
 * FR-E34 — URL-based routing: tab + project params survive refresh.
 * Pure helpers around window.location / history (injected so tests run in jsdom).
 */
export type TabId = 'graph' | 'search' | 'export';

export const TAB_PARAM = 'tab';
export const PROJECT_PARAM = 'project';
export const DEFAULT_TAB: TabId = 'graph';

export function parseTab(value: string | null): TabId {
  return value === 'search' || value === 'export' ? value : DEFAULT_TAB;
}

export function parseProject(value: string | null): string | undefined {
  const v = value?.trim();
  return v && v !== '/' && v !== '.' ? v : undefined;
}

export interface UrlState {
  tab: TabId;
  project?: string;
}

export function readUrlState(search: string): UrlState {
  const params = new URLSearchParams(search);
  return {
    tab: parseTab(params.get(TAB_PARAM)),
    project: parseProject(params.get(PROJECT_PARAM)),
  };
}

export function buildUrlSearch(state: UrlState): string {
  const params = new URLSearchParams();
  params.set(TAB_PARAM, state.tab);
  if (state.project) params.set(PROJECT_PARAM, state.project);
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

/** Write state to the URL without a reload (replaceState). */
export function writeUrlState(
  state: UrlState,
  replace: (url: string) => void = (url) => window.history.replaceState({}, '', url),
): void {
  replace(`${window.location.pathname}${buildUrlSearch(state)}`);
}
