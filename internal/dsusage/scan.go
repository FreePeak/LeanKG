//go:build dshusage

package dsusage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ScanRoots walks DSH session directories and returns LeanKG tool steps,
// newest first. An empty roots list means ~/.dsh/sessions.
// It covers session.v3.jsonl* and session.v4.jsonl*; when a session
// has both formats only v4 is used so one call is not counted twice.
//
// ponytail: one zstd process per session file, no index. Fine for a few
// hundred local sessions; append a JSONL from a hook if this is polled
// harder than a dashboard refresh.
func ScanRoots(roots []string) ([]Step, []SessionIssue, error) {
	roots, err := defaultRoots(roots)
	if err != nil {
		return nil, nil, err
	}
	var steps []Step
	var allIssues []SessionIssue
	for _, root := range roots {
		got, si, err := scanRoot(root)
		if err != nil {
			return steps, allIssues, err
		}
		steps = append(steps, got...)
		allIssues = append(allIssues, si...)
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].TimeMS > steps[j].TimeMS })
	return steps, allIssues, nil
}

// SessionIssue is a session-level finding computed from the tool mix,
// not from a single LeanKG call. These feed the dashboard and the
// enhancement backlog (e.g. "agent never called LeanKG this session").
type SessionIssue struct {
	Rule      string   `json:"rule"`
	Severity  Severity `json:"severity"`
	Title     string   `json:"title"`
	Detail    string   `json:"detail"`
	Fix       string   `json:"fix"`
	SessionID string   `json:"session_id,omitempty"`
	Workspace string   `json:"workspace,omitempty"`
}

// sessionStats tracks per-session tool usage so ScanRoots can surface
// sessions that never called LeanKG despite doing code search locally.
type sessionStats struct {
	leankgCalls int
	totalTools  int
	// rungs counts ladder rungs that actually answered (L1/L2/L3), so a
	// session can report that its queries never reached semantic search.
	rungs map[string]int
	// degradedFromL3 counts answers whose reason says L3 WAS attempted and
	// degraded (e.g. "no vector collection ... degraded from L3"). This is
	// the one case that proves semantic was reachable-but-broken, as opposed
	// to a keyword/exact hit that simply answered first or an agent that
	// pinned action=exact/fuzzy and never consulted L3.
	degradedFromL3 int
}

// parseRetrieval pulls retrieval.rung / retrieval.reason out of a LeanKG
// tool result. The block is emitted last in the response body and real
// answers run thousands of chars (hit contents), so the caller must pass
// the UNCAPPED text — Step.Output is clipped to 2000 and would miss it.
func parseRetrieval(text string) (rung, reason string) {
	i := strings.LastIndex(text, `"retrieval"`)
	if i < 0 {
		return "", ""
	}
	j := strings.Index(text[i:], "{")
	if j < 0 {
		return "", ""
	}
	// The value object may be followed by sibling braces from the enclosing
	// body; Decoder stops after the first complete JSON value, so trailing
	// braces are ignored.
	var env struct {
		Rung   string `json:"rung"`
		Reason string `json:"reason"`
	}
	if json.NewDecoder(strings.NewReader(text[i+j:])).Decode(&env) != nil {
		return "", ""
	}
	return env.Rung, env.Reason
}

func scanRoot(root string) ([]Step, []SessionIssue, error) {
	var steps []Step
	var sessIssues []SessionIssue
	// sessionsWithV4: session dirs that already have a v4 log; skip v3
	// for those so the same tool call is not counted twice.
	sessionsWithV4 := map[string]struct{}{}

	// First pass: record which session dirs have v4 logs.
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "session-") {
			return nil
		}
		hasV4, _ := hasLogFile(path, "session.v4.jsonl")
		if hasV4 {
			sessionsWithV4[name] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Second pass: scan logs, preferring v4 over v3 per session.
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		isV4 := strings.HasPrefix(name, "session.v4.jsonl")
		isV3 := strings.HasPrefix(name, "session.v3.jsonl")
		if !isV4 && !isV3 {
			return nil
		}
		// Prefer v4: skip v3 when this session already has v4.
		if isV3 {
			_, sid := sessionIdentity(path)
			if _, ok := sessionsWithV4[sid]; ok {
				return nil
			}
		}
		got, si, err := scanFile(path)
		if err != nil {
			return nil
		}
		steps = append(steps, got...)
		sessIssues = append(sessIssues, si...)
		return nil
	})
	return steps, sessIssues, err
}

func hasLogFile(sessionDir, prefix string) (bool, error) {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			return true, nil
		}
	}
	return false, nil
}

func scanFile(path string) ([]Step, []SessionIssue, error) {
	rc, err := openLog(path)
	if err != nil {
		return nil, nil, err
	}
	defer rc.Close()
	ws, sid := sessionIdentity(path)
	var (
		userText    string
		agentBefore string
		steps       []Step
		byCall      = map[string]int{}
		stats       sessionStats
	)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var ev event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "user/message":
			if t := textOf(ev.Data); t != "" && !strings.HasPrefix(strings.TrimSpace(t), "<system-reminder>") {
				userText = clip(t, 500)
			}
		case "assistant/message":
			t := assistantText(ev.Data)
			if t == "" {
				continue
			}
			t = clip(t, 500)
			if n := len(steps); n > 0 && steps[n-1].AgentAfter == "" {
				steps[n-1].AgentAfter = t
			}
			agentBefore = t
		case "tool/call":
			name, _ := ev.Data["name"].(string)
			if !strings.Contains(name, "leankg") {
				stats.totalTools++
				continue
			}
			stats.leankgCalls++
			callID, _ := ev.Data["callId"].(string)
			st := Step{
				SessionID:   sid,
				Workspace:   ws,
				TimeMS:      ev.Time,
				Turn:        asInt(ev.Data["turn"]),
				Step:        asInt(ev.Data["step"]),
				CallID:      callID,
				Tool:        name,
				Input:       stringify(ev.Data["arguments"]),
				AgentBefore: agentBefore,
				UserPrompt:  userText,
			}
			byCall[callID] = len(steps)
			steps = append(steps, st)
		case "tool/result":
			msg, _ := ev.Data["message"].(map[string]any)
			if msg == nil {
				continue
			}
			src, _ := msg["source"].(map[string]any)
			callID, _ := src["callId"].(string)
			idx, ok := byCall[callID]
			if !ok {
				continue
			}
			text, isErr := resultText(msg)
			// Parse the rung from the UNCAPPED text: the retrieval block
			// sits at the tail of a multi-KB body, past Output's 2000 clip.
			steps[idx].Rung, steps[idx].RungReason = parseRetrieval(text)
			if r := steps[idx].Rung; r != "" {
				if stats.rungs == nil {
					stats.rungs = map[string]int{}
				}
				stats.rungs[r]++
			}
			if strings.Contains(strings.ToLower(steps[idx].RungReason), "degraded from l3") {
				stats.degradedFromL3++
			}
			steps[idx].Output = clip(text, 2000)
			steps[idx].IsError = isErr
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	out := steps[:0]
	for i := range steps {
		steps[i].Issues = Classify(steps[i])
		steps[i].TopSeverity = Top(steps[i].Issues)
		out = append(out, steps[i])
	}
	return out, sessionIssues(sid, ws, stats), nil
}

func sessionIssues(sid, ws string, s sessionStats) []SessionIssue {
	var out []SessionIssue
	if s.totalTools > 0 && s.leankgCalls == 0 {
		// Session used local tools (bash/grep/read/glob) but never called LeanKG.
		out = append(out, SessionIssue{
			Rule:      "no_leankg_in_code_session",
			Severity:  SevHigh,
			Title:     "Session searched code without LeanKG",
			Detail:    sid + ": " + fmt.Sprintf("%d", s.totalTools) + " local tool calls, 0 LeanKG calls",
			Fix:       "Query LeanKG first in DSH sessions: leankg query with project=<repo>.",
			SessionID: sid,
			Workspace: ws,
		})
	}
	if s.rungs["L3"] == 0 {
		answered := s.rungs["L1"] + s.rungs["L2"] + s.rungs["L3"]
		if answered > 0 {
			mix := fmt.Sprintf("%s [%s]: rung mix L1 %d · L2 %d · L3 %d",
				sid, ws, s.rungs["L1"], s.rungs["L2"], s.rungs["L3"])
			if s.degradedFromL3 > 0 {
				// Semantic WAS reached and degraded: the project has no
				// vectors. This is the actionable infrastructure gap.
				out = append(out, SessionIssue{
					Rule:     "semantic_degraded_no_vectors",
					Severity: SevMedium,
					Title:    "Semantic search was reached but degraded (no vectors)",
					Detail:   fmt.Sprintf("%s · %d answer(s) degraded from L3", mix, s.degradedFromL3),
					Fix: "This project has no vectors, so L3 degrades to keyword. Build them ONCE per " +
						"project: scripts/embed-runtime.sh on && " +
						"LEANKG_EMBED_BASE_URL=http://127.0.0.1:9101/v1 " +
						"leankg-embed run --project <ABSOLUTE-PATH>. Always pass --project; without it " +
						"leankg-embed embeds the cwd store instead.",
					SessionID: sid,
					Workspace: ws,
				})
			} else {
				// No L3 attempt: either a keyword/exact hit answered first, or
				// the agent pinned action=exact/fuzzy. Routing behaviour, not
				// a missing collection — flag it differently.
				out = append(out, SessionIssue{
					Rule:     "semantic_never_consulted",
					Severity: SevInfo,
					Title:    "Semantic search was never consulted (no L3, but none degraded either)",
					Detail:   mix,
					Fix: "L1/L2 answered first (a keyword or exact hit short-circuits the ladder) or the " +
						"call pinned action=exact/fuzzy, which bypasses L3. Not a missing-vector problem. " +
						"Pass no action (or action=search) to let the ladder reach L3 on semantic questions.",
					SessionID: sid,
					Workspace: ws,
				})
			}
		}
	}
	return out
}

type event struct {
	Type string         `json:"type"`
	Time int64          `json:"time"`
	Data map[string]any `json:"data"`
}

func openLog(path string) (io.ReadCloser, error) {
	if strings.HasSuffix(path, ".zstd") {
		cmd := exec.Command("zstd", "-dc", "-q", path)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &cmdReader{r: stdout, cmd: cmd}, nil
	}
	return os.Open(path)
}

type cmdReader struct {
	r   io.ReadCloser
	cmd *exec.Cmd
}

func (c *cmdReader) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *cmdReader) Close() error {
	_ = c.r.Close()
	return c.cmd.Wait()
}

func sessionIdentity(path string) (workspace, id string) {
	dir := filepath.Dir(path)
	return filepath.Base(filepath.Dir(dir)), filepath.Base(dir)
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func asInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	default:
		return 0
	}
}

func textOf(data map[string]any) string {
	if data == nil {
		return ""
	}
	if s, ok := data["text"].(string); ok {
		return s
	}
	content, _ := data["content"].([]any)
	return joinText(content)
}

func assistantText(data map[string]any) string {
	msg, _ := data["message"].(map[string]any)
	if msg == nil {
		return textOf(data)
	}
	content, _ := msg["content"].([]any)
	var b strings.Builder
	for _, item := range content {
		m, _ := item.(map[string]any)
		if m == nil || m["type"] == "reasoning" {
			continue
		}
		if s, ok := m["text"].(string); ok {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func joinText(content []any) string {
	var b strings.Builder
	for _, item := range content {
		switch t := item.(type) {
		case string:
			b.WriteString(t)
		case map[string]any:
			if s, ok := t["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

func resultText(msg map[string]any) (string, bool) {
	content, _ := msg["content"].([]any)
	var b strings.Builder
	isErr := false
	for _, item := range content {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if m["isError"] == true {
			isErr = true
		}
		switch inner := m["content"].(type) {
		case string:
			b.WriteString(inner)
		case []any:
			b.WriteString(joinText(inner))
		}
		if s, ok := m["text"].(string); ok {
			b.WriteString(s)
		}
	}
	text := b.String()
	low := strings.ToLower(text)
	if strings.HasPrefix(text, "ERROR") || strings.Contains(low, "error:") {
		isErr = true
	}
	return text, isErr
}
