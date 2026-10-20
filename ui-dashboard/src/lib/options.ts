// Shared filter options. Values are the query strings the Go API accepts.

export const SINCE_OPTIONS = [
  { value: '24h', label: 'Last 24 hours' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: 'all', label: 'All time' },
] as const;

/** Clients the dashboard knows about (matches the sessionlink adapters). */
export const CLIENT_OPTIONS = ['claude-code', 'pi', 'omp', 'xdev', 'opencode', 'grok', 'codex', 'gemini-cli'] as const;

export const FAILURE_GROUPS = [
  { value: 'reason', label: 'Reason' },
  { value: 'tool', label: 'Tool' },
  { value: 'client', label: 'Client' },
] as const;
