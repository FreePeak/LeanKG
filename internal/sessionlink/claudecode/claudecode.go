// Package claudecode is the sessionlink adapter for Claude Code transcripts:
// <root>/<cwd-slug>/<sessionId>.jsonl, with subagent transcripts under
// <root>/<cwd-slug>/<sessionId>/subagents/*.jsonl. Root is
// $CLAUDE_CONFIG_DIR/projects when that is set, else ~/.claude/projects.
// Read-only; it never follows a symlink out of the store.
package claudecode

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

const clientName = "claude-code"

type adapter struct{}

// New returns the Claude Code adapter.
func New() sessionlink.Adapter { return adapter{} }

func (adapter) Client() string { return clientName }

// root is the projects directory under the configured Claude config dir.
func root(home string) string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	return filepath.Join(home, ".claude", "projects")
}

func (adapter) Detect(home string) bool {
	fi, err := os.Stat(root(home))
	return err == nil && fi.IsDir()
}

func (adapter) Roots(home string) []string { return []string{root(home)} }

// slug maps a working directory to the store's directory name.
func slug(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// Locate finds the transcript for c. An exact client session id is used when
// present; otherwise the heuristic (cwd slug, time range, tool and args hash)
// picks the newest covering transcript.
func (a adapter) Locate(home string, c sessionlink.CallRef) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	r := root(home)
	if c.ClientSessionID != "" {
		return locateExact(r, c)
	}
	return locateHeuristic(r, c)
}

func locateExact(r string, c sessionlink.CallRef) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	sid := c.ClientSessionID
	if !sessionlink.ValidSessionID(sid) {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	var mains []string
	unsupported := false
	if c.Cwd != "" {
		mains = append(mains, filepath.Join(r, slug(c.Cwd), sid+".jsonl"))
	}
	if more, _ := filepath.Glob(filepath.Join(r, "*", sid+".jsonl")); len(more) > 0 {
		mains = append(mains, more...)
	}
	for _, main := range mains {
		for _, p := range append([]string{main}, subagentFiles(main, sid)...) {
			ev, _, err := parseFile(p)
			if errors.Is(err, sessionlink.ErrUnsupportedVersion) {
				unsupported = true
			}
			if err != nil {
				continue
			}
			if sessionlink.CountMatches(ev, c) > 0 {
				return sessionlink.TranscriptRef{Client: clientName, Path: p, SessionID: sid}, sessionlink.ConfidenceExact, nil
			}
		}
	}
	if unsupported {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrUnsupportedVersion
	}
	return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
}

// subagentFiles lists <dir>/<sid>/subagents/*.jsonl for a main transcript.
func subagentFiles(main, sid string) []string {
	dir := filepath.Join(filepath.Dir(main), sid, "subagents")
	m, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	return m
}

type candidate struct {
	path    string
	mtime   time.Time
	matches int
}

func locateHeuristic(r string, c sessionlink.CallRef) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	if c.Cwd == "" || c.Tool == "" {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	dir := filepath.Join(r, slug(c.Cwd))
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	mains, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	var cands []candidate
	total := 0
	unsupported := false
	for _, main := range mains {
		files := append([]string{main}, subagentFiles(main, strings.TrimSuffix(filepath.Base(main), ".jsonl"))...)
		for _, p := range files {
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			// Cheap prefilter: a transcript last written before the call
			// cannot hold the call's tool_result.
			if !c.TS.IsZero() && fi.ModTime().Before(c.TS.Add(-sessionlink.CallLookBack)) {
				continue
			}
			ev, cwd, err := parseFile(p)
			if errors.Is(err, sessionlink.ErrUnsupportedVersion) {
				unsupported = true
			}
			if err != nil || !coversTime(ev, c.TS) || !sameCwd(cwd, c.Cwd) {
				continue
			}
			n := sessionlink.CountMatchesNear(ev, c)
			if n == 0 {
				continue
			}
			total += n
			cands = append(cands, candidate{path: p, mtime: fi.ModTime(), matches: n})
		}
	}
	if len(cands) == 0 {
		if unsupported {
			return sessionlink.TranscriptRef{}, 0, sessionlink.ErrUnsupportedVersion
		}
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	best := cands[0]
	for _, cd := range cands[1:] {
		if cd.mtime.After(best.mtime) {
			best = cd
		}
	}
	return sessionlink.TranscriptRef{Client: clientName, Path: best.path, SessionID: sessionIDOf(best.path)},
		sessionlink.ConfidenceFor(total), nil
}

// coversTime reports whether ts falls inside the transcript's time range.
func coversTime(ev []sessionlink.Event, ts time.Time) bool {
	if ts.IsZero() {
		return true
	}
	var first, last time.Time
	for _, e := range ev {
		t := e.Turn.TS
		if t.IsZero() {
			continue
		}
		if first.IsZero() || t.Before(first) {
			first = t
		}
		if t.After(last) {
			last = t
		}
	}
	return !first.IsZero() && !first.After(ts) && !last.Before(ts)
}

// sameCwd is true when the transcript's cwd, if any entry recorded one, is
// the call's cwd. This rejects slug collisions such as /a.b and /a/b.
func sameCwd(transcriptCwd, cwd string) bool {
	return cwd == "" || transcriptCwd == "" || transcriptCwd == cwd
}

func sessionIDOf(p string) string {
	return strings.TrimSuffix(filepath.Base(p), ".jsonl")
}

// Window extracts up to n turns before and after the matched call.
func (adapter) Window(t sessionlink.TranscriptRef, c sessionlink.CallRef, n int) (sessionlink.Window, error) {
	ev, _, err := parseFile(t.Path)
	if err != nil {
		return sessionlink.Window{}, err
	}
	conf := sessionlink.ConfidenceExact
	if c.ClientSessionID == "" || t.SessionID != c.ClientSessionID {
		conf = sessionlink.ConfidenceFor(sessionlink.CountMatches(ev, c))
	}
	return sessionlink.BuildWindow(clientName, t.SessionID, t.Path, ev, c, n, conf)
}

// Transcript returns the whole file's turns, capped at maxTurns.
func (adapter) Transcript(t sessionlink.TranscriptRef, maxTurns int) ([]sessionlink.Turn, error) {
	ev, _, err := parseFile(t.Path)
	if err != nil {
		return nil, err
	}
	return sessionlink.TurnsOf(ev, maxTurns), nil
}

// parseFile reads a transcript into adapter-neutral events and the first cwd
// it records. Unknown or malformed lines are skipped. A non-empty file with no
// recognizable entry is an unsupported format, not a crash.
func parseFile(path string) ([]sessionlink.Event, string, error) {
	r := projectsRoot(path)
	if r == "" {
		return nil, "", sessionlink.ErrNotFound
	}
	f, err := sessionlink.OpenUnder(r, path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	return parseReader(f)
}

// projectsRoot is the nearest ancestor of a transcript named "projects". All
// transcripts, including subagent ones, live under it.
func projectsRoot(path string) string {
	for d := filepath.Dir(path); d != filepath.Dir(d); d = filepath.Dir(d) {
		if filepath.Base(d) == "projects" {
			return d
		}
	}
	return ""
}

type entry struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"sessionId"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
	Cwd         string          `json:"cwd"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

type message struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   struct {
		In         int64 `json:"input_tokens"`
		Out        int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func parseReader(rd io.Reader) ([]sessionlink.Event, string, error) {
	br := bufio.NewReaderSize(rd, 64<<10)
	var (
		events     []sessionlink.Event
		byMsgID    = map[string]int{}
		results    = map[string]toolResult{}
		cwd        string
		lines      int
		recognized int
	)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if s := strings.TrimSpace(string(line)); s != "" {
				lines++
				if handleLine([]byte(s), &events, byMsgID, results, &cwd) {
					recognized++
				}
			}
		}
		if err != nil {
			break
		}
	}
	if lines > 0 && recognized == 0 {
		return nil, "", sessionlink.ErrUnsupportedVersion
	}
	// Attach tool results to the calls that made them.
	for i := range events {
		for j := range events[i].Turn.ToolCalls {
			tc := &events[i].Turn.ToolCalls[j]
			if r, ok := results[tc.ID]; ok {
				sessionlink.SetResult(tc, r.text, r.isErr)
			}
		}
	}
	return events, cwd, nil
}

type toolResult struct {
	text  string
	isErr bool
}

// handleLine parses one JSONL entry into events. It reports whether the line
// was a recognized entry.
func handleLine(line []byte, events *[]sessionlink.Event, byMsgID map[string]int, results map[string]toolResult, cwd *string) bool {
	var e entry
	if json.Unmarshal(line, &e) != nil {
		return false
	}
	if *cwd == "" && e.Cwd != "" {
		*cwd = e.Cwd
	}
	if e.Type != "user" && e.Type != "assistant" {
		return e.SessionID != "" || e.UUID != ""
	}
	var m message
	if json.Unmarshal(e.Message, &m) != nil || len(m.Content) == 0 {
		return false
	}
	ts, _ := time.Parse(time.RFC3339Nano, e.Timestamp)

	// Content is either a plain string or an array of typed blocks.
	var blocks []block
	var plain string
	if json.Unmarshal(m.Content, &plain) != nil {
		if json.Unmarshal(m.Content, &blocks) != nil {
			return false
		}
	}

	switch e.Type {
	case "user":
		var text []string
		if plain != "" {
			text = append(text, plain)
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					text = append(text, b.Text)
				}
			case "tool_result":
				results[b.ToolUseID] = toolResult{text: resultText(b.Content), isErr: b.IsError}
			}
		}
		if len(text) == 0 || e.IsMeta {
			return true
		}
		*events = append(*events, sessionlink.Event{
			Sidechain: e.IsSidechain,
			Prompt:    true,
			Turn: sessionlink.Turn{
				Role: "user",
				Text: sessionlink.Clip(strings.Join(text, "\n"), sessionlink.TextCap),
				TS:   ts,
			},
		})
	case "assistant":
		idx, seen := -1, false
		if m.ID != "" {
			idx, seen = byMsgID[m.ID]
		}
		var ev *sessionlink.Event
		if seen {
			ev = &(*events)[idx]
		} else {
			*events = append(*events, sessionlink.Event{
				Sidechain: e.IsSidechain,
				Turn: sessionlink.Turn{
					Role:         "assistant",
					TS:           ts,
					InputTokens:  m.Usage.In,
					OutputTokens: m.Usage.Out,
					CacheRead:    m.Usage.CacheRead,
					CacheWrite:   m.Usage.CacheWrite,
					Model:        m.Model,
				},
			})
			ev = &(*events)[len(*events)-1]
			if m.ID != "" {
				byMsgID[m.ID] = len(*events) - 1
			}
		}
		var text []string
		if plain != "" {
			text = append(text, plain)
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					text = append(text, b.Text)
				}
			case "tool_use":
				ev.Turn.ToolCalls = append(ev.Turn.ToolCalls, sessionlink.MakeToolCall(b.ID, b.Name, rawOrEmpty(b.Input), ts))
			}
		}
		if len(text) > 0 {
			joined := sessionlink.Clip(strings.Join(text, "\n"), sessionlink.TextCap)
			if ev.Turn.Text == "" {
				ev.Turn.Text = joined
			} else {
				ev.Turn.Text = sessionlink.Clip(ev.Turn.Text+"\n"+joined, sessionlink.TextCap)
			}
		}
	}
	return true
}

// resultText flattens a tool_result content (string or text blocks).
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []block
	if json.Unmarshal(raw, &bs) == nil {
		var out []string
		for _, b := range bs {
			if b.Text != "" {
				out = append(out, b.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

func rawOrEmpty(r json.RawMessage) []byte {
	if len(r) == 0 {
		return []byte("{}")
	}
	return r
}
