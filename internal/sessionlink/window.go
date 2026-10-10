package sessionlink

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// Confidence levels. Exact is a client session id match; a heuristic match
// is unique (0.9) or ambiguous (0.5). Anything below HeuristicThreshold is
// shown as low confidence by the dashboard.
const (
	ConfidenceExact     Confidence = 1.0
	ConfidenceUnique    Confidence = 0.9
	ConfidenceAmbiguous Confidence = 0.5

	HeuristicThreshold = 0.8
)

// Caps applied to transcript text before it is stored or returned.
const (
	TextCap   = 4096
	ArgsCap   = 2048
	ResultCap = 2048
)

// Clip redacts s and caps it to max bytes. Adapters apply it to every text
// field they emit, so nothing unredacted leaves a parser.
func Clip(s string, max int) string {
	return telemetry.Cap(telemetry.Redact(s), max)
}

// Event is one adapter-neutral entry of a transcript, in file order. A
// real user prompt is an Event with Prompt set; tool results are already
// attached to the ToolCalls of the assistant Event that made them.
type Event struct {
	Sidechain bool
	Prompt    bool
	Turn      Turn
}

// MakeToolCall builds a ToolCall from a client's raw tool name and raw
// args JSON. The args hash is telemetry.ArgsHash of the raw bytes, which is
// the same hash the capture middleware stores for the matching call.
func MakeToolCall(id, name string, rawArgs []byte, ts time.Time) ToolCall {
	return newToolCall(id, name, rawArgs, ts, ArgsCap)
}

// SetResult stores a redacted, capped tool result on tc.
func SetResult(tc *ToolCall, text string, isErr bool) {
	tc.Result = Clip(text, ResultCap)
	tc.IsError = isErr
}

// targetOf returns the file path, path or pattern argument, when present.
func targetOf(raw []byte) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"file_path", "filePath", "path", "pattern"} {
		var s string
		if v, ok := m[k]; ok && json.Unmarshal(v, &s) == nil && s != "" {
			return Clip(s, 512)
		}
	}
	return ""
}

// MatchesCall reports whether tc is the LeanKG call c: same canonical tool
// and the same canonical args hash.
func MatchesCall(tc ToolCall, c CallRef) bool {
	return c.Tool != "" && tc.Norm == "leankg."+c.Tool && tc.ArgsHash == c.ArgsHash
}

// CountMatches counts the tool calls in events that match c.
func CountMatches(events []Event, c CallRef) int {
	n := 0
	for _, ev := range events {
		for _, tc := range ev.Turn.ToolCalls {
			if MatchesCall(tc, c) {
				n++
			}
		}
	}
	return n
}

// Heuristic time window: a tool call is near the server receive time when it
// is at most CallLookBack before it or at most CallSlack after it.
const (
	CallLookBack = 2 * time.Minute
	CallSlack    = 5 * time.Second
)

// CountMatchesNear counts matches of c whose tool call time lies in the
// heuristic window around c.TS. A zero c.TS disables the time check.
func CountMatchesNear(events []Event, c CallRef) int {
	if c.TS.IsZero() {
		return CountMatches(events, c)
	}
	n := 0
	for _, ev := range events {
		for _, tc := range ev.Turn.ToolCalls {
			if MatchesCall(tc, c) && !tc.TS.Before(c.TS.Add(-CallLookBack)) && !tc.TS.After(c.TS.Add(CallSlack)) {
				n++
			}
		}
	}
	return n
}

// ConfidenceFor is the heuristic confidence for n candidate matches.
func ConfidenceFor(n int) Confidence {
	if n == 1 {
		return ConfidenceUnique
	}
	return ConfidenceAmbiguous
}

// BuildWindow locates the matching LeanKG call in events and extracts the
// bounded window around it. The matched call and its neighbours are taken
// from the same chain as the match (main or sidechain) so a subagent's
// calls never leak into the main conversation, and vice versa.
func BuildWindow(client, sessionID, path string, events []Event, c CallRef, n int, conf Confidence) (Window, error) {
	// Pick the candidate closest in time to the call; ties keep the first.
	best, bestTC, bestDist := -1, -1, time.Duration(-1)
	for i, ev := range events {
		for j, tc := range ev.Turn.ToolCalls {
			if !MatchesCall(tc, c) {
				continue
			}
			d := absDur(tc.TS.Sub(c.TS))
			if c.TS.IsZero() || tc.TS.IsZero() {
				d = 0
			}
			if best < 0 || d < bestDist {
				best, bestTC, bestDist = i, j, d
			}
		}
	}
	if best < 0 {
		return Window{}, ErrNotFound
	}

	side := events[best].Sidechain
	var seq []Event
	mi := -1
	for i, ev := range events {
		if ev.Sidechain != side {
			continue
		}
		if i == best {
			mi = len(seq)
		}
		seq = append(seq, ev)
	}

	w := Window{Client: client, SessionID: sessionID, Path: path, Confidence: conf}
	for k := mi; k >= 0; k-- {
		if seq[k].Prompt {
			w.Prompt = seq[k].Turn.Text
			break
		}
	}

	for k := mi - 1; k >= 0 && len(w.Before) < n; k-- {
		w.Before = append([]Turn{seq[k].Turn}, w.Before...)
	}
	for k := mi + 1; k < len(seq) && len(w.After) < n; k++ {
		w.After = append(w.After, seq[k].Turn)
	}

	matched := seq[mi].Turn.ToolCalls[bestTC]
	w.Matched = &matched

	for _, tc := range seq[mi].Turn.ToolCalls[bestTC+1:] {
		w.FollowUps = append(w.FollowUps, slim(tc))
	}
	for k := mi + 1; k < len(seq) && !seq[k].Prompt; k++ {
		for _, tc := range seq[k].Turn.ToolCalls {
			w.FollowUps = append(w.FollowUps, slim(tc))
		}
	}
	return w, nil
}

// slim drops the bodies of a follow-up call; the window keeps only name,
// target and success for those.
func slim(tc ToolCall) ToolCall {
	tc.Args = ""
	tc.Result = ""
	return tc
}

// TurnsOf returns the turns of events in order, capped at maxTurns (<= 0 is
// no cap).
func TurnsOf(events []Event, maxTurns int) []Turn {
	var out []Turn
	for _, ev := range events {
		if maxTurns > 0 && len(out) >= maxTurns {
			break
		}
		out = append(out, ev.Turn)
	}
	return out
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// OpenUnder opens path for reading when it lies under root and no component
// below root is a symbolic link. It never follows a link out of the client
// store. A missing or non-regular file yields ErrNotFound.
func OpenUnder(root, path string) (*os.File, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil, ErrNotFound
	}
	cur := root
	parts := strings.Split(rel, string(filepath.Separator))
	for i, p := range parts {
		cur = filepath.Join(cur, p)
		fi, err := os.Lstat(cur)
		if err != nil {
			return nil, ErrNotFound
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, ErrNotFound
		}
		last := i == len(parts)-1
		if last && !fi.Mode().IsRegular() {
			return nil, ErrNotFound
		}
		if !last && !fi.IsDir() {
			return nil, ErrNotFound
		}
	}
	return os.Open(path)
}

// ValidSessionID reports whether id is safe to use as a file name component.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > 200 || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, "/\\\x00")
}
