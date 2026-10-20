// Package gemini reads Gemini CLI chat logs (plan v4.15 DS-15):
// ~/.gemini/tmp/<project-hash>/chats/session-*.jsonl (and legacy whole-file
// .json). The project hash is taken to be sha256 of the project root path,
// which lets the heuristic match a cwd without reading every file. Everything
// here is experimental: no local chat existed to confirm the shapes.
package gemini

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

const maxLine = 32 << 20

type adapter struct{}

// New returns the Gemini CLI adapter.
func New() sessionlink.Adapter { return &adapter{} }

// Client is the canonical client name.
func (a *adapter) Client() string { return "gemini" }

// Experimental reports that the format is built from docs, not a local sample.
func (a *adapter) Experimental() bool { return true }

func root(home string) string { return filepath.Join(home, ".gemini", "tmp") }

// Roots is the tmp store Detect looks at.
func (a *adapter) Roots(home string) []string { return []string{root(home)} }

// Detect reports whether the tmp store exists.
func (a *adapter) Detect(home string) bool {
	fi, err := os.Stat(root(home))
	return err == nil && fi.IsDir()
}

// projectHash is sha256 hex of the project root path, the project directory
// name Gemini uses under tmp/.
func projectHash(cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	return hex.EncodeToString(sum[:])
}

// listChats returns session files under <root>/<project>/chats, skipping
// symlinks and anything that is not a regular file.
func listChats(r string) []string {
	var out []string
	projects, err := os.ReadDir(r)
	if err != nil {
		return nil
	}
	for _, p := range projects {
		if p.Type()&os.ModeSymlink != 0 || !p.IsDir() {
			continue
		}
		chats, err := os.ReadDir(filepath.Join(r, p.Name(), "chats"))
		if err != nil {
			continue
		}
		for _, c := range chats {
			name := c.Name()
			if !c.Type().IsRegular() || !strings.HasPrefix(name, "session-") {
				continue
			}
			if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".json") {
				out = append(out, filepath.Join(r, p.Name(), "chats", name))
			}
		}
	}
	return out
}

// Locate finds the transcript for c: exact by sessionId, else heuristic on the
// project hash of the cwd, the time window, normalized tool and args hash.
func (a *adapter) Locate(home string, c sessionlink.CallRef) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	r := root(home)
	files := listChats(r)
	if cid := c.ClientSessionID; cid != "" {
		for _, f := range files {
			if sid := sessionIDOf(f); sid == cid {
				return sessionlink.TranscriptRef{Client: "gemini", Path: f, SessionID: sid}, sessionlink.ConfidenceExact, nil
			}
		}
	}
	if c.Cwd == "" || c.Tool == "" || c.TS.IsZero() {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	want := projectHash(filepath.Clean(c.Cwd))
	var best string
	var bestMod time.Time
	var bestN int
	for _, f := range files {
		if filepath.Base(filepath.Dir(filepath.Dir(f))) != want {
			continue
		}
		fi, err := os.Stat(f)
		if err != nil || fi.ModTime().Before(c.TS.Add(-sessionlink.TurnHeuristic.LookBack)) {
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
		if best == "" || fi.ModTime().After(bestMod) {
			best, bestMod, bestN = f, fi.ModTime(), n
		}
	}
	if best == "" {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	return sessionlink.TranscriptRef{Client: "gemini", Path: best, SessionID: sessionIDOf(best)}, sessionlink.ConfidenceFor(bestN), nil
}

// sessionIDOf reads the sessionId from a JSONL metadata line or a legacy document.
func sessionIDOf(path string) string {
	if strings.HasSuffix(path, ".json") {
		s, err := load(path)
		if err != nil {
			return ""
		}
		return s.id
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for i := 0; i < 20 && sc.Scan(); i++ {
		var m struct {
			SessionID string `json:"sessionId"`
			Type      string `json:"type"`
		}
		if json.Unmarshal(bytes.TrimSpace(sc.Bytes()), &m) == nil && m.Type == "" && m.SessionID != "" {
			return m.SessionID
		}
	}
	return ""
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

type gTool struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Result json.RawMessage `json:"result"`
	Status string          `json:"status"`
}

type gTokens struct {
	Input  int64 `json:"input"`
	Cached int64 `json:"cached"`
	Output int64 `json:"output"`
}

type gMsg struct {
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []gTool         `json:"toolCalls"`
	Tokens    *gTokens        `json:"tokens"`
	Model     string          `json:"model"`
}

// load parses a chat file. JSONL: a metadata line carries sessionId, message
// lines carry type, and a repeated id replaces the earlier version in place.
// Legacy .json is one document with a messages array. Garbage is skipped.
func load(path string) (session, error) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return session{}, fmt.Errorf("%w: %v", sessionlink.ErrNotFound, err)
	}
	var (
		id    string
		msgs  []gMsg
		index = map[string]int{}
		seen  bool
	)
	add := func(m gMsg) {
		seen = true
		if m.ID != "" {
			if i, ok := index[m.ID]; ok {
				msgs[i] = m
				return
			}
			index[m.ID] = len(msgs)
		}
		msgs = append(msgs, m)
	}

	if strings.HasSuffix(path, ".json") {
		raw, err := os.ReadFile(path)
		if err != nil {
			return session{}, fmt.Errorf("%w: %v", sessionlink.ErrNotFound, err)
		}
		var doc struct {
			SessionID string `json:"sessionId"`
			Messages  []gMsg `json:"messages"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return session{}, sessionlink.ErrUnsupportedVersion
		}
		id = doc.SessionID
		seen = id != ""
		for _, m := range doc.Messages {
			add(m)
		}
	} else {
		f, err := os.Open(path)
		if err != nil {
			return session{}, fmt.Errorf("%w: %v", sessionlink.ErrNotFound, err)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), maxLine)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var probe struct {
				Type      string `json:"type"`
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(line, &probe) != nil {
				continue
			}
			switch {
			case probe.Type != "":
				var m gMsg
				if json.Unmarshal(line, &m) == nil {
					add(m)
				}
			case probe.SessionID != "" && id == "":
				id = probe.SessionID
				seen = true
			}
		}
	}
	if !seen {
		return session{}, sessionlink.ErrUnsupportedVersion
	}
	if id == "" {
		id = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".jsonl"), ".json")
	}
	return session{id: id, turns: buildTurns(msgs)}, nil
}

func buildTurns(msgs []gMsg) []sessionlink.Turn {
	var turns []sessionlink.Turn
	for _, m := range msgs {
		ts := sessionlink.ParseRFC3339(m.Timestamp)
		switch m.Type {
		case "user":
			text := contentText(m.Content)
			if text == "" {
				continue
			}
			turns = append(turns, sessionlink.Turn{Role: "user", Text: sessionlink.Clip(text, sessionlink.TurnTextCap), TS: ts})
		case "gemini":
			t := sessionlink.Turn{Role: "assistant", Text: sessionlink.Clip(contentText(m.Content), sessionlink.TurnTextCap), TS: ts, Model: m.Model}
			if tk := m.Tokens; tk != nil {
				in := tk.Input - tk.Cached
				if in < 0 {
					in = 0
				}
				t.InputTokens, t.CacheRead, t.OutputTokens = in, tk.Cached, tk.Output
			}
			for _, tool := range m.ToolCalls {
				tc := sessionlink.MakeTurnToolCall(tool.ID, tool.Name, sessionlink.RawArgs(tool.Args), ts)
				tc.Result = sessionlink.Clip(resultText(tool.Result), sessionlink.TurnResultCap)
				tc.IsError = tool.Status == "error"
				t.ToolCalls = append(t.ToolCalls, tc)
			}
			if t.Text == "" && len(t.ToolCalls) == 0 && t.InputTokens == 0 && t.OutputTokens == 0 {
				continue
			}
			turns = append(turns, t)
		}
		// info and error messages are not part of the conversation.
	}
	return turns
}

// contentText reads a string or an array of {text} parts.
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
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		if p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// resultText returns a tool result as text: a string as is, anything else as JSON.
func resultText(raw json.RawMessage) string {
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
