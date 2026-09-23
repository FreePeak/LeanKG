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
	steps, si, err := scanFile(path)
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
	if len(si) != 0 {
		t.Fatalf("session issues for leankg session = %+v", si)
	}
}

func TestScanFileV4IsRead(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "--work-onegw--", "session-v4")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user/message","time":2000,"data":{"text":"status of onegw"}}
{"type":"assistant/message","time":2001,"data":{"message":{"content":[{"type":"text","text":"I will check status."}]}}}
{"type":"tool/call","time":2002,"data":{"turn":1,"step":1,"callId":"v4c1","name":"mcp__leankg__status","arguments":"{\"project\":\"onegw\"}"}}
{"type":"tool/result","time":2003,"data":{"message":{"source":{"kind":"tool","callId":"v4c1"},"content":[{"type":"text","text":"ok"}],"isError":false}}
`
	path := filepath.Join(ws, "session.v4.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, si, err := scanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Tool != "mcp__leankg__status" {
		t.Fatalf("v4 scan missed step: %+v", steps)
	}
	if len(si) != 0 {
		t.Fatalf("session issues for leankg session = %+v", si)
	}
}

func TestScanFileLocalToolsNoLeanKG(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "--work-menu--", "session-local")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user/message","time":3000,"data":{"text":"find the promo code"}}
{"type":"assistant/message","time":3001,"data":{"message":{"content":[{"type":"text","text":"I'll grep for it."}]}}}
{"type":"tool/call","time":3002,"data":{"name":"bash","arguments":"{\"command\":\"grep promo\"}"}}
{"type":"tool/result","time":3003,"data":{"message":{"source":{"kind":"tool","callId":"bash1"},"content":[{"type":"text","text":"DISCOUNT=10"}],"isError":false}}
`
	path := filepath.Join(ws, "session.v4.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, si, err := scanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("expected no leankg steps, got %+v", steps)
	}
	if len(si) != 1 || si[0].Rule != "no_leankg_in_code_session" {
		t.Fatalf("expected no_leankg_in_code_session issue, got %+v", si)
	}
}

func TestScanRootPrefersV4OverV3(t *testing.T) {
	root := t.TempDir()
	sid := "session-prefers-v4"
	ws := filepath.Join(root, "--work-x--", sid)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	v3 := `{"type":"user/message","time":1000,"data":{"text":"v3"}}
{"type":"tool/call","time":1001,"data":{"callId":"v3only","name":"mcp__leankg__status","arguments":"{\"project\":\"x\"}"}}
`
	v4 := `{"type":"user/message","time":2000,"data":{"text":"v4"}}
{"type":"tool/call","time":2001,"data":{"callId":"v4only","name":"mcp__leankg__query","arguments":"{\"query\":\"x\"}"}}
`
	if err := os.WriteFile(filepath.Join(ws, "session.v3.jsonl"), []byte(v3), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "session.v4.jsonl"), []byte(v4), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, si, err := ScanRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(si) != 0 {
		t.Fatalf("unexpected session issues for leankg session: %+v", si)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step (v4 only), got %d: %+v", len(steps), steps)
	}
	if steps[0].CallID != "v4only" {
		t.Fatalf("expected v4 call, got %s", steps[0].CallID)
	}
}
