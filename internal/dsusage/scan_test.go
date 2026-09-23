//go:build dshusage

package dsusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// The retrieval block is emitted at the TAIL of a real answer, well past
// the 2000-char clip applied to Step.Output. This is the regression guard:
// parsing off Output instead of the raw text makes the rung silently empty.
func TestScanFileExtractsRetrievalRungBeyondClipBudget(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "--work-harness--", "session-rung")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	// ~3000 chars of hit content, then the retrieval block at the very end.
	filler := strings.Repeat("x", 3000)
	result := `{"freshness":"fresh","hits":[{"content":"` + filler + `"}],"retrieval":{"reason":"FTS5 keyword match","rung":"L2"}}`
	line, _ := json.Marshal(map[string]any{
		"type": "tool/result", "time": 1003,
		"data": map[string]any{"message": map[string]any{
			"source": map[string]any{"kind": "tool", "callId": "c1"},
			"content": []any{map[string]any{
				"type":    "tool-result",
				"content": []any{map[string]any{"type": "text", "text": result}},
			}},
		}},
	})
	body := `{"type":"tool/call","time":1002,"data":{"turn":1,"step":1,"callId":"c1","name":"mcp__leankg__query","arguments":"{\"query\":\"x\"}"}}
` + string(line) + "\n"
	path := filepath.Join(ws, "session.v4.jsonl")
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
	if steps[0].Rung != "L2" {
		t.Fatalf("rung = %q, want L2 (retrieval sits past the 2000-char clip)", steps[0].Rung)
	}
	if steps[0].RungReason != "FTS5 keyword match" {
		t.Fatalf("rung reason = %q", steps[0].RungReason)
	}
	// L2-only with a plain keyword reason: L3 was never consulted (a keyword
	// hit answered first), which is routing behaviour, not a missing vector.
	if len(si) != 1 || si[0].Rule != "semantic_never_consulted" || si[0].Severity != SevInfo {
		t.Fatalf("expected semantic_never_consulted (info), got %+v", si)
	}
}

func TestSessionIssueSemanticNeverServedSuppressedByL3(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "--work-harness--", "session-l3")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(id, rung string) string {
		res := `{"freshness":"fresh","hits":[{"content":"a"}],"retrieval":{"reason":"r","rung":"` + rung + `"}}`
		line, _ := json.Marshal(map[string]any{
			"type": "tool/result", "time": 1003,
			"data": map[string]any{"message": map[string]any{
				"source": map[string]any{"kind": "tool", "callId": id},
				"content": []any{map[string]any{
					"type":    "tool-result",
					"content": []any{map[string]any{"type": "text", "text": res}},
				}},
			}},
		})
		call := `{"type":"tool/call","time":1002,"data":{"callId":"` + id + `","name":"mcp__leankg__query","arguments":"{\"query\":\"x\"}"}}` + "\n"
		return call + string(line) + "\n"
	}
	body := mk("c1", "L2") + mk("c2", "L3")
	path := filepath.Join(ws, "session.v4.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, si, err := scanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].Rung != "L2" || steps[1].Rung != "L3" {
		t.Fatalf("rungs not parsed: %+v", steps)
	}
	for _, s := range si {
		if s.Rule == "semantic_never_consulted" || s.Rule == "semantic_degraded_no_vectors" {
			t.Fatalf("L3 was served; no semantic-gap issue may fire: %+v", si)
		}
	}
}

// The degraded-from-L3 fix text is the operator's only instruction for
// turning L3 on, so it must stay runnable: an absolute --project (leankg-embed
// otherwise embeds the cwd store, the #370 footgun) and LEANKG_EMBED_BASE_URL
// (FromEnv for provider=local refuses to attach without it and L3 degrades).
func TestSemanticDegradedFixIsActionable(t *testing.T) {
	issues := sessionIssues("session-x", "--work-abc--", sessionStats{
		leankgCalls:    2,
		rungs:          map[string]int{"L2": 2},
		degradedFromL3: 2,
	})
	if len(issues) != 1 || issues[0].Rule != "semantic_degraded_no_vectors" {
		t.Fatalf("got %+v", issues)
	}
	fix := issues[0].Fix
	for _, want := range []string{
		"leankg-embed run",
		"--project",
		"ABSOLUTE-PATH",
		"LEANKG_EMBED_BASE_URL",
	} {
		if !strings.Contains(fix, want) {
			t.Fatalf("fix text missing %q: %s", want, fix)
		}
	}
	if !strings.Contains(issues[0].Detail, "--work-abc--") {
		t.Fatalf("detail must name the workspace: %s", issues[0].Detail)
	}
}

// A degrade reason carrying "degraded from L3" is the only proof semantic was
// attempted, so the scanner must key the actionable no-vectors issue off it.
func TestDegradedFromL3ClassifiesAsMissingVectors(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "--work-abc--", "session-deg")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	res := `{"freshness":"fresh","hits":[{"content":"a"}],"retrieval":{"reason":"no vector collection for model local; degraded from L3","rung":"L2"}}`
	line, _ := json.Marshal(map[string]any{
		"type": "tool/result", "time": 1003,
		"data": map[string]any{"message": map[string]any{
			"source": map[string]any{"kind": "tool", "callId": "c1"},
			"content": []any{map[string]any{
				"type":    "tool-result",
				"content": []any{map[string]any{"type": "text", "text": res}},
			}},
		}},
	})
	body := `{"type":"tool/call","time":1002,"data":{"callId":"c1","name":"mcp__leankg__query","arguments":"{\"query\":\"x\"}"}}` + "\n" + string(line) + "\n"
	if err := os.WriteFile(filepath.Join(ws, "session.v4.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, si, err := scanFile(filepath.Join(ws, "session.v4.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(si) != 1 || si[0].Rule != "semantic_degraded_no_vectors" || si[0].Severity != SevMedium {
		t.Fatalf("degrade must classify as semantic_degraded_no_vectors (medium), got %+v", si)
	}
}
