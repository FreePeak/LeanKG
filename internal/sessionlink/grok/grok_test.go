package grok

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// Synthetic fixtures only, generated under t.TempDir(). No real Grok session
// is read or committed. The update shapes follow the ACP-style docs, not a
// local sample: the only local sample carried hook_execution updates.

const fixtureCwd = "/synthetic/grok-project"

var t0 = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)

func encodeCwd(cwd string) string { return url.PathEscape(cwd) }

// writeGrokSession creates <root>/<enc cwd>/<sid>/{summary.json,updates.jsonl}.
func writeGrokSession(t *testing.T, root, cwd, sid string, created, updated time.Time, updates []map[string]any) string {
	t.Helper()
	dir := filepath.Join(root, encodeCwd(cwd), sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := map[string]any{
		"info":       map[string]any{"id": sid, "cwd": cwd},
		"created_at": created.Format(time.RFC3339Nano),
		"updated_at": updated.Format(time.RFC3339Nano),
	}
	raw, _ := json.Marshal(sum)
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("this line is not json\n")
	for _, u := range updates {
		line, err := json.Marshal(u)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteString("\n")
	}
	b.WriteString("{\"method\":\"session/update\",\"params\":\n")
	p := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func update(at time.Time, promptIndex int, u map[string]any) map[string]any {
	return map[string]any{
		"method":    "session/update",
		"timestamp": at.UnixMilli(),
		"params": map[string]any{
			"sessionId": "ignored",
			"_meta":     map[string]any{"agentTimestampMs": at.UnixMilli(), "promptIndex": promptIndex, "eventId": "e"},
			"update":    u,
		},
	}
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func newRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func withRoot(t *testing.T, root string) sessionlink.Adapter {
	t.Helper()
	t.Setenv("GROK_HOME", filepath.Dir(root))
	return New()
}

func hashOf(args map[string]any) string {
	raw, _ := json.Marshal(args)
	return telemetry.ArgsHash(raw)
}

func TestClientExperimentalAndRootsHonorEnv(t *testing.T) {
	a := New()
	if a.Client() != "grok" {
		t.Errorf("Client() = %q", a.Client())
	}
	if !a.(interface{ Experimental() bool }).Experimental() {
		t.Error("grok adapter must be marked experimental until a local sample confirms it")
	}
	home := t.TempDir()
	grokHome := t.TempDir()
	t.Setenv("GROK_HOME", grokHome)
	if got := a.Roots(home); got[0] != filepath.Join(grokHome, "sessions") {
		t.Errorf("roots with GROK_HOME = %v", got)
	}
	t.Setenv("GROK_HOME", "")
	if got := a.Roots(home); got[0] != filepath.Join(home, ".grok", "sessions") {
		t.Errorf("default root = %v", got)
	}
	if a.Detect(home) {
		t.Error("Detect must be false without a store")
	}
}

func TestLocateExactBySummaryID(t *testing.T) {
	root := newRoot(t)
	const sid = "01a0-exact-sid"
	p := writeGrokSession(t, root, fixtureCwd, sid, t0, t0.Add(time.Minute), nil)
	a := withRoot(t, root)
	ref, conf, err := a.Locate("", sessionlink.CallRef{ClientSessionID: sid, Cwd: fixtureCwd})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Path != p || ref.Client != "grok" || float64(conf) != 1 {
		t.Errorf("exact locate = %+v conf=%v", ref, conf)
	}
}

func TestLocateHeuristicByCwdTimeToolArgs(t *testing.T) {
	root := newRoot(t)
	args := map[string]any{"q": "grok-synthetic"}
	callAt := t0.Add(time.Minute)
	p := writeGrokSession(t, root, fixtureCwd, "sid-h", t0, callAt.Add(time.Minute), []map[string]any{
		update(t0, 0, map[string]any{"sessionUpdate": "user_message_chunk", "content": text("look up things")}),
		update(callAt, 0, map[string]any{"sessionUpdate": "tool_call", "tool_call_id": "tc-1", "name": "mcp__leankg__query", "rawInput": args}),
	})
	writeGrokSession(t, root, "/synthetic/other", "sid-other", t0, callAt.Add(time.Minute), []map[string]any{
		update(callAt, 0, map[string]any{"sessionUpdate": "tool_call", "tool_call_id": "tc-9", "name": "mcp__leankg__query", "rawInput": args}),
	})
	a := withRoot(t, root)
	ref, conf, err := a.Locate("", sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(args), Cwd: fixtureCwd, TS: callAt.Add(time.Second)})
	if err != nil {
		t.Fatalf("heuristic: %v", err)
	}
	if ref.Path != p || float64(conf) >= 1 {
		t.Errorf("heuristic = %+v conf=%v", ref, conf)
	}
	if _, _, err := a.Locate("", sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(map[string]any{"q": "nope"}), Cwd: fixtureCwd, TS: callAt}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("wrong args: err = %v", err)
	}
}

func TestWindowPromptFollowUpsAndAssembly(t *testing.T) {
	root := newRoot(t)
	args := map[string]any{"q": "match-grok"}
	callAt := t0.Add(time.Minute)
	p := writeGrokSession(t, root, fixtureCwd, "sid-w", t0, callAt.Add(3*time.Minute), []map[string]any{
		update(t0, 0, map[string]any{"sessionUpdate": "user_message_chunk", "content": text("first ")}),
		update(t0, 0, map[string]any{"sessionUpdate": "user_message_chunk", "content": text("prompt")}),
		update(t0, 0, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": text("secret reasoning")}),
		update(t0, 0, map[string]any{"sessionUpdate": "agent_message_chunk", "content": text("looking")}),
		update(callAt, 0, map[string]any{"sessionUpdate": "tool_call", "tool_call_id": "tc-m", "name": "mcp__leankg__query", "rawInput": args}),
		update(callAt, 0, map[string]any{"sessionUpdate": "tool_call_update", "tool_call_id": "tc-m", "status": "completed", "rawOutput": "3 hits"}),
		update(callAt, 0, map[string]any{"sessionUpdate": "tool_call", "tool_call_id": "tc-g", "name": "grep", "rawInput": map[string]any{"pattern": "Handler"}}),
		update(callAt, 0, map[string]any{"sessionUpdate": "tool_call_update", "tool_call_id": "tc-g", "status": "failed", "rawOutput": "boom"}),
		update(callAt, 1, map[string]any{"sessionUpdate": "user_message_chunk", "content": text("next")}),
		update(callAt, 1, map[string]any{"sessionUpdate": "tool_call", "tool_call_id": "tc-b", "name": "bash", "rawInput": map[string]any{"command": "ls"}}),
	})
	w, err := withRoot(t, root).Window(sessionlink.TranscriptRef{Client: "grok", Path: p}, sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(args), Cwd: fixtureCwd, TS: callAt}, 2)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if w.Prompt != "first prompt" {
		t.Errorf("prompt = %q", w.Prompt)
	}
	if w.Matched == nil || w.Matched.Result != "3 hits" || !w.Matched.IsLeanKG {
		t.Errorf("matched = %+v", w.Matched)
	}
	if len(w.FollowUps) != 1 || w.FollowUps[0].Norm != "grep" || !w.FollowUps[0].IsError {
		t.Errorf("follow-ups = %+v, want the failed grep only (bash is after the next prompt)", w.FollowUps)
	}
	for _, turn := range w.Before {
		if strings.Contains(turn.Text, "secret reasoning") {
			t.Error("thought chunks must not enter the transcript")
		}
	}
}

func TestTranscriptSkipsGarbageAndCaps(t *testing.T) {
	root := newRoot(t)
	big := strings.Repeat("x", 9000)
	p := writeGrokSession(t, root, fixtureCwd, "sid-t", t0, t0, []map[string]any{
		update(t0, 0, map[string]any{"sessionUpdate": "user_message_chunk", "content": text(big)}),
		update(t0, 0, map[string]any{"sessionUpdate": "hook_execution", "event_name": "x"}),
	})
	turns, err := withRoot(t, root).(*adapter).Transcript(sessionlink.TranscriptRef{Path: p}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || len(turns[0].Text) > 8<<10+len("...[truncated]") {
		t.Errorf("turns = %d, text len = %d; want one capped user turn", len(turns), len(turns[0].Text))
	}
}

func TestMissingStoreIsNotFound(t *testing.T) {
	a := withRoot(t, filepath.Join(t.TempDir(), "sessions"))
	if _, _, err := a.Locate("", sessionlink.CallRef{ClientSessionID: "x", Cwd: fixtureCwd}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
