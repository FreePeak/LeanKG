package claudecode

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

// All fixtures are synthetic and written under t.TempDir().

var t0 = time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)

func ts(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }

// fixtureHome points HOME at a temp dir with no CLAUDE_CONFIG_DIR override and
// returns the projects root under it.
func fixtureHome(t *testing.T) (home, projects string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home = t.TempDir()
	projects = filepath.Join(home, ".claude", "projects")
	return home, projects
}

func line(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func prompt(sid, uuid, cwd string, d time.Duration, text string) string {
	return line(map[string]any{
		"type": "user", "sessionId": sid, "uuid": uuid, "cwd": cwd, "timestamp": ts(d),
		"message": map[string]any{"role": "user", "content": text},
	})
}

func resultLine(sid, uuid, cwd string, d time.Duration, toolUseID, text string, isErr bool) string {
	return line(map[string]any{
		"type": "user", "sessionId": sid, "uuid": uuid, "cwd": cwd, "timestamp": ts(d),
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": text, "is_error": isErr},
		}},
	})
}

func assistantText(sid, uuid, cwd string, d time.Duration, msgID, text string) string {
	return line(map[string]any{
		"type": "assistant", "sessionId": sid, "uuid": uuid, "cwd": cwd, "timestamp": ts(d),
		"message": map[string]any{"id": msgID, "role": "assistant", "model": "synthetic-model",
			"content": []any{map[string]any{"type": "text", "text": text}},
			"usage":   map[string]any{"input_tokens": 11, "output_tokens": 7, "cache_read_input_tokens": 3, "cache_creation_input_tokens": 2}},
	})
}

func toolUse(sid, uuid, cwd string, d time.Duration, msgID, name, id string, input map[string]any) string {
	return line(map[string]any{
		"type": "assistant", "sessionId": sid, "uuid": uuid, "cwd": cwd, "timestamp": ts(d),
		"message": map[string]any{"id": msgID, "role": "assistant", "model": "synthetic-model",
			"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}},
			"usage":   map[string]any{"input_tokens": 5, "output_tokens": 2}},
	})
}

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func argsHash(input map[string]any) string {
	b, _ := json.Marshal(input)
	return telemetry.ArgsHash(b)
}

const cwd = "/work/repo.v2"

// mainSession is a realistic session: a prompt, a grep, the LeanKG query
// under test, a read, then a second prompt with a decoy LeanKG call.
func mainSession(sid string) []string {
	q := map[string]any{"query": "auth handler", "action": "search"}
	decoy := map[string]any{"query": "other thing", "action": "search"}
	return []string{
		prompt(sid, "u1", cwd, 0, "find the auth handler"),
		toolUse(sid, "a1", cwd, 1*time.Second, "m1", "Grep", "tu-grep", map[string]any{"pattern": "auth"}),
		resultLine(sid, "r1", cwd, 2*time.Second, "tu-grep", "auth.go:3", false),
		toolUse(sid, "a2", cwd, 5*time.Second, "m2", "mcp__leankg__query", "tu-lk", q),
		resultLine(sid, "r2", cwd, 6*time.Second, "tu-lk", "ok: AuthHandler", false),
		toolUse(sid, "a3", cwd, 7*time.Second, "m3", "Read", "tu-read", map[string]any{"file_path": "/work/repo.v2/auth.go"}),
		resultLine(sid, "r3", cwd, 8*time.Second, "tu-read", "package auth", false),
		prompt(sid, "u2", cwd, 30*time.Second, "now write the test"),
		toolUse(sid, "a4", cwd, 31*time.Second, "m4", "mcp__leankg__query", "tu-decoy", decoy),
		resultLine(sid, "r4", cwd, 32*time.Second, "tu-decoy", "ok", false),
	}
}

func lkCall(sid string, d time.Duration, args map[string]any, exact bool) sessionlink.CallRef {
	c := sessionlink.CallRef{
		CallID:   "call-" + sid,
		TS:       t0.Add(d),
		Cwd:      cwd,
		Tool:     "query",
		ArgsHash: argsHash(args),
	}
	if exact {
		c.ClientSessionID = sid
	}
	return c
}

func TestDetectAndRoots(t *testing.T) {
	home, projects := fixtureHome(t)
	a := New()
	if a.Client() != "claude-code" {
		t.Fatalf("Client = %q", a.Client())
	}
	if a.Detect(home) {
		t.Fatal("Detect true before the store exists")
	}
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if !a.Detect(home) {
		t.Fatal("Detect false with a projects dir")
	}
	if got := a.Roots(home); len(got) != 1 || got[0] != projects {
		t.Fatalf("Roots = %v, want [%s]", got, projects)
	}
}

func TestRootHonorsClaudeConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/claude")
	if got := root("/home/u"); got != "/custom/claude/projects" {
		t.Fatalf("root = %q, want /custom/claude/projects", got)
	}
}

func TestExactLocateAndWindow(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-exact-1"
	main := filepath.Join(projects, slug(cwd), sid+".jsonl")
	write(t, main, mainSession(sid)...)

	args := map[string]any{"query": "auth handler", "action": "search"}
	c := lkCall(sid, 5*time.Second, args, true)
	a := New()

	ref, conf, err := a.Locate(home, c)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if ref.Path != main || conf != sessionlink.ConfidenceExact {
		t.Fatalf("Locate = %s conf %v, want %s conf 1.0", ref.Path, conf, main)
	}
	if !strings.Contains(main, "-work-repo-v2") {
		t.Fatalf("slug should replace / and . with -: %s", main)
	}

	w, err := a.Window(ref, c, 6)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if w.Prompt != "find the auth handler" {
		t.Fatalf("Prompt = %q, want the prompt before the call (not a tool_result)", w.Prompt)
	}
	if w.Matched == nil || w.Matched.ID != "tu-lk" || w.Matched.Norm != "leankg.query" || !w.Matched.IsLeanKG {
		t.Fatalf("Matched = %+v", w.Matched)
	}
	if !strings.Contains(w.Matched.Result, "AuthHandler") {
		t.Fatalf("matched result not attached: %q", w.Matched.Result)
	}
	if w.Matched.ArgsHash != c.ArgsHash {
		t.Fatalf("matched hash %s != call hash %s", w.Matched.ArgsHash, c.ArgsHash)
	}
	if w.Confidence != sessionlink.ConfidenceExact {
		t.Fatalf("window confidence = %v", w.Confidence)
	}
	// Before: the user prompt, then the grep turn; After: read, prompt 2, decoy.
	if len(w.Before) != 2 || w.Before[0].Role != "user" || w.Before[1].ToolCalls[0].Norm != "grep" {
		t.Fatalf("Before = %+v", w.Before)
	}
	if w.Before[1].InputTokens != 5 || w.Before[1].Model != "synthetic-model" {
		t.Fatalf("per-turn usage/model not carried: %+v", w.Before[1])
	}
	// Follow-ups stop at the next real prompt, so the decoy is excluded.
	if len(w.FollowUps) != 1 || w.FollowUps[0].Norm != "read" || w.FollowUps[0].Target != "/work/repo.v2/auth.go" {
		t.Fatalf("FollowUps = %+v, want only the read of auth.go", w.FollowUps)
	}
}

func TestExactLocateFollowsSubagentWhenCallLivesThere(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-sub-1"
	write(t, filepath.Join(projects, slug(cwd), sid+".jsonl"),
		prompt(sid, "u1", cwd, 0, "main task"),
		assistantText(sid, "a1", cwd, time.Second, "m1", "delegating"),
	)
	subPath := filepath.Join(projects, slug(cwd), sid, "subagents", "agent-x.jsonl")
	args := map[string]any{"query": "sub query"}
	write(t, subPath,
		line(map[string]any{"type": "user", "isSidechain": true, "sessionId": sid, "uuid": "su1", "cwd": cwd, "timestamp": ts(2 * time.Second),
			"message": map[string]any{"role": "user", "content": "subagent task"}}),
		toolUse(sid, "sa1", cwd, 3*time.Second, "sm1", "mcp__leankg__query", "tu-sub", args),
	)
	// The subagent entries are sidechain; mark them so the window isolates them.
	b, _ := os.ReadFile(subPath)
	os.WriteFile(subPath, []byte(strings.ReplaceAll(string(b), `"type":"assistant"`, `"type":"assistant","isSidechain":true`)), 0o600)

	c := lkCall(sid, 3*time.Second, args, true)
	c.Cwd = cwd
	a := New()
	ref, _, err := a.Locate(home, c)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if ref.Path != subPath {
		t.Fatalf("Locate = %s, want the subagent transcript %s", ref.Path, subPath)
	}
	w, err := a.Window(ref, c, 6)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if w.Prompt != "subagent task" {
		t.Fatalf("Prompt = %q, want the subagent task (sidechain isolation)", w.Prompt)
	}
}

func TestExactLocateSearchesOtherProjectDirs(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-moved-1"
	// Stored under a different slug than the call's cwd.
	main := filepath.Join(projects, "-elsewhere", sid+".jsonl")
	args := map[string]any{"query": "auth handler", "action": "search"}
	write(t, main, toolUse(sid, "a1", "/elsewhere", time.Second, "m1", "mcp__leankg__query", "tu-x", args))
	c := lkCall(sid, time.Second, args, true)
	c.Cwd = "/work/repo.v2"

	ref, conf, err := New().Locate(home, c)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if ref.Path != main || conf != sessionlink.ConfidenceExact {
		t.Fatalf("Locate = %s conf %v", ref.Path, conf)
	}
}

func TestExactLocateRefusesMissingCallAndTraversal(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-nocall"
	write(t, filepath.Join(projects, slug(cwd), sid+".jsonl"), mainSession(sid)...)
	a := New()

	// The call is not in the transcript: no false link.
	c := lkCall(sid, 5*time.Second, map[string]any{"query": "never asked"}, true)
	if _, _, err := a.Locate(home, c); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Fatalf("missing call: err = %v, want ErrNotFound", err)
	}

	// A hostile session id must never be used as a path.
	evil := lkCall("../../etc/passwd", 5*time.Second, map[string]any{"query": "x"}, true)
	if _, _, err := a.Locate(home, evil); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Fatalf("traversal id: err = %v, want ErrNotFound", err)
	}
}

func TestSymlinkedTranscriptIsRefused(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-link"
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	write(t, outside, mainSession(sid)...)
	slugDir := filepath.Join(projects, slug(cwd))
	os.MkdirAll(slugDir, 0o755)
	if err := os.Symlink(outside, filepath.Join(slugDir, sid+".jsonl")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	c := lkCall(sid, 5*time.Second, map[string]any{"query": "auth handler", "action": "search"}, true)
	if _, _, err := New().Locate(home, c); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Fatalf("symlink out of the store: err = %v, want ErrNotFound", err)
	}
}

func TestHeuristicLocatePicksCoveringTranscript(t *testing.T) {
	home, projects := fixtureHome(t)
	args := map[string]any{"query": "auth handler", "action": "search"}
	slugDir := filepath.Join(projects, slug(cwd))

	// Old session: same tool and args, but it ended long before the call.
	old := filepath.Join(slugDir, "old-session.jsonl")
	write(t, old,
		prompt("old-session", "u0", cwd, -time.Hour, "old prompt"),
		toolUse("old-session", "a0", cwd, -time.Hour+time.Second, "mo", "mcp__leankg__query", "tu-old", args),
		resultLine("old-session", "r0", cwd, -time.Hour+2*time.Second, "tu-old", "ok", false),
	)
	// Covering session: contains the call near the call time.
	cov := filepath.Join(slugDir, "live-session.jsonl")
	write(t, cov, mainSession("live-session")...)

	c := lkCall("live", 5*time.Second, args, false)
	ref, conf, err := New().Locate(home, c)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if ref.Path != cov {
		t.Fatalf("Locate = %s, want the covering session %s", ref.Path, cov)
	}
	if conf != sessionlink.ConfidenceUnique {
		t.Fatalf("conf = %v, want unique 0.9", conf)
	}
	w, err := New().Window(ref, c, 6)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if w.Matched == nil || w.Matched.ID != "tu-lk" {
		t.Fatalf("Matched = %+v, want tu-lk", w.Matched)
	}
	if w.Confidence != sessionlink.ConfidenceUnique {
		t.Fatalf("window confidence = %v, want 0.9", w.Confidence)
	}
}

func TestHeuristicAmbiguousIsLowConfidence(t *testing.T) {
	home, projects := fixtureHome(t)
	args := map[string]any{"query": "auth handler", "action": "search"}
	slugDir := filepath.Join(projects, slug(cwd))
	for _, sid := range []string{"twin-a", "twin-b"} {
		write(t, filepath.Join(slugDir, sid+".jsonl"), mainSession(sid)...)
	}
	c := lkCall("twin", 5*time.Second, args, false)
	_, conf, err := New().Locate(home, c)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if conf >= sessionlink.HeuristicThreshold {
		t.Fatalf("ambiguous match conf = %v, must be below the threshold %v", conf, sessionlink.HeuristicThreshold)
	}
}

func TestHeuristicRejectsSlugCollision(t *testing.T) {
	home, projects := fixtureHome(t)
	args := map[string]any{"query": "auth handler", "action": "search"}
	// A transcript in the same slug dir but recorded for a different cwd.
	p := filepath.Join(projects, slug(cwd), "collide.jsonl")
	write(t, p, toolUse("collide", "a1", "/work/repo/v2", time.Second, "m1", "mcp__leankg__query", "tu-c", args))
	c := lkCall("x", time.Second, args, false)
	if _, _, err := New().Locate(home, c); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Fatalf("slug collision: err = %v, want ErrNotFound", err)
	}
}

func TestGarbageLinesAreSkippedNotFatal(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-garbage"
	lines := []string{
		"this is not json at all",
		`{"type":"progress","data":{"x":1}}`,
		`{"type":"assistant","sessionId":"` + sid + `","message":"not an object"}`,
		"",
	}
	lines = append(lines, mainSession(sid)...)
	lines = append(lines, `{truncated`)
	write(t, filepath.Join(projects, slug(cwd), sid+".jsonl"), lines...)
	c := lkCall(sid, 5*time.Second, map[string]any{"query": "auth handler", "action": "search"}, true)

	a := New()
	ref, _, err := a.Locate(home, c)
	if err != nil {
		t.Fatalf("Locate on a transcript with garbage lines: %v", err)
	}
	w, err := a.Window(ref, c, 6)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if w.Matched == nil || w.Prompt != "find the auth handler" {
		t.Fatalf("window lost data around garbage: prompt=%q matched=%+v", w.Prompt, w.Matched)
	}
}

func TestUnsupportedFormatIsErrUnsupportedVersion(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-future"
	write(t, filepath.Join(projects, slug(cwd), sid+".jsonl"),
		"not json", `{"kind":"new-format","v":9}`, `[1,2,3]`)
	c := lkCall(sid, 5*time.Second, map[string]any{"query": "x"}, true)
	if _, _, err := New().Locate(home, c); !errors.Is(err, sessionlink.ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestTranscriptCapsTurnsAndResults(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-replay"
	big := strings.Repeat("r", 20000)
	lines := []string{prompt(sid, "u1", cwd, 0, "go")}
	for i := 0; i < 10; i++ {
		lines = append(lines, toolUse(sid, "a"+string(rune('a'+i)), cwd, time.Duration(i+1)*time.Second,
			"m"+string(rune('a'+i)), "Grep", "tu-"+string(rune('a'+i)), map[string]any{"pattern": "x"}))
		lines = append(lines, resultLine(sid, "r"+string(rune('a'+i)), cwd, time.Duration(i+1)*time.Second+time.Millisecond,
			"tu-"+string(rune('a'+i)), big, false))
	}
	p := filepath.Join(projects, slug(cwd), sid+".jsonl")
	write(t, p, lines...)
	a := New()
	turns, err := a.Transcript(sessionlink.TranscriptRef{Path: p, SessionID: sid}, 4)
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(turns) != 4 {
		t.Fatalf("turns = %d, want 4 (maxTurns cap)", len(turns))
	}
	res := turns[1].ToolCalls[0].Result
	if len(res) > sessionlink.ResultCap+len("...[truncated]") {
		t.Fatalf("tool result not capped: %d bytes", len(res))
	}
	_ = home
}

func TestRealUserPromptSkipsToolResultOnlyEntries(t *testing.T) {
	home, projects := fixtureHome(t)
	sid := "sess-prompt"
	args := map[string]any{"query": "q"}
	write(t, filepath.Join(projects, slug(cwd), sid+".jsonl"),
		prompt(sid, "u1", cwd, 0, "real prompt"),
		toolUse(sid, "a1", cwd, time.Second, "m1", "Read", "tu-r", map[string]any{"file_path": "/x"}),
		resultLine(sid, "r1", cwd, 2*time.Second, "tu-r", "content", false),
		toolUse(sid, "a2", cwd, 3*time.Second, "m2", "mcp__leankg__query", "tu-q", args),
	)
	c := lkCall(sid, 3*time.Second, args, true)
	ref, _, err := New().Locate(home, c)
	if err != nil {
		t.Fatal(err)
	}
	w, err := New().Window(ref, c, 6)
	if err != nil {
		t.Fatal(err)
	}
	if w.Prompt != "real prompt" {
		t.Fatalf("Prompt = %q, want the real prompt, not the tool_result entry", w.Prompt)
	}
}
