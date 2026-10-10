package telemetry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)

func openTestLedger(t *testing.T) *SQLiteStore {
	t.Helper()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "telemetry.db"), false)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSQLiteCallRoundTrip(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	in := CallEvent{
		ID: "c1", TS: t0, LatencyMS: 12, Transport: TransportStdio, Method: "tools/call",
		Tool: "query", Action: "search", Command: "find", Project: "/w/a",
		Identity: Identity{ClientName: "claude-code", ClientVersion: "1.2", ClientSessionID: "s-1", Cwd: "/w/a"},
		ArgsHash: "h", ArgKeys: "action,query", ArgsRedacted: `{"query":"x"}`,
		Outcome: OutcomeOK, OutcomeReason: "r", ErrorCode: "", Rung: "L1", Confidence: "high",
		Freshness: "fresh", Hits: 3, HitFiles: "a.go\nb.go",
		OutTokensPre: 100, OutTokensPost: 60, BudgetTrimmedTokens: 40, BaselineTokens: 500,
		BaselineMethod: BaselineFileRead, TokensSaved: 440, BodyRedacted: "body",
	}
	if err := st.InsertCalls(ctx, []CallEvent{in}); err != nil {
		t.Fatalf("InsertCalls: %v", err)
	}
	got, ok, err := st.Call(ctx, "c1")
	if err != nil || !ok {
		t.Fatalf("Call: ok=%v err=%v", ok, err)
	}
	if !got.TS.Equal(t0) || got.Hits != 3 || got.HitFiles != in.HitFiles || got.TokensSaved != 440 ||
		got.BaselineMethod != BaselineFileRead || got.BodyRedacted != "body" || got.ClientSessionID != "s-1" ||
		got.SessionID != "claude-code:s-1" || got.Correlation != CorrExact {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if _, ok, _ := st.Call(ctx, "missing"); ok {
		t.Fatal("Call(missing) reported found")
	}
}

func TestSQLiteCallFilters(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	mk := func(id, client, project, tool, outcome string, at time.Time) CallEvent {
		return CallEvent{ID: id, TS: at, Tool: tool, Project: project, Outcome: outcome,
			Transport: TransportStdio, Identity: Identity{ClientName: client, Cwd: "/w/" + project}}
	}
	calls := []CallEvent{
		mk("a", "pi", "p1", "query", OutcomeOK, t0),
		mk("b", "pi", "p1", "status", "error:ERR_X", t0.Add(time.Minute)),
		mk("c", "omp", "p2", "query", "error:ERR_Y", t0.Add(2*time.Minute)),
		mk("d", "omp", "p2", "query", OutcomeZeroHit, t0.Add(3*time.Minute)),
	}
	if err := st.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	ids := func(cs []CallEvent) string {
		s := ""
		for _, c := range cs {
			s += c.ID
		}
		return s
	}
	cases := []struct {
		name string
		f    CallFilter
		want string
	}{
		{"no filter newest first", CallFilter{}, "dcba"},
		{"client", CallFilter{Client: "pi"}, "ba"},
		{"project", CallFilter{Project: "p2"}, "dc"},
		{"tool", CallFilter{Tool: "status"}, "b"},
		{"exact outcome", CallFilter{Outcome: OutcomeOK}, "a"},
		{"error matches any error:*", CallFilter{Outcome: "error"}, "cb"},
		{"since", CallFilter{Since: t0.Add(time.Minute)}, "dcb"},
		{"until is exclusive", CallFilter{Until: t0.Add(2 * time.Minute)}, "ba"},
		{"limit", CallFilter{Limit: 2}, "dc"},
		{"limit and offset", CallFilter{Limit: 2, Offset: 1}, "cb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := st.Calls(ctx, c.f)
			if err != nil {
				t.Fatal(err)
			}
			if s := ids(got); s != c.want {
				t.Fatalf("ids = %q, want %q", s, c.want)
			}
		})
	}
}

func TestSQLiteExactSessionAssignment(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	calls := []CallEvent{
		{ID: "e1", TS: t0, Identity: Identity{ClientName: "claude-code", ClientSessionID: "abc", Cwd: "/w/a"}},
		{ID: "e2", TS: t0.Add(time.Hour), Identity: Identity{ClientName: "claude-code", ClientSessionID: "abc", Cwd: "/w/a"}},
	}
	if err := st.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e2"} {
		c, _, _ := st.Call(ctx, id)
		if c.SessionID != "claude-code:abc" || c.Correlation != CorrExact {
			t.Fatalf("%s: session=%q corr=%q", id, c.SessionID, c.Correlation)
		}
	}
	s, ok, err := st.Session(ctx, "claude-code:abc")
	if err != nil || !ok {
		t.Fatalf("Session: ok=%v err=%v", ok, err)
	}
	if !s.FirstTS.Equal(t0) || !s.LastTS.Equal(t0.Add(time.Hour)) || s.Correlation != CorrExact || s.Cwd != "/w/a" {
		t.Fatalf("session = %+v", s)
	}
}

func TestSQLiteHeuristicSessionExtendAndGapSplit(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	id := func(ts time.Time, cwd string, call string) CallEvent {
		return CallEvent{ID: call, TS: ts, Identity: Identity{ClientName: "pi", Cwd: cwd}}
	}
	// Call 1 opens a group. Call 2 is 10 min later in the same cwd: extend.
	// Call 3 is 31 min after call 2: a new group. Call 4 is in another cwd
	// at the same time as call 3: its own group.
	calls := []CallEvent{
		id(t0, "/w/a", "h1"),
		id(t0.Add(10*time.Minute), "/w/a", "h2"),
		id(t0.Add(41*time.Minute+time.Second), "/w/a", "h3"),
		id(t0.Add(41*time.Minute+time.Second), "/w/b", "h4"),
	}
	if err := st.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	get := func(c string) CallEvent {
		got, ok, err := st.Call(ctx, c)
		if err != nil || !ok {
			t.Fatalf("Call(%s): ok=%v err=%v", c, ok, err)
		}
		return got
	}
	s1, s2, s3, s4 := get("h1").SessionID, get("h2").SessionID, get("h3").SessionID, get("h4").SessionID
	if s1 != "pi:h:h1" || s2 != s1 {
		t.Fatalf("extend failed: h1=%q h2=%q", s1, s2)
	}
	if s3 == s1 || s3 != "pi:h:h3" {
		t.Fatalf("gap > 30 min must split: h3=%q", s3)
	}
	if s4 == s3 || s4 != "pi:h:h4" {
		t.Fatalf("other cwd must split: h4=%q", s4)
	}
	if get("h2").Correlation != CorrHeuristic {
		t.Fatalf("heuristic call correlation = %q", get("h2").Correlation)
	}
	sess, _, _ := st.Session(ctx, s1)
	if !sess.LastTS.Equal(t0.Add(10*time.Minute)) || sess.Correlation != CorrHeuristic {
		t.Fatalf("session after extend = %+v", sess)
	}
}

func TestSQLiteMemoryEventSessionAssignment(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	ev := MemoryEvent{ID: "m1", TS: t0, Verb: "recall", Identity: Identity{ClientName: "claude-code", ClientSessionID: "xyz"}}
	if err := st.InsertMemoryEvents(ctx, []MemoryEvent{ev}); err != nil {
		t.Fatal(err)
	}
	got, err := st.MemoryEvents(ctx, CallFilter{Client: "claude-code"})
	if err != nil || len(got) != 1 {
		t.Fatalf("MemoryEvents: n=%d err=%v", len(got), err)
	}
	if got[0].SessionID != "claude-code:xyz" || got[0].Verb != "recall" {
		t.Fatalf("memory event = %+v", got[0])
	}
	byVerb, _ := st.MemoryEvents(ctx, CallFilter{Tool: "retain"})
	if len(byVerb) != 0 {
		t.Fatalf("verb filter returned %d rows", len(byVerb))
	}
}

func TestSQLiteSessionsFilterAndUpsert(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	ss := []Session{
		{ID: "pi:h:x", ClientName: "pi", Project: "p", Cwd: "/w", FirstTS: t0, LastTS: t0, Correlation: CorrHeuristic},
		{ID: "omp:h:y", ClientName: "omp", Cwd: "/v", FirstTS: t0.Add(time.Hour), LastTS: t0.Add(time.Hour), Correlation: CorrHeuristic},
	}
	if err := st.UpsertSessions(ctx, ss); err != nil {
		t.Fatal(err)
	}
	// Re-upserting widens the time range instead of replacing the row.
	if err := st.UpsertSessions(ctx, []Session{{ID: "pi:h:x", ClientName: "pi", FirstTS: t0.Add(-time.Hour), LastTS: t0.Add(2 * time.Hour), Correlation: CorrHeuristic}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Sessions(ctx, CallFilter{Client: "pi"})
	if err != nil || len(got) != 1 {
		t.Fatalf("Sessions(pi): n=%d err=%v", len(got), err)
	}
	if !got[0].FirstTS.Equal(t0.Add(-time.Hour)) || !got[0].LastTS.Equal(t0.Add(2*time.Hour)) || got[0].Project != "p" {
		t.Fatalf("upsert = %+v", got[0])
	}
	all, _ := st.Sessions(ctx, CallFilter{})
	if len(all) != 2 {
		t.Fatalf("all sessions = %d, want 2", len(all))
	}
	one, _ := st.Sessions(ctx, CallFilter{SessionID: "omp:h:y"})
	if len(one) != 1 {
		t.Fatalf("SessionID filter = %d rows", len(one))
	}
}

func TestSQLiteLinksAndUnlinkedCalls(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	calls := []CallEvent{
		{ID: "u1", TS: t0, Method: MethodToolsCall, Identity: Identity{ClientName: "pi", Cwd: "/w"}},
		{ID: "u2", TS: t0.Add(time.Minute), Method: MethodToolsCall, Identity: Identity{ClientName: "pi", Cwd: "/w"}},
		{ID: "u3", TS: t0.Add(time.Hour), Method: MethodToolsCall, Identity: Identity{ClientName: "pi", Cwd: "/w"}},
		// Never linkable (no transcript tool_use): handshakes and REST rows.
		{ID: "h1", TS: t0, Method: MethodInitialize, Identity: Identity{ClientName: "pi", Cwd: "/w"}},
		{ID: "r1", TS: t0, Method: "POST /v1/default/banks/{bank}/memories/recall", Transport: TransportHS, Identity: Identity{ClientName: "pi", Cwd: "/w"}},
	}
	if err := st.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertLinks(ctx, []SessionLink{{CallID: "u2", SessionID: "pi:h:u1", Confidence: 0.9, ToolUseID: "tu", Window: "{}", LinkedAt: t0}}); err != nil {
		t.Fatal(err)
	}
	links, err := st.Links(ctx, "pi:h:u1")
	if err != nil || len(links) != 1 || links[0].CallID != "u2" || links[0].Confidence != 0.9 {
		t.Fatalf("Links = %+v err=%v", links, err)
	}
	unlinked, err := st.UnlinkedCalls(ctx, t0.Add(30*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unlinked) != 1 || unlinked[0].ID != "u1" {
		t.Fatalf("UnlinkedCalls = %+v, want only u1 (u2 linked, u3 newer, h1/r1 not MCP tool calls)", unlinked)
	}
}

func TestSQLiteABRunsRoundTrip(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	r := ABRun{ID: "ab1", Source: "ab_harness", Task: "t", Arm: "with", Repo: "r", Tokens: 9, Turns: 3,
		DurationS: 1.5, ToolCalls: 4, FileReads: 2, CostUSD: 0.01, JudgeScore: 4.5, Valid: true, ImportedAt: t0}
	if err := st.InsertABRuns(ctx, []ABRun{r}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ABRuns(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("ABRuns: n=%d err=%v", len(got), err)
	}
	if got[0].Tokens != 9 || !got[0].Valid || got[0].JudgeScore != 4.5 || got[0].Arm != "with" {
		t.Fatalf("ab run = %+v", got[0])
	}
}

func TestSQLitePurgeAndStats(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	old := t0.Add(-40 * 24 * time.Hour)
	calls := []CallEvent{
		{ID: "old", TS: old, Identity: Identity{ClientName: "pi", Cwd: "/old"}},
		{ID: "new", TS: t0, Identity: Identity{ClientName: "omp", Cwd: "/new"}},
	}
	if err := st.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMemoryEvents(ctx, []MemoryEvent{{ID: "mo", TS: old, Verb: "recall"}, {ID: "mn", TS: t0, Verb: "recall"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertLinks(ctx, []SessionLink{{CallID: "old", SessionID: "pi:h:old", LinkedAt: old}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDropped(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDropped(ctx, 4); err != nil {
		t.Fatal(err)
	}

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 2 || stats.MemoryEvents != 2 || stats.Links != 1 || stats.DroppedEvents != 7 || stats.SizeBytes <= 0 {
		t.Fatalf("stats before purge = %+v", stats)
	}
	if !stats.OldestTS.Equal(old) || !stats.NewestTS.Equal(t0) {
		t.Fatalf("stats range = %v .. %v", stats.OldestTS, stats.NewestTS)
	}

	n, err := st.Purge(ctx, t0.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 { // one call, one memory event, one link
		t.Fatalf("Purge removed %d, want 3", n)
	}
	if _, ok, _ := st.Call(ctx, "old"); ok {
		t.Fatal("old call survived purge")
	}
	if _, ok, _ := st.Session(ctx, "pi:h:old"); ok {
		t.Fatal("session with no calls survived purge")
	}
	if _, ok, _ := st.Session(ctx, "omp:h:new"); !ok {
		t.Fatal("session with calls was removed")
	}
}

func TestSQLiteReadOnlyHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	rw, err := OpenSQLite(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.InsertCalls(context.Background(), []CallEvent{{ID: "r1", TS: t0}}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenSQLite(path, true)
	if err != nil {
		t.Fatalf("read-only open: %v", err)
	}
	defer ro.Close()
	st, err := ro.Stats(context.Background())
	if err != nil || st.Calls != 1 {
		t.Fatalf("read-only Stats = %+v err=%v", st, err)
	}
	if err := ro.InsertCalls(context.Background(), []CallEvent{{ID: "r2", TS: t0}}); err == nil {
		t.Fatal("write through a read-only handle succeeded")
	}
}

func TestSQLiteReadOnlyMissingWrapsErrNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "telemetry.db")
	_, err := OpenSQLite(path, true)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("read-only open created the file")
	}
}

func TestSQLiteMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	for i := 0; i < 2; i++ {
		st, err := OpenSQLite(path, false)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Four handles on one file, each in its own goroutine, mimic four MCP
// processes writing the same ledger. Every row must land.
func TestSQLiteConcurrentWritersSeparateHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	const writers, batches, perBatch = 4, 10, 10
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		st, err := OpenSQLite(path, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		wg.Add(1)
		go func(w int, st *SQLiteStore) {
			defer wg.Done()
			for b := 0; b < batches; b++ {
				var calls []CallEvent
				for i := 0; i < perBatch; i++ {
					calls = append(calls, CallEvent{
						ID: fmtID(w, b, i), TS: t0.Add(time.Duration(b*perBatch+i) * time.Second),
						Identity: Identity{ClientName: "pi", Cwd: "/w"},
					})
				}
				if err := st.InsertCalls(context.Background(), calls); err != nil {
					errs <- err
					return
				}
			}
		}(w, st)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent insert: %v", err)
	}
	st, err := OpenSQLite(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stats, err := st.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(writers * batches * perBatch); stats.Calls != want {
		t.Fatalf("calls = %d, want %d", stats.Calls, want)
	}
}

func fmtID(w, b, i int) string {
	return string(rune('a'+w)) + "-" + string(rune('a'+b)) + "-" + string(rune('a'+i))
}

// The linker records where a session's transcript lives and whether it was
// linked; a later upsert of the same session must carry both (smoke-found:
// the conflict branch dropped them, so the replay never found the file).
func TestSQLiteUpsertSessionKeepsLinkFields(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	ts := time.Unix(1700000000, 0)
	if err := st.InsertCalls(ctx, []CallEvent{{ID: "c1", TS: ts, Tool: "query",
		Identity: Identity{ClientName: "claude-code", ClientSessionID: "s1"}}}); err != nil {
		t.Fatal(err)
	}
	sess, ok, err := st.Session(ctx, "claude-code:s1")
	if err != nil || !ok {
		t.Fatalf("session: ok=%v err=%v", ok, err)
	}
	sess.TranscriptPath, sess.LinkStatus = "~/.claude/projects/x/s1.jsonl", "linked"
	if err := st.UpsertSessions(ctx, []Session{sess}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := st.Session(ctx, "claude-code:s1")
	if got.TranscriptPath != sess.TranscriptPath || got.LinkStatus != "linked" {
		t.Fatalf("link fields lost: path=%q status=%q", got.TranscriptPath, got.LinkStatus)
	}
	// A later call insert re-upserts the session without link fields: they stay.
	if err := st.InsertCalls(ctx, []CallEvent{{ID: "c2", TS: ts.Add(time.Minute), Tool: "query",
		Identity: Identity{ClientName: "claude-code", ClientSessionID: "s1"}}}); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.Session(ctx, "claude-code:s1")
	if got.TranscriptPath != sess.TranscriptPath || got.LinkStatus != "linked" {
		t.Fatalf("link fields cleared by a call insert: path=%q status=%q", got.TranscriptPath, got.LinkStatus)
	}
}
