// Package sessionlink joins captured LeanKG calls to the calling agent's own
// session transcript (plan v4.15, DS-10..DS-15). It is opt-in per client,
// read-only, and runs lazily (batch), never on the MCP hot path.
//
// Each agent format is an Adapter in its own subpackage; adapters never
// write, never follow symlinks out of the client store, and fail soft
// (ErrUnsupportedVersion / ErrNotFound) instead of crashing.
package sessionlink

import (
	"errors"
	"time"
)

// Errors an adapter returns instead of panicking on unknown input.
var (
	ErrNotFound           = errors.New("sessionlink: transcript not found")
	ErrUnsupportedVersion = errors.New("sessionlink: unsupported transcript version")
)

// CallRef is what the ledger knows about one captured call.
type CallRef struct {
	CallID          string
	TS              time.Time // server receive time
	ClientName      string
	ClientSessionID string // empty unless exact correlation is possible
	Cwd             string
	Tool            string // canonical: import | query | status
	Action          string
	ArgsHash        string // sha256 hex of canonical args JSON (see CanonicalArgsHash)
	HitFiles        []string
}

// TranscriptRef locates one transcript (file or DB row set).
type TranscriptRef struct {
	Client    string
	Path      string // file, or DB path for SQLite-backed clients
	SessionID string // the client's own session id
}

// Confidence is how sure Locate is: 1.0 for exact, < 1 for heuristic.
type Confidence float64

// ToolCall is one tool invocation inside a transcript.
type ToolCall struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`      // as the client wrote it, e.g. mcp__leankg__query
	Norm     string    `json:"norm"`      // normalized: "leankg.query", "grep", "read", "bash", "edit", ...
	Args     string    `json:"args"`      // redacted, capped JSON
	ArgsHash string    `json:"args_hash"` // CanonicalArgsHash of the raw args
	Target   string    `json:"target"`    // file path or symbol it touched, when evident
	Result   string    `json:"result"`    // redacted, capped
	IsError  bool      `json:"is_error"`
	TS       time.Time `json:"ts"`
	IsLeanKG bool      `json:"is_leankg"`
}

// Turn is one message in the transcript window.
type Turn struct {
	Role         string     `json:"role"` // user | assistant | tool
	Text         string     `json:"text"` // redacted, capped
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	TS           time.Time  `json:"ts"`
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	CacheRead    int64      `json:"cache_read"`
	CacheWrite   int64      `json:"cache_write"`
	Model        string     `json:"model,omitempty"`
}

// Window is the bounded context around one LeanKG call.
type Window struct {
	Client     string     `json:"client"`
	SessionID  string     `json:"session_id"`
	Path       string     `json:"path"`
	Prompt     string     `json:"prompt"`  // the user prompt that led to the call
	Matched    *ToolCall  `json:"matched"` // the LeanKG tool call itself
	Before     []Turn     `json:"before"`
	After      []Turn     `json:"after"`
	FollowUps  []ToolCall `json:"follow_ups"` // every tool call after Matched until the next user prompt
	Confidence Confidence `json:"confidence"`
}

// Adapter reads one agent's transcript format.
type Adapter interface {
	// Client is the canonical client name ("claude-code", "pi", "omp",
	// "xdev", "opencode", "grok", "codex", "gemini").
	Client() string
	// Detect reports whether this client's store exists under home.
	Detect(home string) bool
	// Roots are the store paths Detect looked at (shown on the consent screen).
	Roots(home string) []string
	// Locate finds the transcript for c: exact by session id, else heuristic.
	Locate(home string, c CallRef) (TranscriptRef, Confidence, error)
	// Window extracts up to n turns before and after the call.
	Window(t TranscriptRef, c CallRef, n int) (Window, error)
	// Transcript returns the whole session (capped) for the replay view.
	Transcript(t TranscriptRef, maxTurns int) ([]Turn, error)
}
