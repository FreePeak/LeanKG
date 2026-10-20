// Package report holds the JSON shapes of the dashboard API
// (/api/dashboard/v1, plan v4.15 DS-21). The metrics engine produces them,
// internal/dashboard serves them, and ui-dashboard/src/api/types.ts mirrors
// them field for field. Lists are never nil (RS-09: no `null` lists).
//
// Every counterfactual number carries a Method and is an estimate; only
// ABPanel figures are "measured" (plan §2, rule 5).
package report

import "time"

// KPI is one stat tile.
type KPI struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit"`             // "", "%", "ms", "tokens"
	Estimate bool    `json:"estimate"`         // render an "estimate" badge
	Method   string  `json:"method,omitempty"` // how it was computed
}

// Count is a labelled count, used for mixes and rankings.
type Count struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// DayPoint is one day of the overview series.
type DayPoint struct {
	Day         string `json:"day"` // YYYY-MM-DD, local time
	Calls       int64  `json:"calls"`
	Errors      int64  `json:"errors"`
	TokensSaved int64  `json:"tokens_saved"`
	Sessions    int64  `json:"sessions"`
}

// Overview is GET /overview.
type Overview struct {
	Since        time.Time  `json:"since"`
	KPIs         []KPI      `json:"kpis"`
	Series       []DayPoint `json:"series"`
	TopFailures  []Count    `json:"top_failures"`
	ByClient     []Count    `json:"by_client"`
	OutcomeMix   []Count    `json:"outcome_mix"`
	CaptureLevel string     `json:"capture_level"`
	SessionsOn   bool       `json:"sessions_on"`
}

// SessionSummary is one row of GET /sessions.
type SessionSummary struct {
	ID              string    `json:"id"`
	ClientName      string    `json:"client_name"`
	ClientSessionID string    `json:"client_session_id"`
	Project         string    `json:"project"`
	FirstTS         time.Time `json:"first_ts"`
	LastTS          time.Time `json:"last_ts"`
	DurationS       float64   `json:"duration_s"`
	Calls           int64     `json:"calls"`
	Errors          int64     `json:"errors"`
	SuccessRate     float64   `json:"success_rate"` // 0..1
	TokensSaved     int64     `json:"tokens_saved"`
	OutcomeMix      []Count   `json:"outcome_mix"`
	Correlation     string    `json:"correlation"`
	LinkStatus      string    `json:"link_status"`
	MemoryRecalls   int64     `json:"memory_recalls"`
}

// SessionList is GET /sessions.
type SessionList struct {
	Total    int              `json:"total"`
	Sessions []SessionSummary `json:"sessions"`
}

// CallRow is one call as the UI shows it.
type CallRow struct {
	ID             string    `json:"id"`
	TS             time.Time `json:"ts"`
	LatencyMS      int64     `json:"latency_ms"`
	Transport      string    `json:"transport"`
	Tool           string    `json:"tool"`
	Action         string    `json:"action"`
	Command        string    `json:"command"`
	Outcome        string    `json:"outcome"`
	OutcomeReason  string    `json:"outcome_reason"`
	ErrorCode      string    `json:"error_code"`
	Rung           string    `json:"rung"`
	Confidence     string    `json:"confidence"`
	Freshness      string    `json:"freshness"`
	Hits           int       `json:"hits"`
	OutTokens      int64     `json:"out_tokens"`
	BaselineTokens int64     `json:"baseline_tokens"`
	BaselineMethod string    `json:"baseline_method"`
	TokensSaved    int64     `json:"tokens_saved"`
	Args           string    `json:"args,omitempty"` // Bodies level only
	Body           string    `json:"body,omitempty"` // Bodies level only
	Signals        *Signals  `json:"signals,omitempty"`
}

// Signals are the transcript-derived context-use proxies for one call
// (DS-17). Present only when the call is linked.
type Signals struct {
	HitsUsed       int      `json:"hits_used"`
	HitsReturned   int      `json:"hits_returned"`
	UsedPrecision  float64  `json:"used_precision"` // HitsUsed / HitsReturned
	Fallback       bool     `json:"fallback"`       // grep/glob/read outside hits within next K calls
	FallbackTools  []string `json:"fallback_tools"`
	Requery        bool     `json:"requery"`
	FollowedGuide  *bool    `json:"followed_guidance,omitempty"`
	AbandonedAfter bool     `json:"abandoned_after"`
}

// SessionDetail is GET /sessions/{id}.
type SessionDetail struct {
	Session  SessionSummary `json:"session"`
	KPIs     []KPI          `json:"kpis"`
	Calls    []CallRow      `json:"calls"`
	Memory   []MemoryRow    `json:"memory"`
	Failures []Count        `json:"failures"`
	Linked   bool           `json:"linked"`
}

// ToolCallPart mirrors assistant-ui's tool-call content part.
type ToolCallPart struct {
	Type       string `json:"type"` // "tool-call"
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	ArgsText   string `json:"argsText"`
	Result     string `json:"result,omitempty"`
	IsError    bool   `json:"isError"`
	// LeanKG enrichment, present only for LeanKG calls.
	LeanKG *CallRow `json:"leankg,omitempty"`
	// Fallback marks a post-LeanKG search outside the hits (DS-17).
	Fallback bool `json:"fallback"`
}

// MessagePart is a text or tool-call part of a transcript message.
type MessagePart struct {
	Type string        `json:"type"` // "text" | "tool-call"
	Text string        `json:"text,omitempty"`
	Tool *ToolCallPart `json:"tool,omitempty"`
}

// Message mirrors assistant-ui's ThreadMessageLike (role, content, id, createdAt).
type Message struct {
	ID        string        `json:"id"`
	Role      string        `json:"role"` // user | assistant
	CreatedAt time.Time     `json:"createdAt"`
	Content   []MessagePart `json:"content"`
	Tokens    int64         `json:"tokens"`
}

// Transcript is GET /sessions/{id}/transcript. When the session is not
// linked (no sessions grant), Messages holds a synthetic call timeline built
// from captured LeanKG calls only and Synthetic is true.
type Transcript struct {
	SessionID string    `json:"session_id"`
	Synthetic bool      `json:"synthetic"`
	Reason    string    `json:"reason,omitempty"`
	Messages  []Message `json:"messages"`
}

// FailureGroup is one group of GET /failures.
type FailureGroup struct {
	Key      string    `json:"key"`
	Count    int64     `json:"count"`
	Share    float64   `json:"share"`
	Guidance string    `json:"guidance,omitempty"`
	Samples  []CallRow `json:"samples"`
}

// Failures is GET /failures?group=reason|tool|client.
type Failures struct {
	Group  string         `json:"group"`
	Total  int64          `json:"total"`
	Groups []FailureGroup `json:"groups"`
}

// ToolStat is one row of the Tools page.
type ToolStat struct {
	Tool        string  `json:"tool"`
	Action      string  `json:"action"`
	Calls       int64   `json:"calls"`
	SuccessRate float64 `json:"success_rate"`
	P50MS       float64 `json:"p50_ms"`
	P95MS       float64 `json:"p95_ms"`
	RungMix     []Count `json:"rung_mix"`
	OutcomeMix  []Count `json:"outcome_mix"`
	TokensSaved int64   `json:"tokens_saved"`
}

// Tools is GET /tools.
type Tools struct {
	Tools []ToolStat `json:"tools"`
}

// MemoryRow is one memory event as the UI shows it.
type MemoryRow struct {
	ID        string    `json:"id"`
	TS        time.Time `json:"ts"`
	Verb      string    `json:"verb"`
	Transport string    `json:"transport"`
	Banks     string    `json:"banks"`
	Returned  int       `json:"returned"`
	Written   int       `json:"written"`
	Skipped   int       `json:"skipped"`
	Replaced  int       `json:"replaced"`
	Deleted   int       `json:"deleted"`
	AgeMaxS   int64     `json:"age_max_s"`
	LatencyMS int64     `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
}

// BankStat is one bank on the Memory page.
type BankStat struct {
	Bank          string  `json:"bank"`
	Recalls       int64   `json:"recalls"`
	HitRate       float64 `json:"hit_rate"`
	Written       int64   `json:"written"`
	NeverRecalled float64 `json:"never_recalled_share"` // proxy until a per-id counter exists
}

// Memory is GET /memory.
type Memory struct {
	KPIs   []KPI       `json:"kpis"`
	Banks  []BankStat  `json:"banks"`
	Recent []MemoryRow `json:"recent"`
}

// Dist is a median + IQR + n summary.
type Dist struct {
	N      int     `json:"n"`
	Median float64 `json:"median"`
	P25    float64 `json:"p25"`
	P75    float64 `json:"p75"`
}

// ArmCompare compares one metric between with-LeanKG and without.
type ArmCompare struct {
	Metric  string `json:"metric"`
	Unit    string `json:"unit"`
	With    Dist   `json:"with"`
	Without Dist   `json:"without"`
}

// Evidence is GET /evidence: the observational panel (estimate, confounded)
// and the controlled A/B panel (the only "measured" figures).
type Evidence struct {
	Observational     []ArmCompare `json:"observational"`
	ObservationalNote string       `json:"observational_note"`
	Controlled        []ArmCompare `json:"controlled"`
	ControlledRuns    int          `json:"controlled_runs"`
	ControlledValid   bool         `json:"controlled_valid"`
	ControlledNote    string       `json:"controlled_note"`
}

// ClientStore is one detected agent store shown on the consent screen.
type ClientStore struct {
	Client  string   `json:"client"`
	Found   bool     `json:"found"`
	Roots   []string `json:"roots"`
	Granted bool     `json:"granted"`
}

// Settings is GET /consent (and the Settings page).
type Settings struct {
	Capture        string        `json:"capture"`
	SessionsOn     bool          `json:"sessions_on"`
	Clients        []ClientStore `json:"clients"`
	RetentionDays  int           `json:"retention_days"`
	MaxBodyBytes   int           `json:"max_body_bytes"`
	GrantedAt      *time.Time    `json:"granted_at,omitempty"`
	GrantedBy      string        `json:"granted_by,omitempty"`
	ConsentCurrent bool          `json:"consent_current"`
	ConfigPath     string        `json:"config_path"`
	DBPath         string        `json:"db_path"`
	DBSizeBytes    int64         `json:"db_size_bytes"`
	DroppedEvents  int64         `json:"dropped_events"`
	CSRFToken      string        `json:"csrf_token"`
}

// ConsentRequest is POST /consent.
type ConsentRequest struct {
	Capture  string   `json:"capture"`  // off | metadata | bodies
	Sessions []string `json:"sessions"` // client allowlist; empty disables transcript reading
	Purge    bool     `json:"purge"`    // with capture=off: also delete the ledger
}
