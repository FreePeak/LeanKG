//go:build dshusage

package dsusage

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ScanRoots walks DSH session directories and returns LeanKG tool steps,
// newest first. An empty roots list means ~/.dsh/sessions.
//
// ponytail: one zstd process per session file, no index. Fine for a few
// hundred local sessions; append a JSONL from a hook if this is polled
// harder than a dashboard refresh.
func ScanRoots(roots []string) ([]Step, error) {
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		roots = []string{filepath.Join(home, ".dsh", "sessions")}
	}
	var steps []Step
	for _, root := range roots {
		found, err := scanRoot(root)
		if err != nil {
			return steps, err
		}
		steps = append(steps, found...)
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].TimeMS > steps[j].TimeMS })
	return steps, nil
}

func scanRoot(root string) ([]Step, error) {
	var steps []Step
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(d.Name(), "session.v3.jsonl") {
			return nil
		}
		got, err := scanFile(path)
		if err != nil {
			return nil
		}
		steps = append(steps, got...)
		return nil
	})
	return steps, err
}

func scanFile(path string) ([]Step, error) {
	rc, err := openLog(path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	ws, sid := sessionIdentity(path)
	var (
		userText    string
		agentBefore string
		steps       []Step
		byCall      = map[string]int{}
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
				continue
			}
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
			steps[idx].Output = clip(text, 2000)
			steps[idx].IsError = isErr
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := steps[:0]
	for i := range steps {
		steps[i].Issues = Classify(steps[i])
		steps[i].TopSeverity = Top(steps[i].Issues)
		out = append(out, steps[i])
	}
	return out, nil
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
