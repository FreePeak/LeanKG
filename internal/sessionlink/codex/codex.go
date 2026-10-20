// Package codex reads Codex CLI rollout transcripts (plan v4.15 DS-15):
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl. The adapter is
// experimental; the entry structure was surveyed locally, while the
// function_call and token_count shapes come from docs.
package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

const (
	maxLine  = 32 << 20
	maxDepth = 4 // sessions/YYYY/MM/DD/<file>
)

type adapter struct{}

// New returns the Codex CLI adapter.
func New() sessionlink.Adapter { return &adapter{} }

// Client is the canonical client name.
func (a *adapter) Client() string { return "codex" }

// Experimental reports that function_call and token_count shapes are unconfirmed.
func (a *adapter) Experimental() bool { return true }

func root(home string) string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "sessions")
	}
	return filepath.Join(home, ".codex", "sessions")
}

// Roots is the sessions directory Detect looks at.
func (a *adapter) Roots(home string) []string { return []string{root(home)} }

// Detect reports whether the sessions directory exists.
func (a *adapter) Detect(home string) bool {
	fi, err := os.Stat(root(home))
	return err == nil && fi.IsDir()
}

type candidate struct {
	path  string
	id    string // session id from session_meta (may be empty)
	cwd   string
	start time.Time
}

// listRollouts walks the date tree, skipping symlinks and anything deeper than
// the YYYY/MM/DD layout.
func listRollouts(r string) []string {
	var out []string
	_ = filepath.WalkDir(r, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(r, p)
		if rerr != nil {
			return nil
		}
		if depth := strings.Count(rel, string(filepath.Separator)); d.IsDir() && rel != "." && depth >= maxDepth {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// uuidFromName returns the trailing id of rollout-<YYYY-MM-DDThh-mm-ss>-<uuid>.jsonl.
func uuidFromName(path string) string {
	base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "rollout-"), ".jsonl")
	const tsLen = len("2026-10-03T20-00-00")
	if len(base) > tsLen+1 {
		return base[tsLen+1:]
	}
	return base
}

// Locate finds the transcript for c: exact by session id (payload id or the
// filename uuid), else heuristic on cwd, time window, normalized tool and args.
func (a *adapter) Locate(home string, c sessionlink.CallRef) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	files := listRollouts(root(home))
	if cid := c.ClientSessionID; cid != "" {
		for _, f := range files {
			if uuidFromName(f) == cid || metaOf(f).id == cid {
				return sessionlink.TranscriptRef{Client: "codex", Path: f, SessionID: idOf(f)}, sessionlink.ConfidenceExact, nil
			}
		}
	}
	if c.Cwd == "" || c.Tool == "" || c.TS.IsZero() {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	var best string
	var bestStart time.Time
	var bestN int
	for _, f := range files {
		m := metaOf(f)
		if m.cwd == "" || filepath.Clean(m.cwd) != filepath.Clean(c.Cwd) {
			continue
		}
		if fi, err := os.Stat(f); err != nil || fi.ModTime().Before(c.TS.Add(-sessionlink.TurnHeuristic.LookBack)) {
			continue
		}
		if !m.start.IsZero() && m.start.After(c.TS.Add(sessionlink.TurnHeuristic.Slack)) {
			continue
		}
		s, err := load(f)
		if err != nil {
			continue
		}
		n := sessionlink.CountTurnMatches(s.turns, c, &sessionlink.TurnHeuristic)
		if n == 0 {
			continue
		}
		if best == "" || m.start.After(bestStart) {
			best, bestStart, bestN = f, m.start, n
		}
	}
	if best == "" {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	return sessionlink.TranscriptRef{Client: "codex", Path: best, SessionID: idOf(best)}, sessionlink.ConfidenceFor(bestN), nil
}

func idOf(path string) string {
	if id := metaOf(path).id; id != "" {
		return id
	}
	return uuidFromName(path)
}

// metaOf reads the session_meta entry from the first lines of a rollout.
func metaOf(path string) candidate {
	c := candidate{path: path}
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for i := 0; i < 20 && sc.Scan(); i++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e rawEntry
		if json.Unmarshal(line, &e) != nil || e.Type != "session_meta" {
			continue
		}
		var p struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
			Cwd       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(e.Payload, &p) != nil {
			return c
		}
		c.id = sessionlink.FirstNonEmpty(p.ID, p.SessionID)
		c.cwd = p.Cwd
		c.start = sessionlink.ParseRFC3339(sessionlink.FirstNonEmpty(p.Timestamp, e.Timestamp))
		return c
	}
	return c
}

// Window extracts the bounded context around the matched leankg call.
func (a *adapter) Window(t sessionlink.TranscriptRef, c sessionlink.CallRef, n int) (sessionlink.Window, error) {
	s, err := load(t.Path)
	if err != nil {
		return sessionlink.Window{}, err
	}
	sid := t.SessionID
	if sid == "" {
		sid = s.id
	}
	return sessionlink.WindowFromTurns(a.Client(), sid, t.Path, s.turns, c, n)
}

// Transcript returns the session turns, capped. maxTurns <= 0 keeps all.
func (a *adapter) Transcript(t sessionlink.TranscriptRef, maxTurns int) ([]sessionlink.Turn, error) {
	s, err := load(t.Path)
	if err != nil {
		return nil, err
	}
	return sessionlink.CapTurns(s.turns, maxTurns), nil
}

// ---- parsing ----

type session struct {
	id    string
	turns []sessionlink.Turn
}

type rawEntry struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type rawItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Input     json.RawMessage `json:"input"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`
}

type rawTokenPayload struct {
	Type string `json:"type"`
	Info *struct {
		LastTokenUsage *struct {
			InputTokens       int64 `json:"input_tokens"`
			CachedInputTokens int64 `json:"cached_input_tokens"`
			OutputTokens      int64 `json:"output_tokens"`
		} `json:"last_token_usage"`
	} `json:"info"`
}

type callPos struct{ turn, call int }

// load parses a rollout into user and assistant turns, skipping garbage lines,
// injected environment context and reasoning items.
func load(path string) (session, error) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return session{}, fmt.Errorf("%w: %v", sessionlink.ErrNotFound, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return session{}, fmt.Errorf("%w: %v", sessionlink.ErrNotFound, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine)

	var (
		turns []sessionlink.Turn
		texts []string
		pos   = map[string]callPos{}
		id    string
		seen  bool
	)
	startTurn := func(role string, ts time.Time) int {
		if n := len(turns); n > 0 && turns[n-1].Role == role {
			return n - 1
		}
		turns = append(turns, sessionlink.Turn{Role: role, TS: ts})
		texts = append(texts, "")
		return len(turns) - 1
	}

	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e rawEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		seen = true
		ts := sessionlink.ParseRFC3339(e.Timestamp)
		switch e.Type {
		case "session_meta":
			var p struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && id == "" {
				id = sessionlink.FirstNonEmpty(p.ID, p.SessionID)
			}
		case "response_item":
			var it rawItem
			if json.Unmarshal(e.Payload, &it) != nil {
				continue
			}
			switch it.Type {
			case "message":
				text := contentText(it.Content)
				if text == "" {
					continue
				}
				switch it.Role {
				case "user":
					if isInjected(text) {
						continue
					}
					i := startTurn("user", ts)
					texts[i] += text
				case "assistant":
					i := startTurn("assistant", ts)
					texts[i] += text
				}
			case "function_call", "custom_tool_call":
				i := startTurn("assistant", ts)
				raw := it.Arguments
				if len(raw) == 0 {
					raw = it.Input
				}
				tc := sessionlink.MakeTurnToolCall(it.CallID, it.Name, sessionlink.RawArgs(raw), ts)
				if it.CallID != "" {
					pos[it.CallID] = callPos{turn: i, call: len(turns[i].ToolCalls)}
				}
				turns[i].ToolCalls = append(turns[i].ToolCalls, tc)
			case "function_call_output", "custom_tool_call_output":
				p, ok := pos[it.CallID]
				if !ok {
					continue
				}
				tc := &turns[p.turn].ToolCalls[p.call]
				tc.Result = sessionlink.Clip(outputText(it.Output), sessionlink.TurnResultCap)
			}
		case "event_msg":
			var tp rawTokenPayload
			if json.Unmarshal(e.Payload, &tp) != nil || tp.Type != "token_count" || tp.Info == nil || tp.Info.LastTokenUsage == nil {
				continue
			}
			u := tp.Info.LastTokenUsage
			for k := len(turns) - 1; k >= 0; k-- {
				if turns[k].Role != "assistant" {
					continue
				}
				// Codex reports input including the cached part; split it so
				// InputTokens and CacheRead do not double count.
				in := u.InputTokens - u.CachedInputTokens
				if in < 0 {
					in = 0
				}
				turns[k].InputTokens += in
				turns[k].CacheRead += u.CachedInputTokens
				turns[k].OutputTokens += u.OutputTokens
				break
			}
		}
	}
	if !seen {
		return session{}, sessionlink.ErrUnsupportedVersion
	}
	for i := range turns {
		turns[i].Text = sessionlink.Clip(texts[i], sessionlink.TurnTextCap)
	}
	if id == "" {
		id = uuidFromName(path)
	}
	return session{id: id, turns: turns}, nil
}

// isInjected reports the environment and instruction blocks Codex prepends
// as user messages; they are context, not the user's prompt.
func isInjected(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "<environment_context>") ||
		strings.HasPrefix(t, "<user_instructions>") ||
		strings.HasPrefix(t, "# AGENTS.md instructions")
}

// contentText joins the text blocks of a message content array.
func contentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var parts []string
	for _, b := range items {
		switch b.Type {
		case "input_text", "output_text", "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// outputText returns a function_call_output as text: a string as is, an object
// as its JSON.
func outputText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(raw)
}
