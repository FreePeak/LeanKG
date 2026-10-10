package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeRec is a Recorder that keeps every event in memory.
type fakeRec struct {
	level telemetry.Level
	mu    sync.Mutex
	calls []telemetry.CallEvent
}

func (f *fakeRec) Level() telemetry.Level { return f.level }
func (f *fakeRec) RecordCall(ev telemetry.CallEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ev)
}
func (f *fakeRec) RecordMemory(telemetry.MemoryEvent) {}
func (f *fakeRec) Close() error                       { return nil }

func (f *fakeRec) snapshot() []telemetry.CallEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]telemetry.CallEvent(nil), f.calls...)
}

func (f *fakeRec) byMethod(method string) []telemetry.CallEvent {
	var out []telemetry.CallEvent
	for _, c := range f.snapshot() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// newCaptureEngine builds one engine over a temp project; every server in a
// test shares it so responses are comparable byte for byte.
func newCaptureEngine(t *testing.T) (*core.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, mem, nil)
	engine.SetProjectDir(dir)
	return engine, dir
}

// connectWith serves engine through the SDK in-memory transport with rec
// attached, and returns the connected client session.
func connectWith(t *testing.T, engine *core.Engine, rec telemetry.Recorder, clientName, clientVersion string) *mcp.ClientSession {
	t.Helper()
	srv := New(engine)
	srv.SetRecorder(rec)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.srv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// directChain runs one tools/call through the same middleware order New
// installs (capture, then resolveToolNames, then the handlers), with ctx
// supplied by the caller, so role and identity can be set per call.
func directChain(srv *Server) mcp.MethodHandler {
	dispatch := func(ctx context.Context, _ string, r mcp.Request) (mcp.Result, error) {
		c := r.(*mcp.CallToolRequest)
		var (
			res *mcp.CallToolResult
			err error
		)
		switch c.Params.Name {
		case core.ToolImport:
			res, err = srv.handleImport(ctx, c)
		case core.ToolQuery:
			res, err = srv.handleQuery(ctx, c)
		case core.ToolStatus:
			res, err = srv.handleStatus(ctx, c)
		}
		if err != nil {
			return nil, err
		}
		return res, nil
	}
	return srv.capture(resolveToolNames(dispatch))
}

func directCall(t *testing.T, srv *Server, ctx context.Context, name, args string) error {
	t.Helper()
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(args)}}
	_, err := directChain(srv)(ctx, "tools/call", req)
	return err
}

func TestCaptureGoldenParity(t *testing.T) {
	engine, _ := newCaptureEngine(t)
	calls := []struct {
		name string
		args any
	}{
		{"status", nil},
		{"query", map[string]any{"query": "Foo", "action": "exact"}},
		{"query", map[string]any{"query": "how does authentication work"}},
		{"query", map[string]any{"query": "x", "action": "bogus"}},
		{"nosuchtool", nil},
	}
	run := func(rec telemetry.Recorder) []string {
		session := connectWith(t, engine, rec, "parity", "1")
		var out []string
		for _, c := range calls {
			res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: c.name, Arguments: c.args})
			if err != nil {
				out = append(out, "err:"+err.Error())
				continue
			}
			b, merr := json.Marshal(res)
			if merr != nil {
				t.Fatal(merr)
			}
			out = append(out, string(b))
		}
		return out
	}
	// status reports the SQLite WAL size, which moves with every write, not
	// with capture; it is normalized so the comparison covers the rest.
	walRE := regexp.MustCompile(`wal_bytes\\":[0-9]+`)
	norm := func(in []string) []string {
		out := make([]string, len(in))
		for i, v := range in {
			out[i] = walRE.ReplaceAllString(v, `wal_bytes\\":0`)
		}
		return out
	}
	off := norm(run(telemetry.Nop{}))
	meta := norm(run(&fakeRec{level: telemetry.Metadata}))
	bodies := norm(run(&fakeRec{level: telemetry.Bodies}))
	for i := range off {
		if off[i] != meta[i] || off[i] != bodies[i] {
			t.Errorf("call %d (%s) response differs with capture on\n off:    %s\n meta:   %s\n bodies: %s",
				i, calls[i].name, off[i], meta[i], bodies[i])
		}
	}
}

// Exactly one tools/call row per call, for each class of call (DS-04 tests).
func TestCaptureOneRowPerCall(t *testing.T) {
	cases := []struct {
		name        string
		tool, args  string
		wantOutcome func(string) bool
		wantErrCode string
	}{
		{name: "success", tool: "status", args: "{}", wantOutcome: func(o string) bool { return o == telemetry.OutcomeCold || o == telemetry.OutcomeOK }},
		{name: "engine error", tool: "query", args: `{"query":"x","action":"bogus"}`, wantOutcome: func(o string) bool { return strings.HasPrefix(o, "error:LEANKG_ERROR_") }},
		{name: "unknown tool", tool: "nosuchtool", args: "{}", wantOutcome: func(o string) bool { return o == "error:LEANKG_ERROR_UNKNOWN_TOOL" }},
		{name: "bad args", tool: "query", args: `"not-an-object"`, wantOutcome: func(o string) bool { return strings.HasPrefix(o, "error:") }},
	}
	engine, _ := newCaptureEngine(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeRec{level: telemetry.Metadata}
			srv := New(engine)
			srv.SetRecorder(rec)
			_ = directCall(t, srv, context.Background(), tc.tool, tc.args)
			rows := rec.byMethod("tools/call")
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want exactly 1", len(rows))
			}
			if !tc.wantOutcome(rows[0].Outcome) {
				t.Errorf("Outcome = %q", rows[0].Outcome)
			}
			if rows[0].Tool != tc.tool {
				t.Errorf("Tool = %q, want %q", rows[0].Tool, tc.tool)
			}
		})
	}
}

func TestCaptureRecordsRBACRefusal(t *testing.T) {
	engine, _ := newCaptureEngine(t)
	rec := &fakeRec{level: telemetry.Metadata}
	srv := New(engine)
	srv.SetRecorder(rec)
	viewer := withRole(context.Background(), auth.Viewer)
	if err := directCall(t, srv, viewer, core.ToolImport, `{"action":"repo","path":"."}`); err == nil {
		t.Fatal("viewer import must be refused")
	}
	rows := rec.byMethod("tools/call")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Outcome != telemetry.OutcomeRefused || rows[0].ErrorCode != "LEANKG_ERROR_PERMISSION_DENIED" {
		t.Errorf("refusal row = %q / %q", rows[0].Outcome, rows[0].ErrorCode)
	}
}

func TestCaptureRecordsInitializeIdentity(t *testing.T) {
	engine, _ := newCaptureEngine(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-claude-1")
	rec := &fakeRec{level: telemetry.Metadata}
	session := connectWith(t, engine, rec, "claude-code", "2.1.0")
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "status"}); err != nil {
		t.Fatal(err)
	}
	// The SDK client opens with server/discover (2026-07-28 protocol), so the
	// handshake row carries that method; identity is read from the session.
	var handshake []telemetry.CallEvent
	for _, ev := range rec.snapshot() {
		if ev.Method != "tools/call" {
			handshake = append(handshake, ev)
		}
	}
	calls := rec.byMethod("tools/call")
	if len(handshake) != 1 || len(calls) != 1 {
		t.Fatalf("handshake rows = %d, tools/call rows = %d; want 1 and 1", len(handshake), len(calls))
	}
	for _, ev := range []telemetry.CallEvent{handshake[0], calls[0]} {
		if ev.ClientName != "claude-code" || ev.ClientVersion != "2.1.0" {
			t.Errorf("%s identity = %q %q", ev.Method, ev.ClientName, ev.ClientVersion)
		}
		if ev.ClientSessionID != "sess-claude-1" || ev.Correlation != telemetry.CorrExact {
			t.Errorf("%s session = %q correlation %q", ev.Method, ev.ClientSessionID, ev.Correlation)
		}
		if ev.Transport != telemetry.TransportStdio || ev.Cwd == "" {
			t.Errorf("%s transport %q cwd %q", ev.Method, ev.Transport, ev.Cwd)
		}
	}
}

// The initialize params carry the client; the session id comes only from the
// env var of that same client, so a Claude Code id never labels an omp run.
func TestInitializeIdentityNoCrossClientSession(t *testing.T) {
	engine, _ := newCaptureEngine(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-claude-only")
	rec := &fakeRec{level: telemetry.Metadata}
	srv := New(engine)
	srv.SetRecorder(rec)
	req := &mcp.ServerRequest[*mcp.InitializeParams]{Params: &mcp.InitializeParams{
		ClientInfo: &mcp.Implementation{Name: "omp", Version: "3.1"},
	}}
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) { return &mcp.InitializeResult{}, nil }
	if _, err := srv.capture(next)(context.Background(), "initialize", req); err != nil {
		t.Fatal(err)
	}
	rows := rec.byMethod("initialize")
	if len(rows) != 1 {
		t.Fatalf("initialize rows = %d, want 1", len(rows))
	}
	if rows[0].ClientName != "omp" || rows[0].ClientVersion != "3.1" || rows[0].ClientSessionID != "" {
		t.Errorf("identity = %q %q session=%q; want omp 3.1 with no session", rows[0].ClientName, rows[0].ClientVersion, rows[0].ClientSessionID)
	}
	if rows[0].Correlation != telemetry.CorrHeuristic {
		t.Errorf("correlation = %q, want heuristic", rows[0].Correlation)
	}
}

func TestIdentityFromHeaders(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    telemetry.Identity
	}{
		{
			name:    "explicit headers win",
			headers: map[string]string{"X-LeanKG-Client": "xdev/1.4.0", "X-LeanKG-Session": "s-9", "X-LeanKG-Cwd": "/w/repo", "User-Agent": "opencode/0.1"},
			want:    telemetry.Identity{ClientName: "xdev", ClientVersion: "1.4.0", ClientSessionID: "s-9", Cwd: "/w/repo"},
		},
		{
			name:    "user agent maps to a client name",
			headers: map[string]string{"User-Agent": "claude-cli/2.0.1 (external, cli)"},
			want:    telemetry.Identity{ClientName: "claude-code", ClientVersion: "2.0.1"},
		},
		{
			name:    "opencode user agent",
			headers: map[string]string{"User-Agent": "opencode/0.9 (linux)"},
			want:    telemetry.Identity{ClientName: "opencode", ClientVersion: "0.9"},
		},
		{
			name:    "no identity is unknown, never an error",
			headers: map[string]string{},
			want:    telemetry.Identity{ClientName: "unknown"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptestRequest(t, tc.headers)
			got := identityFromHeaders(req)
			if got != tc.want {
				t.Fatalf("identity = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNormalizeClientName(t *testing.T) {
	cases := map[string]string{
		"Claude Code": "claude-code",
		"claude-code": "claude-code",
		"claude":      "claude-code",
		"pi":          "pi",
		"pi-agent":    "pi",
		"pix":         "pix",
		"OMP":         "omp",
		"codex-cli":   "codex",
		"Grok":        "grok",
		"gemini-cli":  "gemini",
		"xdev":        "xdev",
		"opencode":    "opencode",
		"Some Tool":   "some tool",
		"":            "unknown",
	}
	for in, want := range cases {
		if got := normalizeClient(in); got != want {
			t.Errorf("normalizeClient(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCaptureUsesHTTPIdentityFromContext(t *testing.T) {
	engine, _ := newCaptureEngine(t)
	rec := &fakeRec{level: telemetry.Metadata}
	srv := New(engine)
	srv.SetRecorder(rec)
	id := telemetry.Identity{ClientName: "codex", ClientVersion: "0.3", Cwd: "/w"}
	ctx := telemetry.WithIdentity(context.Background(), id, telemetry.TransportHTTP)
	if err := directCall(t, srv, ctx, core.ToolStatus, "{}"); err != nil {
		t.Fatal(err)
	}
	rows := rec.byMethod("tools/call")
	if len(rows) != 1 || rows[0].ClientName != "codex" || rows[0].Transport != telemetry.TransportHTTP {
		t.Fatalf("rows = %+v", rows)
	}
}

// Off means off: the middleware hands the same ctx and result through and
// records nothing, and it allocates nothing beyond the level check.
func TestCaptureOffIsPassThrough(t *testing.T) {
	srv := New(nil)
	srv.SetRecorder(telemetry.Nop{})
	ctx := context.WithValue(context.Background(), ctxKey(1), "marker")
	want := &mcp.CallToolResult{}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "status"}}
	var seenCtx context.Context
	next := func(c context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		seenCtx = c
		return want, nil
	}
	h := srv.capture(next)
	got, err := h(ctx, "tools/call", req)
	if err != nil || got != want || seenCtx != ctx {
		t.Fatalf("pass-through changed ctx or result: err=%v", err)
	}
	allocs := testing.AllocsPerRun(200, func() { _, _ = h(ctx, "tools/call", req) })
	if allocs != 0 {
		t.Errorf("Off allocates %.1f per call, want 0", allocs)
	}
}

func TestCaptureBodiesOnlyAtBodiesLevel(t *testing.T) {
	engine, _ := newCaptureEngine(t)
	meta := &fakeRec{level: telemetry.Metadata}
	bodies := &fakeRec{level: telemetry.Bodies}
	args := `{"query":"Foo","action":"exact"}`
	for _, rec := range []*fakeRec{meta, bodies} {
		srv := New(engine)
		srv.SetRecorder(rec)
		_ = directCall(t, srv, context.Background(), core.ToolQuery, args)
	}
	m := meta.byMethod("tools/call")[0]
	b := bodies.byMethod("tools/call")[0]
	if m.ArgsRedacted != "" || m.BodyRedacted != "" {
		t.Errorf("metadata level recorded bodies: %q / %q", m.ArgsRedacted, m.BodyRedacted)
	}
	if b.ArgsRedacted != args || b.BodyRedacted == "" {
		t.Errorf("bodies level args/body = %q / %q", b.ArgsRedacted, b.BodyRedacted)
	}
	if m.Action != "exact" || m.Command != "" || m.ArgsHash == "" || m.ArgKeys != "action,query" {
		t.Errorf("metadata fields action=%q command=%q hash=%q keys=%q", m.Action, m.Command, m.ArgsHash, m.ArgKeys)
	}
}

// The handler reports the pre-budget size, the budget trim and the delivered
// size, and the baseline counts only files that exist inside the project.
func TestFinishCallTokensAndBaseline(t *testing.T) {
	engine, dir := newCaptureEngine(t)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(strings.Repeat("x", 4000)), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &fakeRec{level: telemetry.Metadata}
	srv := New(engine)
	srv.SetRecorder(rec)
	out := map[string]any{
		"retrieval": map[string]any{"rung": "L1", "reason": "exact identifier match"},
		"freshness": "fresh",
		"hits":      []map[string]any{{"file_path": "a.go"}, {"file_path": "../escape.go"}},
	}
	post := map[string]any{"hits": []any{}, "_note": "trimmed"}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: core.ToolQuery, Arguments: json.RawMessage(`{"query":"x"}`)}}
	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		srv.finishCall(ctx, engine, req, time.Now(), "search", out, post,
			budget.Stats{PreTruncationToken: 500, Actual: 100}, nil)
		return &mcp.CallToolResult{}, nil
	}
	if _, err := srv.capture(next)(context.Background(), "tools/call", req); err != nil {
		t.Fatal(err)
	}
	row := rec.byMethod("tools/call")[0]
	postTokens := int64(len(mustJSON(t, post)) / 4)
	if row.OutTokensPre != 500 || row.BudgetTrimmedTokens != 400 || row.OutTokensPost != postTokens {
		t.Errorf("tokens pre/trim/post = %d/%d/%d, want 500/400/%d", row.OutTokensPre, row.BudgetTrimmedTokens, row.OutTokensPost, postTokens)
	}
	if row.BaselineMethod != telemetry.BaselineFileRead || row.BaselineTokens != 1000 {
		t.Errorf("baseline = %d (%s), want 1000 (file_read): escape.go must not count", row.BaselineTokens, row.BaselineMethod)
	}
	if want := telemetry.Saved(1000, postTokens); row.TokensSaved != want {
		t.Errorf("TokensSaved = %d, want %d", row.TokensSaved, want)
	}
	if row.Rung != "L1" || row.Hits != 2 || row.HitFiles != "a.go\n../escape.go" {
		t.Errorf("signals rung=%q hits=%d files=%q", row.Rung, row.Hits, row.HitFiles)
	}
}

// Off is the default: a server with no SetRecorder call holds the Nop
// recorder, so an unknown tool errors exactly as it did before capture.
func TestDefaultRecorderIsOff(t *testing.T) {
	engine, _ := newCaptureEngine(t)
	srv := New(engine)
	if srv.recorder().Level() != telemetry.Off {
		t.Fatalf("default level = %q, want off", srv.recorder().Level())
	}
	if err := directCall(t, srv, context.Background(), "nosuchtool", "{}"); err == nil {
		t.Fatal("unknown tool must error")
	}
}

func httptestRequest(t *testing.T, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestCaptureDoesNotChangeResponseBytesProperty(t *testing.T) {
	engine, _ := newCaptureEngine(t)
	f := func(q string) bool {
		args, _ := json.Marshal(map[string]any{"query": q, "action": "exact"})
		off := New(engine)
		on := New(engine)
		on.SetRecorder(&fakeRec{level: telemetry.Bodies})
		a := callRaw(t, off, args)
		b := callRaw(t, on, args)
		return bytes.Equal(a, b)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 25}); err != nil {
		t.Fatal(err)
	}
}

func callRaw(t *testing.T, srv *Server, args []byte) []byte {
	t.Helper()
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: core.ToolQuery, Arguments: args}}
	res, err := directChain(srv)(context.Background(), "tools/call", req)
	if err != nil {
		return []byte("err:" + err.Error())
	}
	b, _ := json.Marshal(res)
	return b
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// BenchmarkCaptureOff and BenchmarkCaptureMetadata time the same real status
// call through the chain; the difference is the capture overhead (DS-04 gate).
func BenchmarkCaptureOff(b *testing.B) { benchCapture(b, telemetry.Nop{}) }
func BenchmarkCaptureMetadata(b *testing.B) {
	benchCapture(b, &fakeRec{level: telemetry.Metadata})
}

func benchCapture(b *testing.B, rec telemetry.Recorder) {
	st, err := store.Open(filepath.Join(b.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		b.Fatal(err)
	}
	mem, err := memory.Open(b.TempDir(), false)
	if err != nil {
		b.Fatal(err)
	}
	engine := core.New(st, mem, nil)
	engine.SetProjectDir(b.TempDir())
	srv := New(engine)
	srv.SetRecorder(rec)
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: core.ToolStatus, Arguments: json.RawMessage("{}")}}
	h := directChain(srv)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h(ctx, "tools/call", req); err != nil {
			b.Fatal(err)
		}
	}
}
