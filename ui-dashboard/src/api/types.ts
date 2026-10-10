// Mirror of internal/telemetry/report/report.go (the API contract, plan v4.15
// DS-21). Keep field names identical to the Go json tags. Go time.Time values
// arrive as RFC 3339 strings. Pointer fields with omitempty are optional here.
// Lists are never null on the wire (RS-09); the UI still guards against
// missing lists so a partial payload cannot crash a page.

export interface KPI {
  key: string;
  label: string;
  value: number;
  /** "", "%", "ms", "tokens". For "%" the value is already in percent points (0-100). */
  unit: string;
  /** Render an "estimate" badge. */
  estimate: boolean;
  method?: string;
}

export interface Count {
  key: string;
  count: number;
}

export interface DayPoint {
  /** YYYY-MM-DD, local time. */
  day: string;
  calls: number;
  errors: number;
  tokens_saved: number;
  sessions: number;
}

export interface Overview {
  since: string;
  kpis: KPI[];
  series: DayPoint[];
  top_failures: Count[];
  by_client: Count[];
  outcome_mix: Count[];
  capture_level: string;
  sessions_on: boolean;
}

export interface SessionSummary {
  id: string;
  client_name: string;
  client_session_id: string;
  project: string;
  first_ts: string;
  last_ts: string;
  duration_s: number;
  calls: number;
  errors: number;
  /** 0..1 ratio, not percent. */
  success_rate: number;
  tokens_saved: number;
  outcome_mix: Count[];
  correlation: string;
  link_status: string;
  memory_recalls: number;
}

export interface SessionList {
  total: number;
  sessions: SessionSummary[];
}

export interface Signals {
  hits_used: number;
  hits_returned: number;
  /** HitsUsed / HitsReturned, 0..1. A proxy for context use, not ground truth. */
  used_precision: number;
  /** grep/glob/read outside the hits within the next K calls. */
  fallback: boolean;
  fallback_tools: string[];
  requery: boolean;
  followed_guidance?: boolean;
  abandoned_after: boolean;
}

export interface CallRow {
  id: string;
  ts: string;
  latency_ms: number;
  transport: string;
  tool: string;
  action: string;
  command: string;
  outcome: string;
  outcome_reason: string;
  error_code: string;
  rung: string;
  confidence: string;
  freshness: string;
  hits: number;
  out_tokens: number;
  baseline_tokens: number;
  baseline_method: string;
  tokens_saved: number;
  /** Bodies level only. */
  args?: string;
  /** Bodies level only. */
  body?: string;
  /** Present only when the call is linked to a transcript. */
  signals?: Signals;
}

export interface MemoryRow {
  id: string;
  ts: string;
  verb: string;
  transport: string;
  banks: string;
  returned: number;
  written: number;
  skipped: number;
  replaced: number;
  deleted: number;
  age_max_s: number;
  latency_ms: number;
  error?: string;
}

export interface SessionDetail {
  session: SessionSummary;
  kpis: KPI[];
  calls: CallRow[];
  memory: MemoryRow[];
  failures: Count[];
  linked: boolean;
}

/** assistant-ui tool-call content part, plus LeanKG enrichment. */
export interface ToolCallPart {
  type: 'tool-call';
  toolCallId: string;
  toolName: string;
  argsText: string;
  result?: string;
  isError: boolean;
  /** LeanKG enrichment, present only for LeanKG calls. */
  leankg?: CallRow;
  /** A post-LeanKG search outside the hits (DS-17). */
  fallback: boolean;
}

export interface MessagePart {
  type: 'text' | 'tool-call';
  text?: string;
  tool?: ToolCallPart;
}

export interface Message {
  id: string;
  role: 'user' | 'assistant' | (string & {});
  createdAt: string;
  content: MessagePart[];
  tokens: number;
}

export interface Transcript {
  session_id: string;
  /** True when only captured LeanKG calls are shown (no sessions grant). */
  synthetic: boolean;
  reason?: string;
  messages: Message[];
}

export interface FailureGroup {
  key: string;
  count: number;
  /** 0..1 ratio. */
  share: number;
  guidance?: string;
  samples: CallRow[];
}

export interface Failures {
  group: string;
  total: number;
  groups: FailureGroup[];
}

export interface ToolStat {
  tool: string;
  action: string;
  calls: number;
  /** 0..1 ratio. */
  success_rate: number;
  p50_ms: number;
  p95_ms: number;
  rung_mix: Count[];
  outcome_mix: Count[];
  tokens_saved: number;
}

export interface Tools {
  tools: ToolStat[];
}

export interface BankStat {
  bank: string;
  recalls: number;
  /** 0..1 ratio. */
  hit_rate: number;
  written: number;
  /** 0..1 ratio. A proxy until a per-id recall counter exists. */
  never_recalled_share: number;
}

export interface Memory {
  kpis: KPI[];
  banks: BankStat[];
  recent: MemoryRow[];
}

export interface Dist {
  n: number;
  median: number;
  p25: number;
  p75: number;
}

export interface ArmCompare {
  metric: string;
  unit: string;
  with: Dist;
  without: Dist;
}

export interface Evidence {
  observational: ArmCompare[];
  observational_note: string;
  controlled: ArmCompare[];
  controlled_runs: number;
  controlled_valid: boolean;
  controlled_note: string;
}

export interface ClientStore {
  client: string;
  found: boolean;
  roots: string[];
  granted: boolean;
}

export interface Settings {
  capture: 'off' | 'metadata' | 'bodies' | (string & {});
  sessions_on: boolean;
  clients: ClientStore[];
  retention_days: number;
  max_body_bytes: number;
  granted_at?: string;
  granted_by?: string;
  consent_current: boolean;
  config_path: string;
  db_path: string;
  db_size_bytes: number;
  dropped_events: number;
  csrf_token: string;
}

/** POST /consent body. */
export interface ConsentRequest {
  /** off | metadata | bodies */
  capture: 'off' | 'metadata' | 'bodies';
  /** Client allowlist; empty disables transcript reading. */
  sessions: string[];
  /** With capture=off: also delete the ledger. */
  purge: boolean;
}

export interface SessionsQuery {
  client?: string;
  project?: string;
  since?: string;
  outcome?: string;
}
