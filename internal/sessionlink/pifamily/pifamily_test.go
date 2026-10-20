package pifamily

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

// Synthetic fixtures only. Every path, id and prompt here is invented and is
// generated under t.TempDir(); no real transcript is read or committed.

const fixtureCwd = "/synthetic/project-a"

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

type line map[string]any

func jsonl(t *testing.T, lines ...any) string {
	t.Helper()
	var b strings.Builder
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
	return b.String()
}

func header(id, cwd string) line {
	return line{"type": "session", "id": id, "version": 3, "cwd": cwd, "timestamp": t0.Format(time.RFC3339Nano)}
}

func userMsg(id, parent, text string, at time.Time) line {
	return line{"type": "message", "id": id, "parentId": parent, "timestamp": at.Format(time.RFC3339Nano),
		"message": line{"role": "user", "content": []line{{"type": "text", "text": text}}, "timestamp": at.UnixMilli()}}
}

func assistantMsg(id, parent string, at time.Time, blocks []line, usage line) line {
	m := line{"role": "assistant", "content": blocks, "model": "synthetic-model", "timestamp": at.UnixMilli()}
	if usage != nil {
		m["usage"] = usage
	}
	return line{"type": "message", "id": id, "parentId": parent, "timestamp": at.Format(time.RFC3339Nano), "message": m}
}

func toolCall(id, name string, args line) line {
	return line{"type": "toolCall", "id": id, "name": name, "arguments": args}
}

func toolResult(id, parent, callID, name, text string, at time.Time, isErr bool) line {
	return line{"type": "message", "id": id, "parentId": parent, "timestamp": at.Format(time.RFC3339Nano),
		"message": line{"role": "toolResult", "toolCallId": callID, "toolName": name, "isError": isErr,
			"content": []line{{"type": "text", "text": text}}, "timestamp": at.UnixMilli()}}
}

// writeSession writes content to <root>/<slug>/<ts>_<uuid>.jsonl and returns the path.
func writeSession(t *testing.T, root, slug, uuid string, at time.Time, content string) string {
	t.Helper()
	dir := filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := at.UTC().Format("2006-01-02T15-04-05.000Z") + "_" + uuid + ".jsonl"
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func leankgCall(t *testing.T, args line) (string, string) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return telemetry.ArgsHash(raw), "mcp__leankg__query"
}

func piRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNewClientNames(t *testing.T) {
	for _, name := range []string{"pi", "omp", "xdev"} {
		if got := New(name).Client(); got != name {
			t.Errorf("New(%q).Client() = %q", name, got)
		}
	}
}

func TestDetectAndRootsHonorEnv(t *testing.T) {
	home := t.TempDir()
	piDir := t.TempDir()
	ompDir := filepath.Join(t.TempDir(), "omp-sessions")
	xdevDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", piDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", ompDir)
	t.Setenv("XDEV_AGENT_DIR", xdevDir)
	if err := os.MkdirAll(filepath.Join(piDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}

	pi := New("pi")
	if got := pi.Roots(home); len(got) != 1 || got[0] != filepath.Join(piDir, "sessions") {
		t.Errorf("pi roots = %v", got)
	}
	if !pi.Detect(home) {
		t.Error("pi Detect should see PI_CODING_AGENT_DIR/sessions")
	}
	if New("omp").Detect(home) {
		t.Error("omp Detect should be false when its session dir does not exist")
	}
	if got := New("omp").Roots(home); got[0] != ompDir {
		t.Errorf("omp root = %q, want %q", got[0], ompDir)
	}
	if got := New("xdev").Roots(home); got[0] != filepath.Join(xdevDir, "sessions") {
		t.Errorf("xdev root = %q", got[0])
	}
}

func TestDetectDefaultsUnderHome(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("XDEV_AGENT_DIR", "")
	home := t.TempDir()
	want := filepath.Join(home, ".xdev", "agent", "sessions")
	if got := New("xdev").Roots(home); got[0] != want {
		t.Errorf("xdev default root = %q, want %q", got[0], want)
	}
	if New("xdev").Detect(home) {
		t.Error("Detect must be false with no store")
	}
}

func TestLocateExactByHeaderAndFilenameID(t *testing.T) {
	root := piRoot(t)
	const sid = "sid-exact-0001"
	const uuid = "uuid-exact-0002"
	writeSession(t, root, "--synthetic-project-a--", "uuid-other-9999", t0,
		jsonl(t, header("sid-other", fixtureCwd), userMsg("u1", "", "other", t0)))
	p2 := writeSession(t, root, "--synthetic-project-a--", uuid, t0.Add(time.Minute),
		jsonl(t, header(sid, fixtureCwd), userMsg("u1", "", "hello", t0)))

	a := adapterWithRoot(t, root)
	ref, conf, err := a.Locate("", sessionlink.CallRef{ClientSessionID: sid, Cwd: fixtureCwd})
	if err != nil {
		t.Fatalf("exact by header id: %v", err)
	}
	if ref.Path != p2 || float64(conf) != 1.0 || ref.SessionID != sid {
		t.Errorf("exact header locate = %+v conf=%v", ref, conf)
	}
	ref, conf, err = a.Locate("", sessionlink.CallRef{ClientSessionID: uuid, Cwd: fixtureCwd})
	if err != nil {
		t.Fatalf("exact by filename uuid: %v", err)
	}
	if ref.Path != p2 || float64(conf) != 1.0 {
		t.Errorf("exact filename locate = %+v conf=%v", ref, conf)
	}
}

func TestLocateHeuristicByCwdTimeToolAndArgs(t *testing.T) {
	root := piRoot(t)
	args := line{"q": "synthetic-symbol", "limit": 5}
	hash, _ := leankgCall(t, args)
	callAt := t0.Add(2 * time.Minute)
	content := jsonl(t, header("sid-h", fixtureCwd), userMsg("u1", "", "find things", t0),
		assistantMsg("a1", "u1", callAt, []line{toolCall("c1", "mcp__leankg__query", args)}, nil))
	p := writeSession(t, root, "--synthetic-project-a--", "uuid-h", t0, content)
	// A second session in another cwd with identical args must not match.
	writeSession(t, root, "--synthetic-other--", "uuid-x", t0,
		jsonl(t, header("sid-x", "/synthetic/other"), userMsg("u1", "", "x", t0),
			assistantMsg("a1", "u1", callAt, []line{toolCall("c1", "mcp__leankg__query", args)}, nil)))

	a := adapterWithRoot(t, root)
	ref, conf, err := a.Locate("", sessionlink.CallRef{Tool: "query", ArgsHash: hash, Cwd: fixtureCwd, TS: callAt.Add(time.Second)})
	if err != nil {
		t.Fatalf("heuristic locate: %v", err)
	}
	if ref.Path != p {
		t.Errorf("heuristic path = %s, want %s", ref.Path, p)
	}
	if float64(conf) >= 1.0 || float64(conf) <= 0 {
		t.Errorf("heuristic confidence = %v, want in (0,1)", conf)
	}
}

func TestLocateHeuristicRejectsWrongArgsOrTime(t *testing.T) {
	root := piRoot(t)
	callAt := t0.Add(2 * time.Minute)
	writeSession(t, root, "--synthetic-project-a--", "uuid-r", t0,
		jsonl(t, header("sid-r", fixtureCwd), userMsg("u1", "", "p", t0),
			assistantMsg("a1", "u1", callAt, []line{toolCall("c1", "mcp__leankg__query", line{"q": "one"})}, nil)))
	a := adapterWithRoot(t, root)
	wrongHash, _ := leankgCall(t, line{"q": "two"})
	if _, _, err := a.Locate("", sessionlink.CallRef{Tool: "query", ArgsHash: wrongHash, Cwd: fixtureCwd, TS: callAt}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("wrong args: err = %v, want ErrNotFound", err)
	}
	goodHash, _ := leankgCall(t, line{"q": "one"})
	if _, _, err := a.Locate("", sessionlink.CallRef{Tool: "query", ArgsHash: goodHash, Cwd: fixtureCwd, TS: t0.Add(3 * time.Hour)}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("outside time window: err = %v, want ErrNotFound", err)
	}
	if _, _, err := a.Locate("", sessionlink.CallRef{Tool: "query", ArgsHash: goodHash, Cwd: "/synthetic/elsewhere", TS: callAt}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("cwd mismatch: err = %v, want ErrNotFound", err)
	}
}

// Tree: u1 -> a1 (abandoned call "abandoned") ; u1 -> u2 -> a2 (active call "active").
// The last line is on the active branch, so the abandoned call must not appear.
func TestActiveBranchOnlyIsTraversed(t *testing.T) {
	root := piRoot(t)
	abandoned := line{"q": "abandoned"}
	active := line{"q": "active"}
	p := writeSession(t, root, "--synthetic-project-a--", "uuid-b", t0, jsonl(t,
		header("sid-b", fixtureCwd),
		userMsg("u1", "", "first prompt", t0),
		assistantMsg("a1", "u1", t0.Add(time.Second), []line{toolCall("c-old", "mcp__leankg__query", abandoned)}, nil),
		userMsg("u2", "u1", "second prompt", t0.Add(time.Minute)),
		assistantMsg("a2", "u2", t0.Add(time.Minute+time.Second), []line{toolCall("c-new", "mcp__leankg__query", active)}, nil),
	))
	turns, err := adapterWithRoot(t, root).(*adapter).Transcript(sessionlink.TranscriptRef{Client: "pi", Path: p}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range turns {
		for _, tc := range turn.ToolCalls {
			if tc.ID == "c-old" {
				t.Fatalf("abandoned branch call leaked into transcript: %+v", tc)
			}
		}
	}
	oldHash := telemetry.ArgsHash(mustJSON(t, abandoned))
	a := adapterWithRoot(t, root)
	if _, err := a.Window(sessionlink.TranscriptRef{Client: "pi", Path: p}, sessionlink.CallRef{Tool: "query", ArgsHash: oldHash, Cwd: fixtureCwd}, 2); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("abandoned call window err = %v, want ErrNotFound", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWindowPromptBeforeAfterAndFollowUps(t *testing.T) {
	root := piRoot(t)
	q := line{"q": "match-me"}
	hash := telemetry.ArgsHash(mustJSON(t, q))
	callAt := t0.Add(time.Minute)
	p := writeSession(t, root, "--synthetic-project-a--", "uuid-w", t0, jsonl(t,
		header("sid-w", fixtureCwd),
		userMsg("u0", "", "earlier question", t0.Add(-time.Minute)),
		assistantMsg("a0", "u0", t0.Add(-30*time.Second), []line{{"type": "text", "text": "earlier answer"}}, nil),
		userMsg("u1", "a0", "find the handler", t0),
		assistantMsg("a1", "u1", callAt, []line{
			{"type": "text", "text": "searching"},
			toolCall("c-match", "mcp__leankg__query", q),
			toolCall("c-grep", "grep", line{"pattern": "Handler"}),
		}, usageFixture),
		toolResult("r1", "a1", "c-match", "mcp__leankg__query", "hits: 3", callAt.Add(time.Second), false),
		toolResult("r2", "r1", "c-grep", "grep", "err: none", callAt.Add(2*time.Second), true),
		assistantMsg("a2", "r2", callAt.Add(3*time.Second), []line{toolCall("c-read", "read", line{"file_path": "internal/app/handler.go"})}, nil),
		userMsg("u2", "a2", "next task", callAt.Add(time.Minute)),
		assistantMsg("a3", "u2", callAt.Add(2*time.Minute), []line{toolCall("c-bash", "bash", line{"command": "ls"})}, nil),
	))
	w, err := adapterWithRoot(t, root).Window(sessionlink.TranscriptRef{Client: "pi", Path: p, SessionID: "sid-w"},
		sessionlink.CallRef{Tool: "query", ArgsHash: hash, Cwd: fixtureCwd, TS: callAt}, 1)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if w.Prompt != "find the handler" {
		t.Errorf("prompt = %q, want the last user text before the call", w.Prompt)
	}
	if w.Matched == nil || w.Matched.ID != "c-match" || w.Matched.Norm != "leankg.query" || !w.Matched.IsLeanKG {
		t.Fatalf("matched = %+v", w.Matched)
	}
	if w.Matched.Result != "hits: 3" || w.Matched.IsError {
		t.Errorf("matched result = %q err=%v", w.Matched.Result, w.Matched.IsError)
	}
	if len(w.Before) != 1 || w.Before[0].Role != "user" || w.Before[0].Text != "find the handler" {
		t.Errorf("before (n=1) = %+v, want the prompt turn only", w.Before)
	}
	if len(w.After) != 1 || w.After[0].Role != "assistant" {
		t.Errorf("after (n=1) = %+v", w.After)
	}
	if len(w.FollowUps) != 2 {
		t.Fatalf("follow-ups = %+v, want grep and read only (not the bash after the next prompt)", w.FollowUps)
	}
	if w.FollowUps[0].Norm != "grep" || w.FollowUps[0].IsError != true {
		t.Errorf("follow-up 0 = %+v, want grep with error", w.FollowUps[0])
	}
	if w.FollowUps[1].Norm != "read" || w.FollowUps[1].Target != "internal/app/handler.go" {
		t.Errorf("follow-up 1 = %+v, want read targeting file_path", w.FollowUps[1])
	}
	if w.Confidence == 0 {
		t.Error("window confidence must be set")
	}
}

var usageFixture = line{"input": 1200, "output": 85, "cacheRead": 900, "cacheWrite": 40, "totalTokens": 2225}

func TestTokenUsageMappedPerAssistantTurn(t *testing.T) {
	root := piRoot(t)
	p := writeSession(t, root, "--synthetic-project-a--", "uuid-u", t0, jsonl(t,
		header("sid-u", fixtureCwd),
		userMsg("u1", "", "count", t0),
		assistantMsg("a1", "u1", t0.Add(time.Second), []line{{"type": "text", "text": "ok"}}, usageFixture),
	))
	turns, err := adapterWithRoot(t, root).(*adapter).Transcript(sessionlink.TranscriptRef{Path: p}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(turns))
	}
	got := turns[1]
	if got.InputTokens != 1200 || got.OutputTokens != 85 || got.CacheRead != 900 || got.CacheWrite != 40 || got.Model != "synthetic-model" {
		t.Errorf("assistant turn tokens = %+v", got)
	}
}

func TestGarbageLinesAndXdevTitleSkipped(t *testing.T) {
	root := piRoot(t)
	args := line{"q": "garbage-case"}
	hash := telemetry.ArgsHash(mustJSON(t, args))
	callAt := t0.Add(time.Minute)
	content := jsonl(t,
		"title: synthetic fixed-width title line that is not JSON",
		"{\"type\":\"session\",\"id\":\"sid-g\",\"cwd\":\""+fixtureCwd+"\",\"timestamp\":\""+t0.Format(time.RFC3339Nano)+"\",\"version\":3}",
		"this is not json at all",
		"{truncated json",
		"[1,2,3]",
		"",
		userMsg("u1", "", "still works", t0),
		"{\"type\":\"message\",\"id\":\"bad\"",
		assistantMsg("a1", "u1", callAt, []line{toolCall("c1", "mcp__leankg__query", args)}, nil),
	)
	p := writeSession(t, root, "--synthetic-project-a--", "uuid-g", t0, content)
	a := adapterWithRoot(t, root)
	w, err := a.Window(sessionlink.TranscriptRef{Path: p}, sessionlink.CallRef{Tool: "query", ArgsHash: hash, Cwd: fixtureCwd, TS: callAt}, 3)
	if err != nil {
		t.Fatalf("window over garbage: %v", err)
	}
	if w.Prompt != "still works" {
		t.Errorf("prompt = %q", w.Prompt)
	}
}

func TestUnparseableStructureIsUnsupported(t *testing.T) {
	root := piRoot(t)
	p := writeSession(t, root, "--synthetic-project-a--", "uuid-n", t0, "garbage only\nno json here\n")
	if _, err := adapterWithRoot(t, root).(*adapter).Transcript(sessionlink.TranscriptRef{Path: p}, 0); !errors.Is(err, sessionlink.ErrUnsupportedVersion) {
		t.Errorf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestSymlinkOutsideStoreIgnored(t *testing.T) {
	root := piRoot(t)
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(jsonl(t, header("sid-link", fixtureCwd))), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "--synthetic-project-a--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "2026-10-01T09-00-00.000Z_uuid-link.jsonl")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, _, err := adapterWithRoot(t, root).Locate("", sessionlink.CallRef{ClientSessionID: "sid-link", Cwd: fixtureCwd}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("symlinked transcript must not be followed: err = %v", err)
	}
}

func TestMissingStoreIsNotFound(t *testing.T) {
	a := adapterWithRoot(t, filepath.Join(t.TempDir(), "absent"))
	if _, _, err := a.Locate("", sessionlink.CallRef{ClientSessionID: "x", Cwd: fixtureCwd}); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// adapterWithRoot returns the adapter for "pi" with its store pointed at root.
func adapterWithRoot(t *testing.T, root string) sessionlink.Adapter {
	t.Helper()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", root)
	return New("omp")
}
