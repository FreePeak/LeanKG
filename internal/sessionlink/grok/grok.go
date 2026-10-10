// Package grok reads Grok CLI session stores (plan v4.15 DS-14). The layout is
// $GROK_HOME/sessions/<url-encoded cwd>/<session-id>/{summary.json,updates.jsonl}.
// The adapter is experimental: the update shapes come from the ACP-style docs,
// and the only local sample carried hook_execution updates.
package grok

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

const maxLine = 32 << 20

type adapter struct{}

// New returns the Grok CLI adapter.
func New() sessionlink.Adapter { return &adapter{} }

// Client is the canonical client name.
func (a *adapter) Client() string { return "grok" }

// Experimental reports that the format is built from docs, not a local sample.
func (a *adapter) Experimental() bool { return true }

func root(home string) string {
	if d := os.Getenv("GROK_HOME"); d != "" {
		return filepath.Join(d, "sessions")
	}
	return filepath.Join(home, ".grok", "sessions")
}

// Roots is the sessions directory Detect looks at.
func (a *adapter) Roots(home string) []string { return []string{root(home)} }

// Detect reports whether the sessions directory exists.
func (a *adapter) Detect(home string) bool {
	fi, err := os.Stat(root(home))
	return err == nil && fi.IsDir()
}

type summary struct {
	Info struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"info"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type sessionDir struct {
	updates string // updates.jsonl path
	id      string // directory name (the session id)
	cwd     string // decoded from the bucket name
	created time.Time
	updated time.Time
	sumID   string
}

// listSessions walks <root>/<bucket>/<session> without following symlinks.
func listSessions(r string) []sessionDir {
	var out []sessionDir
	buckets, err := os.ReadDir(r)
	if err != nil {
		return nil
	}
	for _, b := range buckets {
		if b.Type()&os.ModeSymlink != 0 || !b.IsDir() {
			continue
		}
		cwd, err := url.PathUnescape(b.Name())
		if err != nil {
			cwd = b.Name()
		}
		sessions, err := os.ReadDir(filepath.Join(r, b.Name()))
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if s.Type()&os.ModeSymlink != 0 || !s.IsDir() {
				continue
			}
			dir := filepath.Join(r, b.Name(), s.Name())
			updates := filepath.Join(dir, "updates.jsonl")
			if fi, err := os.Lstat(updates); err != nil || !fi.Mode().IsRegular() {
				continue
			}
			sd := sessionDir{updates: updates, id: s.Name(), cwd: cwd}
			if raw, err := os.ReadFile(filepath.Join(dir, "summary.json")); err == nil {
				var sum summary
				if json.Unmarshal(raw, &sum) == nil {
					sd.sumID = sum.Info.ID
					if sum.Info.Cwd != "" {
						sd.cwd = sum.Info.Cwd
					}
					sd.created = sessionlink.ParseRFC3339(sum.CreatedAt)
					sd.updated = sessionlink.ParseRFC3339(sum.UpdatedAt)
				}
			}
			out = append(out, sd)
		}
	}
	return out
}

// Locate finds the transcript for c: exact by session id, else heuristic on
// cwd, time window, normalized tool and args hash.
func (a *adapter) Locate(home string, c sessionlink.CallRef) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	sessions := listSessions(root(home))
	if cid := c.ClientSessionID; cid != "" {
		for _, s := range sessions {
			if s.sumID == cid || s.id == cid {
				return a.ref(s), sessionlink.ConfidenceExact, nil
			}
		}
	}
	if c.Cwd == "" || c.Tool == "" || c.TS.IsZero() {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	var best *sessionDir
	var bestN int
	for i := range sessions {
		s := &sessions[i]
		if filepath.Clean(s.cwd) != filepath.Clean(c.Cwd) {
			continue
		}
		if !s.updated.IsZero() && s.updated.Before(c.TS.Add(-sessionlink.TurnHeuristic.LookBack)) {
			continue
		}
		if !s.created.IsZero() && s.created.After(c.TS.Add(sessionlink.TurnHeuristic.Slack)) {
			continue
		}
		sess, err := load(s.updates)
		if err != nil {
			continue
		}
		n := sessionlink.CountTurnMatches(sess.turns, c, &sessionlink.TurnHeuristic)
		if n == 0 {
			continue
		}
		if best == nil || s.created.After(best.created) {
			best, bestN = s, n
		}
	}
	if best == nil {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	return a.ref(*best), sessionlink.ConfidenceFor(bestN), nil
}

func (a *adapter) ref(s sessionDir) sessionlink.TranscriptRef {
	id := s.sumID
	if id == "" {
		id = s.id
	}
	return sessionlink.TranscriptRef{Client: "grok", Path: s.updates, SessionID: id}
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

type updateLine struct {
	Timestamp int64 `json:"timestamp"`
	Params    struct {
		Meta struct {
			AgentTimestampMs int64 `json:"agentTimestampMs"`
		} `json:"_meta"`
		Update rawUpdate `json:"update"`
	} `json:"params"`
}

type rawUpdate struct {
	SessionUpdate  string          `json:"sessionUpdate"`
	Content        json.RawMessage `json:"content"`
	ToolCallID     string          `json:"tool_call_id"`
	ToolCallIDCase string          `json:"toolCallId"`
	Name           string          `json:"name"`
	Title          string          `json:"title"`
	Status         string          `json:"status"`
	RawInput       json.RawMessage `json:"rawInput"`
	RawInputSnake  json.RawMessage `json:"raw_input"`
	RawOutput      json.RawMessage `json:"rawOutput"`
	RawOutputSnake json.RawMessage `json:"raw_output"`
}

type callPos struct{ turn, call int }

// load parses updates.jsonl into user and assistant turns. Garbage lines are
// skipped. Reasoning (agent_thought_chunk) and hook updates never enter a turn.
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
		seen  bool
	)
	appendText := func(role string, ts time.Time, s string) {
		if s == "" {
			return
		}
		if n := len(turns); n == 0 || turns[n-1].Role != role {
			turns = append(turns, sessionlink.Turn{Role: role, TS: ts})
			texts = append(texts, "")
		}
		texts[len(texts)-1] += s
	}
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var u updateLine
		if json.Unmarshal(line, &u) != nil {
			continue
		}
		seen = true
		ts := msTime(u.Params.Meta.AgentTimestampMs)
		if ts.IsZero() {
			ts = msTime(u.Timestamp)
		}
		up := u.Params.Update
		switch up.SessionUpdate {
		case "user_message_chunk":
			appendText("user", ts, contentText(up.Content))
		case "agent_message_chunk":
			appendText("assistant", ts, contentText(up.Content))
		case "tool_call":
			if n := len(turns); n == 0 || turns[n-1].Role != "assistant" {
				turns = append(turns, sessionlink.Turn{Role: "assistant", TS: ts})
				texts = append(texts, "")
			}
			id := sessionlink.FirstNonEmpty(up.ToolCallID, up.ToolCallIDCase)
			name := sessionlink.FirstNonEmpty(up.Name, up.Title)
			tc := sessionlink.MakeTurnToolCall(id, name, sessionlink.RawArgs(firstRaw(up.RawInput, up.RawInputSnake)), ts)
			last := &turns[len(turns)-1]
			if id != "" {
				pos[id] = callPos{turn: len(turns) - 1, call: len(last.ToolCalls)}
			}
			last.ToolCalls = append(last.ToolCalls, tc)
		case "tool_call_update":
			id := sessionlink.FirstNonEmpty(up.ToolCallID, up.ToolCallIDCase)
			p, ok := pos[id]
			if !ok {
				continue
			}
			tc := &turns[p.turn].ToolCalls[p.call]
			if out := firstRaw(up.RawOutput, up.RawOutputSnake); len(out) > 0 {
				tc.Result = sessionlink.Clip(resultText(out, up.Content), sessionlink.TurnResultCap)
			}
			if up.Status == "failed" || up.Status == "error" {
				tc.IsError = true
			}
		}
	}
	if !seen {
		return session{}, sessionlink.ErrUnsupportedVersion
	}
	for i := range turns {
		turns[i].Text = sessionlink.Clip(texts[i], sessionlink.TurnTextCap)
	}
	return session{id: filepath.Base(filepath.Dir(path)), turns: turns}, nil
}

// contentText reads an ACP content block or an array of them.
func contentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	switch raw[0] {
	case '{':
		var b struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &b) == nil && b.Type != "thinking" {
			return b.Text
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return ""
		}
		var parts []string
		for _, it := range items {
			if s := contentText(it); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return ""
}

// resultText prefers a raw output string or object, else the content blocks.
func resultText(out, content json.RawMessage) string {
	if len(out) > 0 && out[0] == '"' {
		var s string
		if json.Unmarshal(out, &s) == nil {
			return s
		}
	}
	if len(out) > 0 && string(out) != "null" {
		return string(out)
	}
	return contentText(content)
}

func firstRaw(vs ...json.RawMessage) json.RawMessage {
	for _, v := range vs {
		if len(v) > 0 && string(v) != "null" {
			return v
		}
	}
	return nil
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
