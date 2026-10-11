// Package telemetry records what the LeanKG servers saw — MCP calls, REST /
// Hindsight / ConnectRPC calls and agent-memory events — into a per-user
// ledger (telemetry.db under Home()) for the `leankg dashboard` efficiency
// metrics (plan v4.15, DS-01..DS-09).
//
// Capture is consent-gated and off by default (DS-01): with Level Off no
// file is created, no queue exists and Recorder is a no-op. Capture never
// changes a response and never fails a call: the hot path only enqueues,
// and a full queue drops the event and counts it.
package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Level is how much a server records. The zero value is Off.
type Level string

const (
	// Off records nothing and creates nothing (the default).
	Off Level = "off"
	// Metadata records outcome, sizes, latency, identity, arg keys and hashes.
	Metadata Level = "metadata"
	// Bodies adds redacted, capped request args and response bodies.
	Bodies Level = "bodies"
)

// Rank orders levels so an env override can only lower consent.
func (l Level) Rank() int {
	switch l {
	case Metadata:
		return 1
	case Bodies:
		return 2
	default:
		return 0
	}
}

// ConsentVersion is bumped whenever what a level records changes; a stored
// consent with an older version is treated as no consent (re-prompt).
const ConsentVersion = 1

// Config is $LEANKG_HOME/telemetry.yaml.
type Config struct {
	Capture       Level          `yaml:"capture" json:"capture"`
	Sessions      SessionsConfig `yaml:"sessions" json:"sessions"`
	RetentionDays int            `yaml:"retention_days" json:"retention_days"`
	MaxBodyBytes  int            `yaml:"max_body_bytes" json:"max_body_bytes"`
	Consent       Consent        `yaml:"consent" json:"consent"`
}

// SessionsConfig is the separate, per-client grant to read agent transcripts.
type SessionsConfig struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Clients []string `yaml:"clients" json:"clients"`
}

// Consent is written only by an explicit user action (CLI or dashboard).
type Consent struct {
	GrantedAt time.Time `yaml:"granted_at,omitempty" json:"granted_at,omitzero"`
	GrantedBy string    `yaml:"granted_by,omitempty" json:"granted_by,omitempty"` // "cli" | "dashboard"
	Version   int       `yaml:"version,omitempty" json:"version,omitempty"`
}

// Default retention and body cap (DS-01).
const (
	DefaultRetentionDays = 30
	DefaultMaxBodyBytes  = 16384
)

// Home is the per-user LeanKG directory: $LEANKG_HOME, else $HOME/.leankg.
// The dashboard reads only the telemetry DB under its own Home (isolation).
func Home() string {
	if h := os.Getenv("LEANKG_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".leankg"
	}
	return filepath.Join(home, ".leankg")
}

// ConfigPath is the telemetry config file under home.
func ConfigPath(home string) string { return filepath.Join(home, "telemetry.yaml") }

// DBPath is the telemetry ledger under home.
func DBPath(home string) string { return filepath.Join(home, "telemetry.db") }

// Transport names where a call entered the server.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportREST  = "rest"
	TransportHS    = "hindsight"
	TransportRPC   = "rpc"
)

// MCP methods the capture middleware records. Handshakes carry identity but
// are not tool calls, so the metrics leave them out of call counts.
const (
	MethodInitialize = "initialize"
	MethodDiscover   = "server/discover"
	MethodToolsCall  = "tools/call"
)

// IsHandshake reports whether the row is an MCP handshake, not a tool call.
func (c CallEvent) IsHandshake() bool {
	return c.Method == MethodInitialize || c.Method == MethodDiscover
}

// Correlation says how a call can be joined to an agent transcript.
const (
	CorrExact     = "exact"     // a client session id was present
	CorrHeuristic = "heuristic" // cwd + time window + tool + args hash
	CorrNone      = "none"
)

// Outcome labels (DS-06). Error outcomes are "error:<ERRS_CODE>".
const (
	OutcomeOK          = "ok"
	OutcomeLowConf     = "ok_low_confidence"
	OutcomeDegraded    = "degraded"
	OutcomeZeroHit     = "zero_hit"
	OutcomeCold        = "cold"
	OutcomeStale       = "stale"
	OutcomeRefused     = "refused"
	OutcomeTimeout     = "timeout"
	OutcomeErrorPrefix = "error:"
)

// Baseline methods for the tokens-saved counterfactual (DS-07).
const (
	BaselineFileRead = "file_read"
	BaselineSLOC     = "sloc"
	BaselineNone     = "none"
)

// Identity is who called: the coding agent and, when known, its session.
type Identity struct {
	ClientName      string `json:"client_name"`    // "claude-code", "opencode", "pi", ... or "unknown"
	ClientVersion   string `json:"client_version"` // from MCP clientInfo or User-Agent
	ClientSessionID string `json:"client_session_id,omitempty"`
	Cwd             string `json:"cwd,omitempty"` // agent working dir when known (stdio: server cwd)
}

// Correlation returns CorrExact when a session id is present, else heuristic.
func (i Identity) Correlation() string {
	if i.ClientSessionID != "" {
		return CorrExact
	}
	return CorrHeuristic
}

// CallEvent is one captured call (MCP tools/call or initialize, REST,
// Hindsight, RPC). Body fields are empty below Level Bodies.
type CallEvent struct {
	ID        string    `json:"id"` // ULID-like, time-sortable
	TS        time.Time `json:"ts"`
	LatencyMS int64     `json:"latency_ms"`
	Transport string    `json:"transport"`
	Method    string    `json:"method"` // "tools/call", "initialize", "POST /api/v1/query", ...
	Tool      string    `json:"tool"`   // import|query|status, or route family
	Action    string    `json:"action"`
	Command   string    `json:"command"`
	Project   string    `json:"project"`
	// SessionID is the ledger Session.ID; the store assigns it on insert
	// when empty (exact by client session id, else a cwd + 30-min-gap group).
	SessionID string `json:"session_id"`
	Identity
	Correlation string `json:"correlation"`

	ArgsHash     string `json:"args_hash"`     // sha256 of canonical args JSON
	ArgKeys      string `json:"arg_keys"`      // comma-joined sorted top-level keys
	ArgsRedacted string `json:"args_redacted"` // Bodies only

	Outcome       string `json:"outcome"`
	OutcomeReason string `json:"outcome_reason"`
	ErrorCode     string `json:"error_code"`
	Rung          string `json:"rung"`
	Confidence    string `json:"confidence"`
	Freshness     string `json:"freshness"`
	Hits          int    `json:"hits"`
	HitFiles      string `json:"hit_files"` // newline-joined distinct files the response named (for DS-17)

	OutTokensPre        int64  `json:"out_tokens_pre"`
	OutTokensPost       int64  `json:"out_tokens_post"`
	BudgetTrimmedTokens int64  `json:"budget_trimmed_tokens"`
	BaselineTokens      int64  `json:"baseline_tokens"`
	BaselineMethod      string `json:"baseline_method"`
	TokensSaved         int64  `json:"tokens_saved"`

	BodyRedacted string `json:"body_redacted"` // Bodies only
}

// MemoryEvent is one agent-memory operation (DS-09).
type MemoryEvent struct {
	ID        string    `json:"id"`
	TS        time.Time `json:"ts"`
	LatencyMS int64     `json:"latency_ms"`
	Verb      string    `json:"verb"` // recall | retain | delete | inject | lesson
	Transport string    `json:"transport"`
	SessionID string    `json:"session_id"` // assigned on insert like CallEvent.SessionID
	Identity
	Banks     string `json:"banks"` // comma-joined
	QueryHash string `json:"query_hash"`
	Limit     int    `json:"limit"`

	Returned    int    `json:"returned"`
	ReturnedIDs string `json:"returned_ids"` // comma-joined, rank order
	Scores      string `json:"scores"`       // comma-joined, rank order
	DenseHits   int    `json:"dense_hits"`   // rows the dense arm contributed
	AgeMedianS  int64  `json:"age_median_s"`
	AgeMaxS     int64  `json:"age_max_s"`

	Written  int    `json:"written"`
	Skipped  int    `json:"skipped"`
	Replaced int    `json:"replaced"`
	Deleted  int    `json:"deleted"`
	Deduped  bool   `json:"deduped"`
	Tokens   int64  `json:"tokens"` // inject: tokens returned
	Error    string `json:"error"`
}

// Session is one coding-agent session as the ledger knows it.
type Session struct {
	ID              string    `json:"id"` // "<client>:<client_session_id>" or "<client>:h:<hash>"
	ClientName      string    `json:"client_name"`
	ClientSessionID string    `json:"client_session_id"`
	Project         string    `json:"project"`
	Cwd             string    `json:"cwd"`
	FirstTS         time.Time `json:"first_ts"`
	LastTS          time.Time `json:"last_ts"`
	Correlation     string    `json:"correlation"`
	TranscriptPath  string    `json:"transcript_path,omitempty"` // set by sessionlink
	LinkStatus      string    `json:"link_status,omitempty"`     // "", linked, not_found, unsupported_version, not_consented
}

// SessionLink joins one call to its transcript window (DS-10). Window holds
// the redacted, capped JSON of a sessionlink.Window.
type SessionLink struct {
	CallID     string    `json:"call_id"`
	SessionID  string    `json:"session_id"`
	Confidence float64   `json:"confidence"`
	ToolUseID  string    `json:"tool_use_id"`
	Window     string    `json:"window"`
	LinkedAt   time.Time `json:"linked_at"`
}

// ABRun is one imported controlled A/B trial (DS-18).
type ABRun struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"` // cross_tool | ab_harness
	Task       string    `json:"task"`
	Arm        string    `json:"arm"` // with | without
	Repo       string    `json:"repo"`
	Tokens     int64     `json:"tokens"`
	Turns      int       `json:"turns"`
	DurationS  float64   `json:"duration_s"`
	ToolCalls  int       `json:"tool_calls"`
	FileReads  int       `json:"file_reads"`
	CostUSD    float64   `json:"cost_usd"`
	JudgeScore float64   `json:"judge_score"`
	Valid      bool      `json:"valid"`
	ImportedAt time.Time `json:"imported_at"`
}

// CallFilter selects calls for queries. Zero values mean "any".
type CallFilter struct {
	Since     time.Time
	Until     time.Time
	Client    string
	Project   string
	SessionID string // Session.ID
	Outcome   string // exact, or "error" for any error:*
	Tool      string
	Limit     int
	Offset    int
}

// Stats describes the ledger for status and the Settings page.
type Stats struct {
	Path          string    `json:"path"`
	SizeBytes     int64     `json:"size_bytes"`
	Calls         int64     `json:"calls"`
	MemoryEvents  int64     `json:"memory_events"`
	Sessions      int64     `json:"sessions"`
	Links         int64     `json:"links"`
	DroppedEvents int64     `json:"dropped_events"`
	OldestTS      time.Time `json:"oldest_ts"`
	NewestTS      time.Time `json:"newest_ts"`
	// Correlation is the exact-vs-heuristic split of the call rows: how much of
	// this ledger can actually be joined to an agent conversation. A ledger
	// dominated by heuristic rows is not broken, but an operator must be able
	// to see it rather than infer it — `leankg telemetry status` prints it.
	Correlation map[string]int64 `json:"correlation"`
}

// Store is the telemetry ledger. SQLite today (DS-03); PostgreSQL later
// (DS-27) behind the same interface.
type Store interface {
	InsertCalls(ctx context.Context, calls []CallEvent) error
	InsertMemoryEvents(ctx context.Context, evs []MemoryEvent) error
	UpsertSessions(ctx context.Context, ss []Session) error
	UpsertLinks(ctx context.Context, ls []SessionLink) error
	InsertABRuns(ctx context.Context, rs []ABRun) error
	AddDropped(ctx context.Context, n int64) error

	Calls(ctx context.Context, f CallFilter) ([]CallEvent, error)
	Call(ctx context.Context, id string) (CallEvent, bool, error)
	MemoryEvents(ctx context.Context, f CallFilter) ([]MemoryEvent, error)
	Sessions(ctx context.Context, f CallFilter) ([]Session, error)
	Session(ctx context.Context, id string) (Session, bool, error)
	Links(ctx context.Context, sessionID string) ([]SessionLink, error)
	// UnlinkedCalls returns unlinked MCP tool calls (handshake and REST rows
	// have no transcript entry and are never returned).
	UnlinkedCalls(ctx context.Context, olderThan time.Time, limit int) ([]CallEvent, error)
	ABRuns(ctx context.Context) ([]ABRun, error)

	Purge(ctx context.Context, before time.Time) (int64, error)
	Stats(ctx context.Context) (Stats, error)
	Close() error
}

// Recorder is the hot-path sink the servers hold. Implementations never
// block and never return an error to the caller.
type Recorder interface {
	Level() Level
	// SetLevel applies a live consent change (the SIGHUP / consent-screen
	// reload). A recorder that cannot change level returns an error rather
	// than silently ignoring the operator's choice.
	SetLevel(level Level) error
	RecordCall(ev CallEvent)
	RecordMemory(ev MemoryEvent)
	Close() error
}

// Nop is the Recorder used when capture is Off.
type Nop struct{}

func (Nop) Level() Level             { return Off }
func (Nop) SetLevel(Level) error     { return nil } // nothing to change
func (Nop) RecordCall(CallEvent)     {}
func (Nop) RecordMemory(MemoryEvent) {}
func (Nop) Close() error             { return nil }

type ctxKey struct{}

// WithIdentity attaches caller identity to ctx so library-level events
// (memory) can be joined to the transport call that caused them.
func WithIdentity(ctx context.Context, id Identity, transport string) context.Context {
	return context.WithValue(ctx, ctxKey{}, callerCtx{id: id, transport: transport})
}

type callerCtx struct {
	id        Identity
	transport string
}

// IdentityFrom returns the identity WithIdentity attached, if any.
func IdentityFrom(ctx context.Context) (Identity, string, bool) {
	if ctx == nil {
		return Identity{}, "", false
	}
	c, ok := ctx.Value(ctxKey{}).(callerCtx)
	return c.id, c.transport, ok
}
