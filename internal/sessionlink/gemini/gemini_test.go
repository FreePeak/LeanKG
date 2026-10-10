package gemini

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// Synthetic fixtures only, generated under t.TempDir(). No local Gemini chat
// exists to confirm against, so the shapes and the sha256(cwd) project hash
// come from docs. Everything here is experimental.

const fixtureCwd = "/synthetic/gemini-project"

var t0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func hashFor(cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	return hex.EncodeToString(sum[:])
}

func jsonLine(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n"
}

// writeChat writes <home>/.gemini/tmp/<hash>/chats/<name> with the given content.
func writeChat(t *testing.T, home, hash, name, content string) string {
	t.Helper()
	dir := filepath.Join(home, ".gemini", "tmp", hash, "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func home(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func hashOf(args map[string]any) string {
	raw, _ := json.Marshal(args)
	return telemetry.ArgsHash(raw)
}

func TestClientExperimentalAndRoots(t *testing.T) {
	a := New()
	if a.Client() != "gemini" {
		t.Errorf("Client() = %q", a.Client())
	}
	if !a.(interface{ Experimental() bool }).Experimental() {
		t.Error("gemini adapter must be experimental until confirmed")
	}
	h := home(t)
	if got := a.Roots(h); got[0] != filepath.Join(h, ".gemini", "tmp") {
		t.Errorf("roots = %v", got)
	}
	if a.Detect(h) {
		t.Error("Detect must be false without a store")
	}
	writeChat(t, h, hashFor(fixtureCwd), "session-x.jsonl", jsonLine(t, map[string]any{"sessionId": "s"}))
	if !a.Detect(h) {
		t.Error("Detect must be true once the tmp store exists")
	}
}

func TestLocateExactBySessionID(t *testing.T) {
	h := home(t)
	const sid = "gem-exact-session"
	p := writeChat(t, h, hashFor(fixtureCwd), "session-2026-10-04T12-00-00-abc.jsonl", strings.Join([]string{
		jsonLine(t, map[string]any{"sessionId": sid, "projectHash": hashFor(fixtureCwd), "startTime": t0.Format(time.RFC3339)}),
		jsonLine(t, map[string]any{"id": "m1", "timestamp": t0.Format(time.RFC3339), "type": "user", "content": "hello"}),
	}, ""))
	ref, conf, err := New().Locate(h, sessionlink.CallRef{ClientSessionID: sid, Cwd: fixtureCwd})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Path != p || float64(conf) != 1 || ref.SessionID != sid {
		t.Errorf("exact = %+v conf=%v", ref, conf)
	}
}

func TestLocateHeuristicByProjectHashTimeToolArgs(t *testing.T) {
	h := home(t)
	args := map[string]any{"q": "gemini-synthetic"}
	callAt := t0.Add(time.Minute)
	p := writeChat(t, h, hashFor(fixtureCwd), "session-h.jsonl", strings.Join([]string{
		jsonLine(t, map[string]any{"sessionId": "gem-h", "projectHash": hashFor(fixtureCwd), "startTime": t0.Format(time.RFC3339)}),
		jsonLine(t, map[string]any{"id": "m1", "timestamp": t0.Format(time.RFC3339), "type": "user", "content": "look"}),
		jsonLine(t, map[string]any{"id": "m2", "timestamp": callAt.Format(time.RFC3339), "type": "gemini", "content": "",
			"toolCalls": []map[string]any{{"id": "tc1", "name": "mcp_leankg_query", "args": args, "status": "success"}}}),
	}, ""))
	writeChat(t, h, hashFor("/synthetic/elsewhere"), "session-o.jsonl", strings.Join([]string{
		jsonLine(t, map[string]any{"sessionId": "gem-o", "projectHash": hashFor("/synthetic/elsewhere")}),
		jsonLine(t, map[string]any{"id": "m2", "timestamp": callAt.Format(time.RFC3339), "type": "gemini", "content": "",
			"toolCalls": []map[string]any{{"id": "tc2", "name": "mcp_leankg_query", "args": args}}}),
	}, ""))
	a := New()
	ref, conf, err := a.Locate(h, sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(args), Cwd: fixtureCwd, TS: callAt.Add(time.Second)})
	if err != nil {
		t.Fatalf("heuristic: %v", err)
	}
	if ref.Path != p || float64(conf) >= 1 {
		t.Errorf("heuristic = %+v conf=%v", ref, conf)
	}
	if _, _, err := a.Locate(h, sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(map[string]any{"q": "no"}), Cwd: fixtureCwd, TS: callAt}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("wrong args: err = %v", err)
	}
}

func TestWindowPromptFollowUpsTokensAndRewrite(t *testing.T) {
	h := home(t)
	args := map[string]any{"q": "gemini-window"}
	callAt := t0.Add(time.Minute)
	p := writeChat(t, h, hashFor(fixtureCwd), "session-w.jsonl", strings.Join([]string{
		jsonLine(t, map[string]any{"sessionId": "gem-w", "projectHash": hashFor(fixtureCwd), "startTime": t0.Format(time.RFC3339)}),
		"this line is garbage",
		"{\"id\": \"broken",
		jsonLine(t, map[string]any{"id": "m1", "timestamp": t0.Format(time.RFC3339), "type": "user", "content": "draft"}),
		// Same id rewritten later: the last version wins.
		jsonLine(t, map[string]any{"id": "m1", "timestamp": t0.Format(time.RFC3339), "type": "user", "content": "final prompt"}),
		jsonLine(t, map[string]any{"id": "m-info", "timestamp": t0.Format(time.RFC3339), "type": "info", "content": "quota notice"}),
		jsonLine(t, map[string]any{"$set": map[string]any{"lastUpdated": callAt.Format(time.RFC3339)}}),
		jsonLine(t, map[string]any{"id": "m2", "timestamp": callAt.Format(time.RFC3339), "type": "gemini", "content": "searching",
			"model":  "synthetic-model",
			"tokens": map[string]any{"input": 400, "cached": 150, "output": 25, "total": 575},
			"toolCalls": []map[string]any{
				{"id": "tc-m", "name": "mcp_leankg_query", "args": args, "status": "success", "result": "3 hits"},
				{"id": "tc-g", "name": "grep_search", "args": map[string]any{"pattern": "Handler"}, "status": "error", "result": "denied"},
			}}),
		jsonLine(t, map[string]any{"id": "m3", "timestamp": callAt.Add(time.Second).Format(time.RFC3339), "type": "gemini", "content": "",
			"toolCalls": []map[string]any{{"id": "tc-r", "name": "read_file", "args": map[string]any{"file_path": "internal/app/x.go"}, "status": "success"}}}),
		jsonLine(t, map[string]any{"id": "m4", "timestamp": callAt.Add(2 * time.Second).Format(time.RFC3339), "type": "user", "content": "next"}),
		jsonLine(t, map[string]any{"id": "m5", "timestamp": callAt.Add(3 * time.Second).Format(time.RFC3339), "type": "gemini", "content": "",
			"toolCalls": []map[string]any{{"id": "tc-b", "name": "run_shell_command", "args": map[string]any{"command": "ls"}}}}),
	}, ""))

	a := New()
	w, err := a.Window(sessionlink.TranscriptRef{Client: "gemini", Path: p}, sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(args), Cwd: fixtureCwd, TS: callAt}, 2)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if w.Prompt != "final prompt" {
		t.Errorf("prompt = %q, want the rewritten final version", w.Prompt)
	}
	if w.Matched == nil || w.Matched.Result != "3 hits" || !w.Matched.IsLeanKG {
		t.Errorf("matched = %+v", w.Matched)
	}
	if len(w.FollowUps) != 2 || w.FollowUps[0].Norm != "grep" || !w.FollowUps[0].IsError {
		t.Fatalf("follow-ups = %+v, want grep (error) then read; bash is after the next prompt", w.FollowUps)
	}
	if w.FollowUps[1].Norm != "read" || w.FollowUps[1].Target != "internal/app/x.go" {
		t.Errorf("follow-up 1 = %+v", w.FollowUps[1])
	}
	all, err := a.Transcript(sessionlink.TranscriptRef{Client: "gemini", Path: p}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range all {
		if strings.Contains(turn.Text, "quota notice") || strings.Contains(turn.Text, "draft") {
			t.Errorf("info or superseded message leaked: %q", turn.Text)
		}
		if turn.Role == "assistant" && turn.Text == "searching" {
			if turn.InputTokens != 250 || turn.CacheRead != 150 || turn.OutputTokens != 25 || turn.Model != "synthetic-model" {
				t.Errorf("tokens = %+v, want input net of cached", turn)
			}
		}
	}
}

func TestLegacyWholeFileJSON(t *testing.T) {
	h := home(t)
	args := map[string]any{"q": "legacy"}
	callAt := t0.Add(time.Minute)
	doc := map[string]any{
		"sessionId": "gem-legacy", "projectHash": hashFor(fixtureCwd), "startTime": t0.Format(time.RFC3339),
		"messages": []map[string]any{
			{"id": "m1", "timestamp": t0.Format(time.RFC3339), "type": "user", "content": "legacy prompt"},
			{"id": "m2", "timestamp": callAt.Format(time.RFC3339), "type": "gemini", "content": "ok",
				"toolCalls": []map[string]any{{"id": "tc1", "name": "mcp_leankg_query", "args": args}}},
		},
	}
	raw, _ := json.Marshal(doc)
	p := writeChat(t, h, hashFor(fixtureCwd), "session-legacy.json", string(raw))
	w, err := New().Window(sessionlink.TranscriptRef{Client: "gemini", Path: p}, sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(args), Cwd: fixtureCwd, TS: callAt}, 1)
	if err != nil {
		t.Fatalf("legacy window: %v", err)
	}
	if w.Prompt != "legacy prompt" || w.SessionID != "gem-legacy" {
		t.Errorf("legacy window = prompt %q session %q", w.Prompt, w.SessionID)
	}
}

func TestUnparseableIsUnsupported(t *testing.T) {
	h := home(t)
	p := writeChat(t, h, hashFor(fixtureCwd), "session-bad.jsonl", "nothing useful\n{\"unrelated\":1}\n")
	if _, err := New().(*adapter).Transcript(sessionlink.TranscriptRef{Path: p}, 0); !errors.Is(err, sessionlink.ErrUnsupportedVersion) {
		t.Errorf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestMissingStoreIsNotFound(t *testing.T) {
	if _, _, err := New().Locate(home(t), sessionlink.CallRef{ClientSessionID: "x", Cwd: fixtureCwd}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
