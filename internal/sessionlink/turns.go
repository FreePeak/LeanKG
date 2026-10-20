package sessionlink

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// Caps for the turn-based adapters (pi family, grok, codex, gemini). They are
// larger than the Event caps (TextCap, ArgsCap, ResultCap) and are kept as the
// turn-based adapters always emitted them.
const (
	TurnTextCap   = 8 << 10
	TurnArgsCap   = 4 << 10
	TurnResultCap = 4 << 10
)

// TurnBounds is the heuristic time window of a turn-based adapter: a call is
// near the server receive time when it is at most LookBack before it or at
// most Slack after it.
type TurnBounds struct {
	LookBack time.Duration
	Slack    time.Duration
}

// TurnHeuristic is the window of the turn-based adapters. The transcript
// records a call before LeanKG receives it, so the look-back is wider than
// CallLookBack, with a small allowance for clock skew.
var TurnHeuristic = TurnBounds{LookBack: 5 * time.Minute, Slack: time.Minute}

// RawArgs returns a tool's arguments as the JSON object they encode. Some
// clients double-encode arguments as a JSON string, which is unwrapped; a
// missing or null value is {}.
func RawArgs(raw json.RawMessage) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return []byte("{}")
	}
	if raw[0] == '"' {
		var inner string
		if json.Unmarshal(raw, &inner) == nil && strings.TrimSpace(inner) != "" {
			return []byte(inner)
		}
		return []byte("{}")
	}
	return raw
}

// MakeTurnToolCall builds a ToolCall for a turn-based transcript from the
// client's raw tool name and args (as returned by RawArgs), with TurnArgsCap.
func MakeTurnToolCall(id, name string, args []byte, ts time.Time) ToolCall {
	return newToolCall(id, name, args, ts, TurnArgsCap)
}

// turnMatch reports whether tc is the call c for the turn-based matchers and,
// when both times are known, its distance from c.TS. A nil bounds ignores the
// time window.
func turnMatch(tc *ToolCall, c CallRef, b *TurnBounds) (time.Duration, bool) {
	if c.Tool == "" || tc.Norm != "leankg."+c.Tool {
		return 0, false
	}
	if c.ArgsHash != "" && tc.ArgsHash != c.ArgsHash {
		return 0, false
	}
	if c.TS.IsZero() || tc.TS.IsZero() {
		return 0, true
	}
	diff := c.TS.Sub(tc.TS)
	if b != nil && (diff < -b.Slack || diff > b.LookBack) {
		return 0, false
	}
	return absDur(diff), true
}

// MatchTurns returns the turn and call index of the LeanKG call in turns that
// corresponds to c: same canonical tool, same args hash when c has one, and
// nearest in time to c.TS (ties keep the first). With bounds set, only calls
// inside the window qualify.
func MatchTurns(turns []Turn, c CallRef, bounds *TurnBounds) (int, int, bool) {
	bi, bj := -1, -1
	var best time.Duration
	for i := range turns {
		for j := range turns[i].ToolCalls {
			d, ok := turnMatch(&turns[i].ToolCalls[j], c, bounds)
			if ok && (bi < 0 || d < best) {
				bi, bj, best = i, j, d
			}
		}
	}
	return bi, bj, bi >= 0
}

// CountTurnMatches counts the calls in turns that match c under the rules of
// MatchTurns.
func CountTurnMatches(turns []Turn, c CallRef, bounds *TurnBounds) int {
	n := 0
	for i := range turns {
		for j := range turns[i].ToolCalls {
			if _, ok := turnMatch(&turns[i].ToolCalls[j], c, bounds); ok {
				n++
			}
		}
	}
	return n
}

// turnConfidence grades a turn-based match like the Event adapters: exact on a
// session id match, else ConfidenceFor the number of matching calls, or
// ConfidenceAmbiguous when c carries no args hash to match on.
func turnConfidence(sessionID string, turns []Turn, c CallRef) Confidence {
	if c.ClientSessionID != "" && c.ClientSessionID == sessionID {
		return ConfidenceExact
	}
	if c.ArgsHash == "" {
		return ConfidenceAmbiguous
	}
	return ConfidenceFor(CountTurnMatches(turns, c, nil))
}

// WindowFromTurns extracts the bounded window around the LeanKG call c from a
// turn-based transcript: up to n turns on each side of the matched turn, the
// user prompt that led to it, and every call after it up to the next user
// turn. Unlike BuildWindow, follow-up calls keep their bodies.
func WindowFromTurns(client, sessionID, path string, turns []Turn, c CallRef, n int) (Window, error) {
	ti, ci, ok := MatchTurns(turns, c, nil)
	if !ok {
		return Window{}, ErrNotFound
	}
	if n < 0 {
		n = 0
	}
	matched := turns[ti].ToolCalls[ci]
	w := Window{
		Client:     client,
		SessionID:  sessionID,
		Path:       path,
		Matched:    &matched,
		Confidence: turnConfidence(sessionID, turns, c),
	}
	for k := ti - 1; k >= 0; k-- {
		if turns[k].Role == "user" && turns[k].Text != "" {
			w.Prompt = turns[k].Text
			break
		}
	}
	from := max(ti-n, 0)
	w.Before = append([]Turn(nil), turns[from:ti]...)
	to := min(ti+1+n, len(turns))
	w.After = append([]Turn(nil), turns[ti+1:to]...)

	w.FollowUps = append(w.FollowUps, turns[ti].ToolCalls[ci+1:]...)
	for k := ti + 1; k < len(turns) && turns[k].Role != "user"; k++ {
		w.FollowUps = append(w.FollowUps, turns[k].ToolCalls...)
	}
	return w, nil
}

// CapTurns returns the first maxTurns turns (<= 0 is no cap).
func CapTurns(turns []Turn, maxTurns int) []Turn {
	if maxTurns > 0 && len(turns) > maxTurns {
		return turns[:maxTurns]
	}
	return turns
}

// ParseRFC3339 parses an RFC 3339 timestamp with optional fractional seconds.
// An empty or malformed value is the zero time.
func ParseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// FirstNonEmpty returns the first non-empty string, or "" when none is.
func FirstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// newToolCall is the shared ToolCall constructor; argsCap is the cap on Args.
func newToolCall(id, name string, rawArgs []byte, ts time.Time, argsCap int) ToolCall {
	norm, isLeanKG := NormalizeTool(name)
	return ToolCall{
		ID:       id,
		Name:     name,
		Norm:     norm,
		Args:     Clip(string(rawArgs), argsCap),
		ArgsHash: telemetry.ArgsHash(rawArgs),
		Target:   targetOf(rawArgs),
		TS:       ts,
		IsLeanKG: isLeanKG,
	}
}
