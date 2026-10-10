package pifamily

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

const maxLine = 32 << 20

// rawEntry is the union of the header and tree entry fields this adapter reads.
// Unknown fields and unknown entry types are ignored, so newer versions parse
// on a best-effort basis.
type rawEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Cwd       string          `json:"cwd"`
	Message   json.RawMessage `json:"message"`
}

type rawMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	Usage      *rawUsage       `json:"usage"`
	ToolCallID string          `json:"toolCallId"`
	IsError    bool            `json:"isError"`
	Timestamp  json.RawMessage `json:"timestamp"`
}

type rawUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

type rawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// session is one parsed transcript restricted to its active branch.
type session struct {
	id    string
	cwd   string
	start time.Time
	turns []sessionlink.Turn
}

type sessionHeader struct {
	id    string
	cwd   string
	start time.Time
}

// readHeader reads only the first lines of a file and returns its session header.
func readHeader(path string) (sessionHeader, bool) {
	f, err := os.Open(path)
	if err != nil {
		return sessionHeader{}, false
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
		if json.Unmarshal(line, &e) != nil || e.Type != "session" {
			continue
		}
		return sessionHeader{id: e.ID, cwd: e.Cwd, start: sessionlink.ParseRFC3339(e.Timestamp)}, true
	}
	return sessionHeader{}, false
}

// load parses one transcript file into its active-branch turns.
func load(path string) (session, error) {
	f, err := os.Open(path)
	if err != nil {
		return session{}, fmt.Errorf("%w: %v", sessionlink.ErrNotFound, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	var (
		hdr     rawEntry
		haveHdr bool
		entries []rawEntry
	)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e rawEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "session":
			if !haveHdr {
				hdr, haveHdr = e, true
			}
		case "title", "":
		default:
			entries = append(entries, e)
		}
	}
	if !haveHdr && len(entries) == 0 {
		return session{}, sessionlink.ErrUnsupportedVersion
	}

	s := session{id: hdr.ID, cwd: hdr.Cwd, start: sessionlink.ParseRFC3339(hdr.Timestamp)}
	if s.id == "" {
		s.id = uuidFromName(path)
	}
	s.turns = buildTurns(activeBranch(entries))
	return s, nil
}

// activeBranch follows parentId from the last tree entry (file order) back to
// the root and returns that path in order. Entries without ids (flat files)
// are returned as written.
func activeBranch(entries []rawEntry) []rawEntry {
	byID := make(map[string]int, len(entries))
	leaf := -1
	for i, e := range entries {
		if e.ID != "" {
			byID[e.ID] = i
			leaf = i
		}
	}
	if leaf < 0 {
		return entries
	}
	var rev []rawEntry
	seen := make(map[int]bool)
	for cur := leaf; cur >= 0 && !seen[cur]; {
		seen[cur] = true
		rev = append(rev, entries[cur])
		p := entries[cur].ParentID
		if p == nil || *p == "" {
			break
		}
		idx, ok := byID[*p]
		if !ok {
			break
		}
		cur = idx
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

type callPos struct{ turn, call int }

// buildTurns turns active-branch entries into user and assistant turns. Tool
// results are merged into their matching ToolCall rather than kept as turns.
func buildTurns(chain []rawEntry) []sessionlink.Turn {
	var turns []sessionlink.Turn
	pos := make(map[string]callPos)
	for _, e := range chain {
		if e.Type != "message" || len(e.Message) == 0 {
			continue
		}
		var m rawMessage
		if json.Unmarshal(e.Message, &m) != nil {
			continue
		}
		ts := entryTime(e.Timestamp, m.Timestamp)
		switch m.Role {
		case "user":
			text := textOf(m.Content)
			if text == "" {
				continue
			}
			turns = append(turns, sessionlink.Turn{Role: "user", Text: sessionlink.Clip(text, sessionlink.TurnTextCap), TS: ts})
		case "assistant":
			t := sessionlink.Turn{Role: "assistant", TS: ts, Model: m.Model}
			var texts []string
			for _, b := range parseBlocks(m.Content) {
				switch b.Type {
				case "text":
					if b.Text != "" {
						texts = append(texts, b.Text)
					}
				case "toolCall":
					tc := sessionlink.MakeTurnToolCall(b.ID, b.Name, sessionlink.RawArgs(b.Arguments), ts)
					if b.ID != "" {
						pos[b.ID] = callPos{turn: len(turns), call: len(t.ToolCalls)}
					}
					t.ToolCalls = append(t.ToolCalls, tc)
				}
			}
			t.Text = sessionlink.Clip(strings.Join(texts, "\n"), sessionlink.TurnTextCap)
			if u := m.Usage; u != nil {
				t.InputTokens, t.OutputTokens = u.Input, u.Output
				t.CacheRead, t.CacheWrite = u.CacheRead, u.CacheWrite
			}
			turns = append(turns, t)
		case "toolResult":
			p, ok := pos[m.ToolCallID]
			if !ok {
				continue
			}
			tc := &turns[p.turn].ToolCalls[p.call]
			tc.Result = sessionlink.Clip(textOf(m.Content), sessionlink.TurnResultCap)
			tc.IsError = m.IsError
		}
	}
	return turns
}

// parseBlocks accepts a content array or a bare string. Malformed blocks are
// dropped one at a time.
func parseBlocks(raw json.RawMessage) []rawBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		return []rawBlock{{Type: "text", Text: s}}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		out := make([]rawBlock, 0, len(items))
		for _, it := range items {
			var b rawBlock
			if json.Unmarshal(it, &b) == nil {
				out = append(out, b)
			}
		}
		return out
	}
	return nil
}

// textOf joins the text blocks of a content value.
func textOf(raw json.RawMessage) string {
	var parts []string
	for _, b := range parseBlocks(raw) {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func entryTime(rfc string, ms json.RawMessage) time.Time {
	if t := sessionlink.ParseRFC3339(rfc); !t.IsZero() {
		return t
	}
	var f float64
	if len(ms) > 0 && json.Unmarshal(ms, &f) == nil && f > 0 {
		return time.UnixMilli(int64(f))
	}
	return time.Time{}
}
