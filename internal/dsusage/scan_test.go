//go:build dshusage

package dsusage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFileJoinsCallResultAndAgent(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "--work-be--", "session-abc")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user/message","time":1000,"data":{"content":[{"type":"text","text":"trace menu cadence"}]}}
{"type":"assistant/message","time":1001,"data":{"message":{"content":[{"type":"reasoning","text":"hidden"},{"type":"text","text":"I will query LeanKG."}]}}}
{"type":"tool/call","time":1002,"data":{"turn":1,"step":2,"callId":"c1","name":"mcp__leankg__query","arguments":"{\"query\":\"menu proto\"}"}}
{"type":"tool/result","time":1003,"data":{"message":{"source":{"kind":"tool","callId":"c1"},"content":[{"type":"tool-result","content":[{"type":"text","text":"{\"hits\":[]}"}],"isError":false}]}}}
{"type":"assistant/message","time":1004,"data":{"message":{"content":[{"type":"text","text":"No hits, falling back to grep."}]}}}
{"type":"tool/call","time":1005,"data":{"name":"grep","arguments":"{}"}}
`
	path := filepath.Join(ws, "session.v3.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, err := scanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps %d", len(steps))
	}
	s := steps[0]
	if s.Tool != "mcp__leankg__query" || s.Output == "" {
		t.Fatalf("%+v", s)
	}
	if s.AgentBefore != "I will query LeanKG." || s.AgentAfter != "No hits, falling back to grep." {
		t.Fatalf("agent before=%q after=%q", s.AgentBefore, s.AgentAfter)
	}
	if s.UserPrompt != "trace menu cadence" {
		t.Fatalf("prompt %q", s.UserPrompt)
	}
	if s.Workspace != "--work-be--" || s.SessionID != "session-abc" {
		t.Fatalf("id %s %s", s.Workspace, s.SessionID)
	}
	if !hasRule(s.Issues, "project_not_passed") {
		t.Fatalf("issues %+v", s.Issues)
	}
}
