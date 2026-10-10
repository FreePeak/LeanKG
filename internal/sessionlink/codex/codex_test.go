package codex

import (
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

// Synthetic fixtures only, generated under t.TempDir(). The entry shapes
// (session_meta with a payload, response_item, event_msg) follow a structure
// survey of a local rollout; function_call and token_count shapes come from docs.

const fixtureCwd = "/synthetic/codex-project"

var t0 = time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)

func ent(at time.Time, typ string, payload map[string]any) map[string]any {
	return map[string]any{"timestamp": at.Format(time.RFC3339Nano), "type": typ, "payload": payload}
}

func msg(at time.Time, role, text string) map[string]any {
	kind := "input_text"
	if role == "assistant" {
		kind = "output_text"
	}
	return ent(at, "response_item", map[string]any{
		"type": "message", "role": role,
		"content": []map[string]any{{"type": kind, "text": text}},
	})
}

func fnCall(at time.Time, name string, args map[string]any, callID string) map[string]any {
	raw, _ := json.Marshal(args)
	return ent(at, "response_item", map[string]any{
		"type": "function_call", "name": name, "arguments": string(raw), "call_id": callID,
	})
}

func fnOutput(at time.Time, callID, out string) map[string]any {
	return ent(at, "response_item", map[string]any{"type": "function_call_output", "call_id": callID, "output": out})
}

func meta(id, cwd string) map[string]any {
	return ent(t0, "session_meta", map[string]any{"id": id, "cwd": cwd, "timestamp": t0.Format(time.RFC3339Nano), "cli_version": "0.0.0-synthetic"})
}

func writeRollout(t *testing.T, root, uuid string, lines ...any) string {
	t.Helper()
	dir := filepath.Join(root, "2026", "10", "03")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("not json at the top\n")
	for _, l := range lines {
		switch v := l.(type) {
		case string:
			b.WriteString(v + "\n")
		default:
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteString("\n")
		}
	}
	p := filepath.Join(dir, "rollout-2026-10-03T20-00-00-"+uuid+".jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

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
	t.Setenv("CODEX_HOME", filepath.Dir(root))
	return New()
}

func hashOf(args map[string]any) string {
	raw, _ := json.Marshal(args)
	return telemetry.ArgsHash(raw)
}

func TestClientExperimentalAndRoots(t *testing.T) {
	a := New()
	if a.Client() != "codex" {
		t.Errorf("Client() = %q", a.Client())
	}
	if !a.(interface{ Experimental() bool }).Experimental() {
		t.Error("codex adapter must be experimental until confirmed")
	}
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if got := a.Roots(home); got[0] != filepath.Join(codexHome, "sessions") {
		t.Errorf("roots with CODEX_HOME = %v", got)
	}
	t.Setenv("CODEX_HOME", "")
	if got := a.Roots(home); got[0] != filepath.Join(home, ".codex", "sessions") {
		t.Errorf("default root = %v", got)
	}
	if a.Detect(home) {
		t.Error("Detect must be false without a store")
	}
}

func TestLocateExactByPayloadIDAndFilenameUUID(t *testing.T) {
	root := newRoot(t)
	const sid = "019a-exact-session"
	const uuid = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001"
	p := writeRollout(t, root, uuid, meta(sid, fixtureCwd), msg(t0, "user", "hi"))
	a := withRoot(t, root)
	ref, conf, err := a.Locate("", sessionlink.CallRef{ClientSessionID: sid, Cwd: fixtureCwd})
	if err != nil || ref.Path != p || float64(conf) != 1 {
		t.Fatalf("exact by payload id: ref=%+v conf=%v err=%v", ref, conf, err)
	}
	ref, conf, err = a.Locate("", sessionlink.CallRef{ClientSessionID: uuid, Cwd: fixtureCwd})
	if err != nil || ref.Path != p || float64(conf) != 1 {
		t.Fatalf("exact by filename uuid: ref=%+v conf=%v err=%v", ref, conf, err)
	}
}

func TestLocateHeuristicByCwdTimeToolArgs(t *testing.T) {
	root := newRoot(t)
	args := map[string]any{"q": "codex-synthetic"}
	callAt := t0.Add(time.Minute)
	p := writeRollout(t, root, "uuid-heur-0001", meta("sid-h", fixtureCwd),
		msg(t0, "user", "find it"),
		fnCall(callAt, "mcp__leankg__query", args, "call-1"),
		fnOutput(callAt.Add(time.Second), "call-1", "hits"),
	)
	writeRollout(t, root, "uuid-heur-0002", meta("sid-o", "/synthetic/other"),
		msg(t0, "user", "other"),
		fnCall(callAt, "mcp__leankg__query", args, "call-2"),
	)
	a := withRoot(t, root)
	ref, conf, err := a.Locate("", sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(args), Cwd: fixtureCwd, TS: callAt.Add(time.Second)})
	if err != nil {
		t.Fatalf("heuristic: %v", err)
	}
	if ref.Path != p || float64(conf) >= 1 {
		t.Errorf("heuristic = %+v conf=%v", ref, conf)
	}
	if _, _, err := a.Locate("", sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(map[string]any{"q": "x"}), Cwd: fixtureCwd, TS: callAt}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("wrong args: err = %v", err)
	}
}

func TestWindowSkipsInjectedContextAndAttachesTokens(t *testing.T) {
	root := newRoot(t)
	args := map[string]any{"q": "codex-window"}
	callAt := t0.Add(time.Minute)
	p := writeRollout(t, root, "uuid-win-0001", meta("sid-w", fixtureCwd),
		msg(t0.Add(-time.Minute), "user", "<environment_context>synthetic env block</environment_context>"),
		msg(t0, "user", "real prompt"),
		msg(t0.Add(time.Second), "assistant", "searching"),
		ent(callAt, "event_msg", map[string]any{"type": "token_count", "info": map[string]any{
			"last_token_usage": map[string]any{"input_tokens": 500, "cached_input_tokens": 200, "output_tokens": 30},
		}}),
		fnCall(callAt, "mcp__leankg__query", args, "call-m"),
		fnOutput(callAt.Add(time.Second), "call-m", "3 hits"),
		fnCall(callAt.Add(2*time.Second), "grep", map[string]any{"pattern": "Handler"}, "call-g"),
		fnOutput(callAt.Add(3*time.Second), "call-g", "none"),
		msg(callAt.Add(4*time.Second), "user", "next question"),
		fnCall(callAt.Add(5*time.Second), "bash", map[string]any{"command": "ls"}, "call-b"),
	)
	w, err := withRoot(t, root).Window(sessionlink.TranscriptRef{Client: "codex", Path: p}, sessionlink.CallRef{Tool: "query", ArgsHash: hashOf(args), Cwd: fixtureCwd, TS: callAt}, 2)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if w.Prompt != "real prompt" {
		t.Errorf("prompt = %q, want the real user text, not injected context", w.Prompt)
	}
	if w.Matched == nil || w.Matched.Result != "3 hits" || w.Matched.Norm != "leankg.query" {
		t.Errorf("matched = %+v", w.Matched)
	}
	if len(w.FollowUps) != 1 || w.FollowUps[0].Norm != "grep" || w.FollowUps[0].Result != "none" {
		t.Errorf("follow-ups = %+v, want the grep only", w.FollowUps)
	}
	all, err := withRoot(t, root).(*adapter).Transcript(sessionlink.TranscriptRef{Client: "codex", Path: p}, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundTokens := false
	for _, turn := range all {
		if turn.Role == "assistant" && turn.InputTokens == 300 && turn.CacheRead == 200 && turn.OutputTokens == 30 {
			foundTokens = true
		}
	}
	if !foundTokens {
		t.Errorf("token_count not attached to the assistant turn: turns=%+v", all)
	}
}

func TestMissingStoreIsNotFound(t *testing.T) {
	a := withRoot(t, filepath.Join(t.TempDir(), "sessions"))
	if _, _, err := a.Locate("", sessionlink.CallRef{ClientSessionID: "x", Cwd: fixtureCwd}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
