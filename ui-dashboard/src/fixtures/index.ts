// Synthetic fixtures for `npm run dev` without a server. Every value here is
// invented; no real session, path, or user data appears in this file.
//
// Query flags: ?fixtures (metadata capture on), ?fixtures=off (consent screen
// opens first), ?fixtures=bodies.

import type { DashboardApi } from '@/api/client';
import { ApiError } from '@/api/client';
import type {
  CallRow,
  ConsentRequest,
  Evidence,
  Failures,
  Memory,
  MemoryRow,
  Overview,
  SessionDetail,
  SessionList,
  SessionSummary,
  Settings,
  Tools,
  Transcript,
} from '@/api/types';

const NOW = new Date('2026-10-10T09:30:00Z');
const iso = (minutesAgo: number) => new Date(NOW.getTime() - minutesAgo * 60_000).toISOString();

function dayKey(daysAgo: number): string {
  const d = new Date(NOW.getTime() - daysAgo * 86_400_000);
  return d.toISOString().slice(0, 10);
}

const SERIES_CALLS = [212, 248, 190, 301, 276, 263, 352];
const SERIES_ERR = [9, 12, 6, 14, 11, 8, 10];

function captureFromQuery(): string {
  try {
    const v = new URLSearchParams(window.location.search).get('fixtures');
    if (v === 'off') return 'off';
    if (v === 'bodies') return 'bodies';
  } catch {
    /* not in a browser */
  }
  return 'metadata';
}

function call(partial: Partial<CallRow> & Pick<CallRow, 'id' | 'tool' | 'action'>): CallRow {
  return {
    ts: iso(0),
    latency_ms: 84,
    transport: 'stdio',
    command: '',
    outcome: 'ok',
    outcome_reason: '',
    error_code: '',
    rung: 'L1',
    confidence: 'high',
    freshness: 'fresh',
    hits: 5,
    out_tokens: 640,
    baseline_tokens: 2480,
    baseline_method: 'median grep+read tokens for the same query shape (estimate)',
    tokens_saved: 1840,
    ...partial,
  };
}

const CALLS_A: CallRow[] = [
  call({ id: 'c-101', tool: 'query', action: 'search', command: 'handler for session linking', ts: iso(96), latency_ms: 61, hits: 6, signals: { hits_used: 4, hits_returned: 6, used_precision: 0.67, fallback: false, fallback_tools: [], requery: false, followed_guidance: true, abandoned_after: false } }),
  call({ id: 'c-102', tool: 'query', action: 'callers', command: 'linkSession', ts: iso(92), latency_ms: 44, rung: 'L2', hits: 3, tokens_saved: 960, signals: { hits_used: 1, hits_returned: 3, used_precision: 0.33, fallback: true, fallback_tools: ['grep'], requery: false, abandoned_after: false } }),
  call({ id: 'c-103', tool: 'query', action: 'search', command: 'token accounting baseline', ts: iso(88), outcome: 'empty', outcome_reason: 'no_hits', rung: 'L3', confidence: 'low', hits: 0, out_tokens: 120, tokens_saved: 0, latency_ms: 312, signals: { hits_used: 0, hits_returned: 0, used_precision: 0, fallback: true, fallback_tools: ['grep', 'read'], requery: true, abandoned_after: false } }),
  call({ id: 'c-104', tool: 'query', action: 'search', command: 'token accounting baseline', ts: iso(86), rung: 'L1', hits: 4, latency_ms: 58, signals: { hits_used: 3, hits_returned: 4, used_precision: 0.75, fallback: false, fallback_tools: [], requery: false, abandoned_after: false } }),
  call({ id: 'c-105', tool: 'import', action: 'repo', command: '.', outcome: 'error', outcome_reason: 'timeout', error_code: 'E_TIMEOUT', latency_ms: 30_000, hits: 0, out_tokens: 0, tokens_saved: 0, rung: '', confidence: '', freshness: 'stale' }),
  call({ id: 'c-106', tool: 'status', action: 'health', latency_ms: 12, hits: 0, out_tokens: 210, baseline_tokens: 210, tokens_saved: 0, rung: '', confidence: '', freshness: 'fresh' }),
  call({ id: 'c-107', tool: 'query', action: 'impact', command: 'store.Backend', hits: 9, latency_ms: 138, outcome: 'partial', outcome_reason: 'truncated', signals: { hits_used: 2, hits_returned: 9, used_precision: 0.22, fallback: false, fallback_tools: [], requery: false, abandoned_after: true } }),
];

const SESSIONS: SessionSummary[] = [
  { id: 's-7f3a', client_name: 'claude-code', client_session_id: 'cc-2c81', project: 'leankg', first_ts: iso(240), last_ts: iso(40), duration_s: 12_000, calls: 84, errors: 6, success_rate: 0.93, tokens_saved: 128_400, outcome_mix: [{ key: 'ok', count: 72 }, { key: 'partial', count: 6 }, { key: 'empty', count: 4 }, { key: 'error', count: 2 }], correlation: 'linked', link_status: 'linked', memory_recalls: 5 },
  { id: 's-91bd', client_name: 'pi', client_session_id: 'pi-0448', project: 'leankg', first_ts: iso(1500), last_ts: iso(1300), duration_s: 3_120, calls: 31, errors: 3, success_rate: 0.9, tokens_saved: 41_950, outcome_mix: [{ key: 'ok', count: 26 }, { key: 'empty', count: 3 }, { key: 'error', count: 2 }], correlation: 'linked', link_status: 'linked', memory_recalls: 0 },
  { id: 's-c02e', client_name: 'opencode', client_session_id: 'oc-a19f', project: 'sample-service', first_ts: iso(2900), last_ts: iso(2600), duration_s: 1_860, calls: 17, errors: 4, success_rate: 0.76, tokens_saved: 9_120, outcome_mix: [{ key: 'ok', count: 13 }, { key: 'error', count: 4 }], correlation: 'unlinked', link_status: 'not_granted', memory_recalls: 2 },
  { id: 's-4d11', client_name: 'claude-code', client_session_id: 'cc-9e05', project: 'sample-service', first_ts: iso(4320), last_ts: iso(4000), duration_s: 540, calls: 9, errors: 0, success_rate: 1, tokens_saved: 3_300, outcome_mix: [{ key: 'ok', count: 9 }], correlation: 'linked', link_status: 'linked', memory_recalls: 1 },
  { id: 's-aa70', client_name: 'claude-code', client_session_id: 'cc-1b77', project: 'web-app', first_ts: iso(7200), last_ts: iso(6900), duration_s: 2_400, calls: 22, errors: 1, success_rate: 0.95, tokens_saved: 17_850, outcome_mix: [{ key: 'ok', count: 19 }, { key: 'partial', count: 2 }, { key: 'error', count: 1 }], correlation: 'linked', link_status: 'linked', memory_recalls: 3 },
  { id: 's-e6b9', client_name: 'pi', client_session_id: 'pi-0511', project: 'web-app', first_ts: iso(9800), last_ts: iso(9700), duration_s: 260, calls: 4, errors: 0, success_rate: 1, tokens_saved: 1_020, outcome_mix: [{ key: 'ok', count: 4 }], correlation: 'unlinked', link_status: 'not_granted', memory_recalls: 0 },
];

const MEMORY_RECENT: MemoryRow[] = [
  { id: 'm-1', ts: iso(30), verb: 'recall', transport: 'hindsight', banks: 'project:leankg', returned: 4, written: 0, skipped: 0, replaced: 0, deleted: 0, age_max_s: 864_000, latency_ms: 38 },
  { id: 'm-2', ts: iso(55), verb: 'retain', transport: 'hindsight', banks: 'project:leankg', returned: 0, written: 2, skipped: 1, replaced: 0, deleted: 0, age_max_s: 0, latency_ms: 91 },
  { id: 'm-3', ts: iso(140), verb: 'recall', transport: 'hindsight', banks: 'global', returned: 0, written: 0, skipped: 0, replaced: 0, deleted: 0, age_max_s: 0, latency_ms: 27 },
  { id: 'm-4', ts: iso(300), verb: 'retain', transport: 'mcp', banks: 'project:web-app', returned: 0, written: 1, skipped: 0, replaced: 1, deleted: 0, age_max_s: 0, latency_ms: 74, error: 'embedding backend busy; written without vector' },
];

const SESSION_CALLS = CALLS_A;

function transcriptFor(id: string): Transcript {
  const tool = (toolCallId: string, toolName: string, argsText: string, result: string, leankg?: CallRow, fallback = false, isError = false) => ({
    type: 'tool-call' as const,
    toolCallId,
    toolName,
    argsText,
    result,
    isError,
    leankg,
    fallback,
  });
  const messages = [
    { id: 'u1', role: 'user', createdAt: iso(96), tokens: 42, content: [{ type: 'text' as const, text: 'Where is session linking wired into the MCP path?' }] },
    {
      id: 'a1',
      role: 'assistant',
      createdAt: iso(95),
      tokens: 380,
      content: [
        { type: 'tool-call' as const, tool: tool('t1', 'mcp__leankg__query', '{"query":"session linking","action":"search"}', 'handler list: linkerLoop, Linker.Run', SESSION_CALLS[0]) },
        { type: 'text' as const, text: 'The linker loop starts from the dashboard process, not from the MCP handler.' },
      ],
    },
    {
      id: 'a2',
      role: 'assistant',
      createdAt: iso(92),
      tokens: 260,
      content: [
        { type: 'tool-call' as const, tool: tool('t2', 'mcp__leankg__query', '{"query":"linkSession","action":"callers"}', '2 callers', SESSION_CALLS[1], true) },
        { type: 'tool-call' as const, tool: tool('t3', 'Grep', '{"pattern":"linkSession"}', 'internal/sessionlink/link.go:41', undefined, true) },
      ],
    },
    {
      id: 'a3',
      role: 'assistant',
      createdAt: iso(88),
      tokens: 150,
      content: [
        { type: 'tool-call' as const, tool: tool('t4', 'mcp__leankg__query', '{"query":"token accounting baseline","action":"search"}', 'no results', SESSION_CALLS[2], false, true) },
        { type: 'tool-call' as const, tool: tool('t5', 'Read', '{"file_path":"internal/telemetry/tokens.go"}', 'package telemetry ...') },
      ],
    },
  ];
  return {
    session_id: id,
    synthetic: id === 's-c02e',
    reason: id === 's-c02e' ? 'only captured LeanKG calls; grant transcript reading in Settings to see the full session' : undefined,
    messages: id === 's-c02e' ? [messages[0], messages[1]].map((m) => ({ ...m, content: m.content.filter((p) => p.type === 'tool-call') })) : messages,
  };
}

function settingsFor(capture: string): Settings {
  const granted = capture !== 'off';
  return {
    capture,
    sessions_on: granted,
    clients: [
      { client: 'claude-code', found: true, roots: ['~/.claude/projects'], granted },
      { client: 'pi', found: true, roots: ['~/.pi/agent/sessions'], granted: false },
      { client: 'opencode', found: true, roots: ['~/.local/share/opencode/opencode.db'], granted: false },
      { client: 'codex', found: false, roots: [], granted: false },
      { client: 'gemini-cli', found: false, roots: [], granted: false },
    ],
    retention_days: 30,
    max_body_bytes: 4096,
    granted_at: granted ? iso(60 * 24 * 3) : undefined,
    granted_by: granted ? 'dashboard' : undefined,
    consent_current: granted,
    config_path: '~/.leankg/telemetry.yaml',
    db_path: '~/.leankg/telemetry.db',
    db_size_bytes: 18_432_000,
    dropped_events: 3,
    csrf_token: 'fixture-csrf-token',
  };
}

function overviewFor(capture: string): Overview {
  return {
    since: iso(7 * 24 * 60),
    kpis: [
      { key: 'calls', label: 'LeanKG calls', value: 1842, unit: '', estimate: false },
      { key: 'success_rate', label: 'Success rate', value: 93.4, unit: '%', estimate: false },
      { key: 'tokens_saved', label: 'Tokens saved', value: 412_300, unit: 'tokens', estimate: true, method: 'baseline = median grep+read tokens for the same query shape, from this ledger (uncontrolled; see Evidence)' },
      { key: 'p95_latency', label: 'p95 latency', value: 412, unit: 'ms', estimate: false },
      { key: 'sessions', label: 'Sessions', value: 38, unit: '', estimate: false },
    ],
    series: SERIES_CALLS.map((calls, i) => ({ day: dayKey(6 - i), calls, errors: SERIES_ERR[i], tokens_saved: calls * 1900, sessions: 4 + (i % 3) })),
    top_failures: [
      { key: 'no_hits', count: 58 },
      { key: 'timeout', count: 24 },
      { key: 'stale_index', count: 11 },
    ],
    by_client: [
      { key: 'claude-code', count: 1204 },
      { key: 'pi', count: 410 },
      { key: 'opencode', count: 228 },
    ],
    outcome_mix: [
      { key: 'ok', count: 1640 },
      { key: 'partial', count: 120 },
      { key: 'empty', count: 58 },
      { key: 'error', count: 24 },
    ],
    capture_level: capture,
    sessions_on: capture !== 'off',
  };
}

const FAILURES: Record<string, Failures> = {
  reason: {
    group: 'reason',
    total: 93,
    groups: [
      { key: 'no_hits', count: 58, share: 58 / 93, guidance: 'Query by the qualified name from a prior search, or import the repo if the index is cold.', samples: [CALLS_A[2]] },
      { key: 'timeout', count: 24, share: 24 / 93, guidance: 'Narrow the query or raise LEANKG_MCP_TOOL_TIMEOUT_SECS on large graphs.', samples: [CALLS_A[4]] },
      { key: 'stale_index', count: 11, share: 11 / 93, guidance: 'Run import action=repo to refresh the index.', samples: [] },
    ],
  },
  tool: {
    group: 'tool',
    total: 93,
    groups: [
      { key: 'query', count: 66, share: 66 / 93, samples: [CALLS_A[2]] },
      { key: 'import', count: 19, share: 19 / 93, samples: [CALLS_A[4]] },
      { key: 'status', count: 8, share: 8 / 93, samples: [] },
    ],
  },
  client: {
    group: 'client',
    total: 93,
    groups: [
      { key: 'claude-code', count: 51, share: 51 / 93, samples: [] },
      { key: 'pi', count: 22, share: 22 / 93, samples: [] },
      { key: 'opencode', count: 20, share: 20 / 93, samples: [] },
    ],
  },
};

const TOOLS: Tools = {
  tools: [
    { tool: 'query', action: 'search', calls: 1120, success_rate: 0.95, p50_ms: 52, p95_ms: 188, rung_mix: [{ key: 'L1', count: 812 }, { key: 'L2', count: 201 }, { key: 'L3', count: 107 }], outcome_mix: [{ key: 'ok', count: 1040 }, { key: 'empty', count: 58 }, { key: 'partial', count: 22 }], tokens_saved: 260_400 },
    { tool: 'query', action: 'callers', calls: 300, success_rate: 0.97, p50_ms: 31, p95_ms: 96, rung_mix: [{ key: 'L1', count: 280 }, { key: 'L2', count: 20 }], outcome_mix: [{ key: 'ok', count: 291 }, { key: 'partial', count: 9 }], tokens_saved: 74_100 },
    { tool: 'query', action: 'impact', calls: 96, success_rate: 0.84, p50_ms: 138, p95_ms: 612, rung_mix: [{ key: 'L1', count: 96 }], outcome_mix: [{ key: 'ok', count: 81 }, { key: 'partial', count: 15 }], tokens_saved: 33_800 },
    { tool: 'import', action: 'repo', calls: 19, success_rate: 0.58, p50_ms: 8_400, p95_ms: 30_000, rung_mix: [], outcome_mix: [{ key: 'ok', count: 11 }, { key: 'error', count: 8 }], tokens_saved: 0 },
    { tool: 'status', action: 'health', calls: 307, success_rate: 1, p50_ms: 9, p95_ms: 22, rung_mix: [], outcome_mix: [{ key: 'ok', count: 307 }], tokens_saved: 0 },
  ],
};

const MEMORY: Memory = {
  kpis: [
    { key: 'recalls', label: 'Recall calls', value: 412, unit: '', estimate: false },
    { key: 'recall_hit_rate', label: 'Recall hit rate', value: 61.2, unit: '%', estimate: false },
    { key: 'retained', label: 'Lessons retained', value: 88, unit: '', estimate: false },
    { key: 'never_recalled', label: 'Never recalled', value: 34, unit: '%', estimate: true, method: 'proxy: share of bank entries with no recall in the window; a per-id recall counter is not yet recorded' },
  ],
  banks: [
    { bank: 'project:leankg', recalls: 290, hit_rate: 0.66, written: 61, never_recalled_share: 0.29 },
    { bank: 'project:web-app', recalls: 84, hit_rate: 0.48, written: 19, never_recalled_share: 0.41 },
    { bank: 'global', recalls: 38, hit_rate: 0.31, written: 8, never_recalled_share: 0.52 },
  ],
  recent: MEMORY_RECENT,
};

const EVIDENCE_OBS: Evidence = {
  observational: [
    { metric: 'Latency per session', unit: 'ms', with: { n: 38, median: 1420, p25: 880, p75: 2310 }, without: { n: 21, median: 1180, p25: 640, p75: 1990 } },
    { metric: 'Tool calls per task', unit: 'calls', with: { n: 38, median: 22, p25: 11, p75: 41 }, without: { n: 21, median: 30, p25: 16, p75: 52 } },
  ],
  observational_note: 'Estimate, confounded: not causal. Sessions that use LeanKG differ in task size, project, and client. Read these as descriptions, not effects.',
  controlled: [],
  controlled_runs: 0,
  controlled_valid: false,
  controlled_note: 'No controlled A/B run is on record. Run the paired benchmark to produce measured figures.',
};

function sessionDetail(id: string): SessionDetail {
  const session = SESSIONS.find((s) => s.id === id);
  if (!session) throw new ApiError(404, 'not_found', `No session with id ${id}.`);
  return {
    session,
    kpis: [
      { key: 'calls', label: 'Calls', value: session.calls, unit: '', estimate: false },
      { key: 'success_rate', label: 'Success rate', value: session.success_rate * 100, unit: '%', estimate: false },
      { key: 'tokens_saved', label: 'Tokens saved', value: session.tokens_saved, unit: 'tokens', estimate: true, method: 'baseline = median grep+read tokens for the same query shape (estimate)' },
    ],
    calls: SESSION_CALLS,
    memory: MEMORY_RECENT.slice(0, 2),
    failures: [
      { key: 'no_hits', count: 1 },
      { key: 'timeout', count: 1 },
    ],
    linked: session.link_status === 'linked',
  };
}

function matchSessions(q: { client?: string; project?: string }): SessionSummary[] {
  return SESSIONS.filter((s) => (!q.client || s.client_name === q.client) && (!q.project || s.project === q.project));
}

/** In-memory fixture API. Consent writes update the in-memory settings only. */
export function createFixtureApi(): DashboardApi {
  let capture = captureFromQuery();
  let granted: string[] = capture === 'off' ? [] : ['claude-code'];
  const delay = <T,>(value: T): Promise<T> => new Promise((resolve) => setTimeout(() => resolve(value), 120));
  const settings = () => {
    const s = settingsFor(capture);
    s.clients = s.clients.map((c) => ({ ...c, granted: granted.includes(c.client) }));
    s.sessions_on = capture !== 'off' && granted.length > 0;
    return s;
  };

  return {
    overview: async () => delay(overviewFor(capture)),
    sessions: async (q) => {
      const rows = matchSessions(q ?? {});
      const list: SessionList = { total: rows.length, sessions: rows };
      return delay(list);
    },
    session: async (id) => delay(sessionDetail(id)),
    transcript: async (id) => {
      if (!SESSIONS.some((s) => s.id === id)) throw new ApiError(404, 'not_found', `No session with id ${id}.`);
      return delay(transcriptFor(id));
    },
    failures: async (group) => {
      const f = FAILURES[group];
      if (!f) throw new ApiError(400, 'bad_group', `group must be reason, tool or client (got ${group}).`);
      return delay(f);
    },
    tools: async () => delay(TOOLS),
    memory: async () => delay(MEMORY),
    evidence: async () => delay(EVIDENCE_OBS),
    consent: async () => delay(settings()),
    postConsent: async (req: ConsentRequest, csrf) => {
      if (csrf !== 'fixture-csrf-token') throw new ApiError(403, 'csrf', 'Missing or invalid CSRF token.');
      capture = req.capture;
      granted = req.sessions;
      if (req.capture === 'off' && req.purge) granted = [];
      await delay(undefined);
    },
  };
}
