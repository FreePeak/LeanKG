package opencode

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// All fixtures are synthetic and built under t.TempDir().

const schema = `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT NOT NULL, title TEXT NOT NULL DEFAULT '',
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL);
`

var t0 = time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC)

func ms(d time.Duration) int64 { return t0.Add(d).UnixMilli() }

// fixtureHome points HOME at a temp dir and clears XDG_DATA_HOME, so the
// store lives at <home>/.local/share/opencode/opencode.db.
func fixtureHome(t *testing.T) (home, dbFile string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", "")
	home = t.TempDir()
	dir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(dir, "opencode.db")
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func argsHash(v any) string {
	b, _ := json.Marshal(v)
	return telemetry.ArgsHash(b)
}

const cwd = "/work/oc-repo"

// buildStore writes a session with: a user prompt, an assistant turn with a
// grep and the LeanKG query under test, a read, a second prompt and a decoy.
func buildStore(t *testing.T, dbFile, sid, dir string, start time.Duration) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbFile)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExec(t, db, schema)
	mustExec(t, db, `INSERT INTO session(id, directory, title, time_created, time_updated) VALUES (?,?,?,?,?)`,
		sid, dir, "synthetic", ms(start), ms(start+time.Minute))

	addMsg := func(id, role string, at time.Duration) {
		mustExec(t, db, `INSERT INTO message(id, session_id, time_created, data) VALUES (?,?,?,?)`,
			id, sid, ms(at), jsonOf(map[string]any{"role": role, "modelID": "synthetic-model",
				"time":   map[string]any{"created": ms(at)},
				"tokens": map[string]any{"input": 9, "output": 4, "cache": map[string]any{"read": 1, "write": 2}}}))
	}
	addPart := func(id, msg string, at time.Duration, data map[string]any) {
		mustExec(t, db, `INSERT INTO part(id, message_id, session_id, time_created, data) VALUES (?,?,?,?,?)`,
			id, msg, sid, ms(at), jsonOf(data))
	}
	text := func(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
	tool := func(callID, name string, input map[string]any, out string) map[string]any {
		return map[string]any{"type": "tool", "tool": name, "callID": callID, "state": map[string]any{
			"status": "completed", "input": input, "output": out, "time": map[string]any{"start": ms(0), "end": ms(0)}}}
	}

	q := map[string]any{"query": "auth handler", "action": "search"}
	decoy := map[string]any{"query": "other thing", "action": "search"}

	addMsg(sid+"-u1", "user", start)
	addPart(sid+"-p1", sid+"-u1", start, text("find the auth handler"))
	addMsg(sid+"-a1", "assistant", start+time.Second)
	addPart(sid+"-p2", sid+"-a1", start+time.Second, tool("call-grep", "grep", map[string]any{"pattern": "auth"}, "auth.go:3"))
	addMsg(sid+"-a2", "assistant", start+5*time.Second)
	addPart(sid+"-p3", sid+"-a2", start+5*time.Second, text("asking leankg"))
	addPart(sid+"-p4", sid+"-a2", start+5*time.Second, tool("call-lk", "leankg_query", q, "ok: AuthHandler"))
	addPart(sid+"-p5", sid+"-a2", start+6*time.Second, tool("call-read", "read", map[string]any{"filePath": "/work/oc-repo/auth.go"}, "package auth"))
	addMsg(sid+"-u2", "user", start+30*time.Second)
	addPart(sid+"-p6", sid+"-u2", start+30*time.Second, text("now write the test"))
	addMsg(sid+"-a3", "assistant", start+31*time.Second)
	addPart(sid+"-p7", sid+"-a3", start+31*time.Second, tool("call-decoy", "leankg_query", decoy, "ok"))
}

func call(d time.Duration, cwdPath string, args map[string]any) sessionlink.CallRef {
	return sessionlink.CallRef{CallID: "c", TS: t0.Add(d), Cwd: cwdPath, Tool: "query", ArgsHash: argsHash(args)}
}

func TestDetectRootsAndXDGPath(t *testing.T) {
	home, dbFile := fixtureHome(t)
	a := New()
	if a.Client() != "opencode" {
		t.Fatalf("Client = %q", a.Client())
	}
	if a.Detect(home) {
		t.Fatal("Detect true before the store exists")
	}
	buildStore(t, dbFile, "s-detect", cwd, 0)
	if !a.Detect(home) {
		t.Fatal("Detect false with the store present")
	}
	if got := a.Roots(home); len(got) != 1 || got[0] != dbFile {
		t.Fatalf("Roots = %v", got)
	}

	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if got := dbPath(home); got != filepath.Join(xdg, "opencode", "opencode.db") {
		t.Fatalf("XDG path = %s", got)
	}
	t.Setenv("XDG_DATA_HOME", "relative/not/absolute")
	if got := dbPath(home); got != dbFile {
		t.Fatalf("relative XDG must be ignored, got %s", got)
	}
}

func TestHeuristicLocateAndWindow(t *testing.T) {
	home, dbFile := fixtureHome(t)
	buildStore(t, dbFile, "ses-1", cwd, 0)
	args := map[string]any{"query": "auth handler", "action": "search"}
	c := call(5*time.Second, cwd, args)
	a := New()

	ref, conf, err := a.Locate(home, c)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if ref.Path != dbFile || ref.SessionID != "ses-1" {
		t.Fatalf("ref = %+v", ref)
	}
	if conf != sessionlink.ConfidenceUnique {
		t.Fatalf("conf = %v, want 0.9", conf)
	}

	w, err := a.Window(ref, c, 6)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if w.Prompt != "find the auth handler" {
		t.Fatalf("Prompt = %q", w.Prompt)
	}
	if w.Matched == nil || w.Matched.ID != "call-lk" || w.Matched.Norm != "leankg.query" || !w.Matched.IsLeanKG {
		t.Fatalf("Matched = %+v", w.Matched)
	}
	if w.Matched.Result != "ok: AuthHandler" || w.Matched.IsError {
		t.Fatalf("matched result = %q err=%v", w.Matched.Result, w.Matched.IsError)
	}
	if len(w.Before) != 2 || w.Before[0].Role != "user" || w.Before[1].ToolCalls[0].Norm != "grep" {
		t.Fatalf("Before = %+v", w.Before)
	}
	if w.Before[1].InputTokens != 9 || w.Before[1].Model != "synthetic-model" {
		t.Fatalf("usage not carried: %+v", w.Before[1])
	}
	// Follow-ups: the same-turn read, and nothing past the second prompt.
	if len(w.FollowUps) != 1 || w.FollowUps[0].Norm != "read" || w.FollowUps[0].Target != "/work/oc-repo/auth.go" {
		t.Fatalf("FollowUps = %+v", w.FollowUps)
	}
}

func TestExactByClientSessionID(t *testing.T) {
	home, dbFile := fixtureHome(t)
	buildStore(t, dbFile, "ses-exact", cwd, 0)
	args := map[string]any{"query": "auth handler", "action": "search"}
	c := call(5*time.Second, cwd, args)
	c.ClientSessionID = "ses-exact"
	ref, conf, err := New().Locate(home, c)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if conf != sessionlink.ConfidenceExact || ref.SessionID != "ses-exact" {
		t.Fatalf("exact = %+v conf %v", ref, conf)
	}
}

func TestNoFalseLinks(t *testing.T) {
	home, dbFile := fixtureHome(t)
	buildStore(t, dbFile, "ses-nf", cwd, 0)
	args := map[string]any{"query": "auth handler", "action": "search"}
	a := New()

	cases := map[string]sessionlink.CallRef{
		"wrong directory":   call(5*time.Second, "/other/repo", args),
		"session not yet":   call(-time.Hour, cwd, args),
		"args not captured": call(5*time.Second, cwd, map[string]any{"query": "never asked"}),
		"far in time":       call(3*time.Hour, cwd, args),
	}
	for name, c := range cases {
		if _, _, err := a.Locate(home, c); !errors.Is(err, sessionlink.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestMissingStoreIsNotFound(t *testing.T) {
	home, _ := fixtureHome(t)
	if _, _, err := New().Locate(home, call(0, cwd, nil)); !errors.Is(err, sessionlink.ErrNotFound) {
		t.Fatalf("missing store: err = %v, want ErrNotFound", err)
	}
}

func TestUnknownSchemaIsUnsupportedVersion(t *testing.T) {
	home, dbFile := fixtureHome(t)
	db, err := sql.Open("sqlite", "file:"+dbFile)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT, time_created INTEGER, time_updated INTEGER)`)
	mustExec(t, db, `CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`)
	mustExec(t, db, `CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`)
	db.Close()
	args := map[string]any{"query": "auth handler", "action": "search"}
	if _, _, err := New().Locate(home, call(5*time.Second, cwd, args)); !errors.Is(err, sessionlink.ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestStoreIsNeverWritten(t *testing.T) {
	home, dbFile := fixtureHome(t)
	buildStore(t, dbFile, "ses-ro", cwd, 0)
	before := fileSum(t, dbFile)

	args := map[string]any{"query": "auth handler", "action": "search"}
	c := call(5*time.Second, cwd, args)
	a := New()
	ref, _, err := a.Locate(home, c)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if _, err := a.Window(ref, c, 6); err != nil {
		t.Fatalf("Window: %v", err)
	}
	if _, err := a.Transcript(ref, 10); err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if after := fileSum(t, dbFile); after != before {
		t.Fatal("the adapter changed opencode.db")
	}

	// The adapter's handle must refuse writes at the driver level too.
	ro, err := openRO(dbFile)
	if err != nil {
		t.Fatalf("openRO: %v", err)
	}
	defer ro.Close()
	if _, err := ro.Exec(`INSERT INTO session(id, directory, time_created, time_updated) VALUES ('x','y',0,0)`); err == nil {
		t.Fatal("write through the read-only handle succeeded")
	}
}

func fileSum(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
