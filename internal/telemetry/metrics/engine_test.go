package metrics

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

func findKPI(kpis []report.KPI, key string) (report.KPI, bool) {
	for _, k := range kpis {
		if k.Key == key {
			return k, true
		}
	}
	return report.KPI{}, false
}

func TestSessionListSortsPagesAndFilters(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{
		mkSession("claude-code:a", 0, 10*time.Minute),
		mkSession("claude-code:b", 0, 30*time.Minute),
		mkSession("claude-code:c", 0, 20*time.Minute),
	}
	a := mkCall("1", "claude-code:a", 1*time.Minute, "query", "search", telemetry.OutcomeOK, 5)
	a.TokensSaved = 100
	a.OutTokensPost = 40
	b1 := mkCall("2", "claude-code:b", 2*time.Minute, "query", "search", telemetry.OutcomeOK, 5)
	b1.TokensSaved = 50
	b2 := mkCall("3", "claude-code:b", 3*time.Minute, "query", "search", "error:ERR_DB", 5)
	c := mkCall("4", "claude-code:c", 4*time.Minute, "query", "search", telemetry.OutcomeRefused, 5)
	st.calls = []telemetry.CallEvent{a, b1, b2, c}
	st.mem = []telemetry.MemoryEvent{
		{ID: "m1", Verb: "recall", SessionID: "claude-code:a", TS: t0},
		{ID: "m2", Verb: "retain", SessionID: "claude-code:a", TS: t0},
	}
	ctx := context.Background()

	got, err := SessionList(ctx, st, Options{}, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 || len(got.Sessions) != 3 {
		t.Fatalf("total=%d rows=%d, want 3/3", got.Total, len(got.Sessions))
	}
	wantOrder := []string{"claude-code:b", "claude-code:c", "claude-code:a"}
	for i, id := range wantOrder {
		if got.Sessions[i].ID != id {
			t.Fatalf("row %d = %s, want %s (LastTS desc)", i, got.Sessions[i].ID, id)
		}
	}
	bRow := got.Sessions[0]
	if bRow.Calls != 2 || bRow.Errors != 1 {
		t.Fatalf("b calls=%d errors=%d, want 2/1", bRow.Calls, bRow.Errors)
	}
	if math.Abs(bRow.SuccessRate-0.5) > 1e-9 {
		t.Fatalf("b success=%v, want 0.5", bRow.SuccessRate)
	}
	if bRow.TokensSaved != 50 {
		t.Fatalf("b tokens saved=%d, want 50", bRow.TokensSaved)
	}
	if bRow.DurationS != 1800 {
		t.Fatalf("b duration=%v, want 1800", bRow.DurationS)
	}
	if len(bRow.OutcomeMix) != 2 || bRow.OutcomeMix[0].Count != 1 {
		t.Fatalf("b outcome mix=%+v", bRow.OutcomeMix)
	}
	if got.Sessions[2].MemoryRecalls != 1 {
		t.Fatalf("a memory recalls=%d, want 1", got.Sessions[2].MemoryRecalls)
	}

	errs, err := SessionList(ctx, st, Options{}, "error", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if errs.Total != 2 {
		t.Fatalf("error-filtered total=%d, want 2 (b and c)", errs.Total)
	}

	page, err := SessionList(ctx, st, Options{}, "", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Sessions) != 1 || page.Sessions[0].ID != "claude-code:c" {
		t.Fatalf("page = %+v", page)
	}

	empty, err := SessionList(ctx, &fakeStore{}, Options{}, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Sessions == nil || empty.Total != 0 {
		t.Fatalf("empty list must be non-nil and total 0, got %+v", empty)
	}
	b, _ := json.Marshal(empty)
	if string(b) != `{"total":0,"sessions":[]}` {
		t.Fatalf("empty JSON = %s", b)
	}
}

func TestSessionDetailKPIsFailuresAndUnlinked(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{mkSession("claude-code:a", 0, time.Hour)}
	lat := []int64{10, 20, 30, 40, 50}
	outs := []string{telemetry.OutcomeOK, telemetry.OutcomeCold, telemetry.OutcomeStale, telemetry.OutcomeDegraded, "error:ERR_X"}
	for i := range lat {
		c := mkCall(string(rune('a'+i)), "claude-code:a", time.Duration(i+1)*time.Minute, "query", "search", outs[i], lat[i])
		c.OutTokensPost = 100
		c.TokensSaved = 30
		c.BaselineMethod = telemetry.BaselineFileRead
		c.OutcomeReason = "run import"
		st.calls = append(st.calls, c)
	}
	st.mem = []telemetry.MemoryEvent{{ID: "m1", Verb: "recall", SessionID: "claude-code:a", TS: t0.Add(time.Minute)}}

	d, ok, err := SessionDetail(context.Background(), st, "claude-code:a")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if d.Linked {
		t.Fatal("no link rows: session must not be Linked")
	}
	if p, _ := findKPI(d.KPIs, "p50_latency"); p.Value != 30 {
		t.Fatalf("p50=%v want 30", p.Value)
	}
	if p, _ := findKPI(d.KPIs, "p95_latency"); math.Abs(p.Value-48) > 1e-9 {
		t.Fatalf("p95=%v want 48 (linear interpolation)", p.Value)
	}
	if k, _ := findKPI(d.KPIs, "tokens_saved"); !k.Estimate || k.Value != 150 || k.Method == "" {
		t.Fatalf("tokens_saved=%+v want estimate 150 with method", k)
	}
	if k, _ := findKPI(d.KPIs, "tokens_delivered"); k.Value != 500 {
		t.Fatalf("tokens_delivered=%v want 500", k.Value)
	}
	if k, _ := findKPI(d.KPIs, "degraded_share"); k.Unit != "%" || math.Abs(k.Value-60) > 1e-9 {
		t.Fatalf("cold+stale+degraded share=%v want 60 (percent)", k.Value)
	}
	if len(d.Failures) != 4 {
		t.Fatalf("failures=%+v want 4 non-ok groups", d.Failures)
	}
	if len(d.Calls) != 5 || d.Calls[0].ID != "a" {
		t.Fatalf("calls must be time-ordered, got %d first=%s", len(d.Calls), d.Calls[0].ID)
	}
	for _, row := range d.Calls {
		if row.Signals != nil {
			t.Fatal("unlinked session must carry no signals")
		}
		if row.Args != "" || row.Body != "" {
			t.Fatal("args/body must stay empty below Bodies level")
		}
	}
	if len(d.Memory) != 1 || d.Memory == nil {
		t.Fatalf("memory rows=%+v", d.Memory)
	}

	if _, ok, _ := SessionDetail(context.Background(), st, "nope"); ok {
		t.Fatal("unknown session must report ok=false")
	}
}

func TestSessionDetailSignalsWhenLinked(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{mkSession("claude-code:a", 0, time.Hour)}
	call := mkCall("c1", "claude-code:a", time.Minute, "query", "search", telemetry.OutcomeOK, 5)
	call.HitFiles = "internal/a.go\ninternal/b.go"
	call.Hits = 2
	st.calls = []telemetry.CallEvent{call}
	win := sessionlink.Window{
		Client:    "claude-code",
		SessionID: "claude-code:a",
		Matched:   &sessionlink.ToolCall{ID: "tu1", Norm: "leankg.query", IsLeanKG: true},
		FollowUps: []sessionlink.ToolCall{{ID: "tu2", Norm: "read", Target: "internal/a.go"}},
	}
	raw, _ := json.Marshal(win)
	st.links = []telemetry.SessionLink{{CallID: "c1", SessionID: "claude-code:a", Confidence: 1, ToolUseID: "tu1", Window: string(raw), LinkedAt: t0}}

	d, _, err := SessionDetail(context.Background(), st, "claude-code:a")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Linked || len(d.Calls) != 1 || d.Calls[0].Signals == nil {
		t.Fatalf("linked session must expose signals: linked=%v calls=%+v", d.Linked, d.Calls)
	}
	if d.Calls[0].Signals.HitsUsed != 1 || d.Calls[0].Signals.HitsReturned != 2 {
		t.Fatalf("signals=%+v want used 1 of 2", d.Calls[0].Signals)
	}

	row, ok, err := CallDetail(context.Background(), st, "c1")
	if err != nil || !ok || row.Signals == nil {
		t.Fatalf("CallDetail linked: ok=%v err=%v signals=%v", ok, err, row.Signals)
	}
	if _, ok, _ := CallDetail(context.Background(), st, "missing"); ok {
		t.Fatal("missing call must report ok=false")
	}
}

func TestCallDetailBodiesLevelCarriesArgsAndBody(t *testing.T) {
	st := &fakeStore{}
	c := mkCall("c9", "claude-code:a", 0, "query", "search", telemetry.OutcomeZeroHit, 7)
	c.ArgsRedacted = `{"q":"x"}`
	c.BodyRedacted = `{"hits":[]}`
	c.OutcomeReason = "try action=fuzzy"
	st.calls = []telemetry.CallEvent{c}
	row, ok, err := CallDetail(context.Background(), st, "c9")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if row.Args != `{"q":"x"}` || row.Body != `{"hits":[]}` || row.OutcomeReason != "try action=fuzzy" {
		t.Fatalf("row = %+v", row)
	}
}

func TestOverviewSeriesKPIsAndCaptureLevel(t *testing.T) {
	st := &fakeStore{}
	day2 := 24 * time.Hour
	st.calls = []telemetry.CallEvent{
		mkCall("1", "claude-code:a", 0, "query", "search", telemetry.OutcomeOK, 5),
		mkCall("2", "claude-code:a", time.Minute, "query", "search", "error:ERR_X", 5),
		mkCall("3", "claude-code:b", day2, "status", "", telemetry.OutcomeOK, 5),
	}
	st.calls[0].TokensSaved = 10
	st.calls[2].TokensSaved = 5
	st.calls[2].ClientName = "opencode"
	cfg := telemetry.Config{Capture: telemetry.Metadata}

	ov, err := Overview(context.Background(), st, cfg, Options{Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := findKPI(ov.KPIs, "calls"); k.Value != 3 {
		t.Fatalf("calls kpi=%v want 3", k.Value)
	}
	if k, _ := findKPI(ov.KPIs, "tokens_saved"); !k.Estimate || k.Value != 15 {
		t.Fatalf("tokens_saved=%+v want estimate 15", k)
	}
	if len(ov.Series) != 2 {
		t.Fatalf("series=%+v want 2 local days", ov.Series)
	}
	if ov.Series[0].Calls != 2 || ov.Series[0].Errors != 1 || ov.Series[0].Sessions != 1 {
		t.Fatalf("day0=%+v", ov.Series[0])
	}
	if ov.Series[1].Calls != 1 || ov.Series[1].Sessions != 1 {
		t.Fatalf("day1=%+v", ov.Series[1])
	}
	if len(ov.TopFailures) != 1 || ov.TopFailures[0].Key != "error:ERR_X" {
		t.Fatalf("top failures=%+v", ov.TopFailures)
	}
	if len(ov.ByClient) != 2 || ov.ByClient[0].Key != "claude-code" {
		t.Fatalf("by client=%+v", ov.ByClient)
	}
	if len(ov.OutcomeMix) != 2 {
		t.Fatalf("outcome mix=%+v", ov.OutcomeMix)
	}
	if want := string(telemetry.EffectiveLevel(cfg, "")); ov.CaptureLevel != want {
		t.Fatalf("capture=%q want %q (EffectiveLevel)", ov.CaptureLevel, want)
	}
	if ov.SessionsOn {
		t.Fatal("no sessions grant: SessionsOn must be false")
	}

	cfg.Sessions = telemetry.SessionsConfig{Enabled: true, Clients: []string{"claude-code"}}
	ov, _ = Overview(context.Background(), st, cfg, Options{Now: fixedNow()})
	if !ov.SessionsOn {
		t.Fatal("granted sessions: SessionsOn must be true")
	}

	empty, err := Overview(context.Background(), &fakeStore{}, telemetry.Config{}, Options{Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Series == nil || empty.TopFailures == nil || empty.ByClient == nil || empty.OutcomeMix == nil || empty.KPIs == nil {
		t.Fatal("overview lists must be non-nil when empty")
	}
}

func TestOverviewFillsEmptyDaysSinceWindow(t *testing.T) {
	st := &fakeStore{}
	st.calls = []telemetry.CallEvent{mkCall("1", "claude-code:a", 48*time.Hour, "query", "search", telemetry.OutcomeOK, 5)}
	st.calls[0] = mkCall("1", "claude-code:a", 48*time.Hour, "query", "search", telemetry.OutcomeOK, 5)
	ov, err := Overview(context.Background(), st, telemetry.Config{}, Options{Since: t0, Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Series) != 3 {
		t.Fatalf("series len=%d want 3 (since day through the call day, zero-filled)", len(ov.Series))
	}
	if ov.Series[2].Calls != 1 || ov.Series[0].Calls != 0 || ov.Series[1].Calls != 0 {
		t.Fatalf("series=%+v", ov.Series)
	}
}

func TestFailuresGroupingSamplesAndGuidance(t *testing.T) {
	st := &fakeStore{}
	for i := 0; i < 7; i++ {
		c := mkCall(string(rune('a'+i)), "claude-code:a", time.Duration(i)*time.Minute, "query", "search", telemetry.OutcomeZeroHit, 5)
		c.OutcomeReason = "use action=fuzzy"
		st.calls = append(st.calls, c)
	}
	e := mkCall("z", "claude-code:a", 20*time.Minute, "import", "repo", "error:ERR_DB", 5)
	e.ClientName = "opencode"
	e.OutcomeReason = "re-run import"
	st.calls = append(st.calls, e)
	st.calls = append(st.calls, mkCall("ok", "claude-code:a", 0, "status", "", telemetry.OutcomeOK, 5))

	f, err := Failures(context.Background(), st, Options{}, "reason")
	if err != nil {
		t.Fatal(err)
	}
	if f.Group != "reason" || f.Total != 8 {
		t.Fatalf("group=%s total=%d want reason/8", f.Group, f.Total)
	}
	if len(f.Groups) != 2 || f.Groups[0].Key != telemetry.OutcomeZeroHit || f.Groups[0].Count != 7 {
		t.Fatalf("groups=%+v", f.Groups)
	}
	if math.Abs(f.Groups[0].Share-7.0/8.0) > 1e-9 {
		t.Fatalf("share=%v", f.Groups[0].Share)
	}
	if f.Groups[0].Guidance != "use action=fuzzy" {
		t.Fatalf("guidance=%q", f.Groups[0].Guidance)
	}
	if len(f.Groups[0].Samples) != 5 || f.Groups[0].Samples[0].ID != "g" {
		t.Fatalf("samples must be the 5 most recent, got %d first=%s", len(f.Groups[0].Samples), f.Groups[0].Samples[0].ID)
	}

	byClient, _ := Failures(context.Background(), st, Options{}, "client")
	if byClient.Group != "client" || byClient.Groups[0].Key != "claude-code" || byClient.Groups[0].Count != 7 {
		t.Fatalf("client groups=%+v", byClient.Groups)
	}
	byTool, _ := Failures(context.Background(), st, Options{}, "tool")
	if byTool.Group != "tool" || byTool.Groups[0].Key != "query.search" {
		t.Fatalf("tool groups=%+v", byTool.Groups)
	}
	odd, _ := Failures(context.Background(), st, Options{}, "bogus")
	if odd.Group != "reason" {
		t.Fatalf("unknown group must normalise to reason, got %q", odd.Group)
	}
	none, _ := Failures(context.Background(), &fakeStore{}, Options{}, "reason")
	if none.Groups == nil || none.Total != 0 {
		t.Fatal("empty failures must have non-nil groups")
	}
}

func TestToolsPercentilesRungAndOutcomeMix(t *testing.T) {
	st := &fakeStore{}
	lat := []int64{10, 20, 30, 40, 50}
	for i, l := range lat {
		c := mkCall(string(rune('a'+i)), "claude-code:a", time.Duration(i)*time.Minute, "query", "search", telemetry.OutcomeOK, l)
		c.Rung = "L2"
		c.TokensSaved = 2
		st.calls = append(st.calls, c)
	}
	st.calls = append(st.calls, mkCall("x", "claude-code:a", 0, "status", "", telemetry.OutcomeCold, 3))

	tl, err := Tools(context.Background(), st, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Tools) != 2 {
		t.Fatalf("tools=%+v", tl.Tools)
	}
	q := tl.Tools[0]
	if q.Tool != "query" || q.Action != "search" || q.Calls != 5 {
		t.Fatalf("query row=%+v", q)
	}
	if q.P50MS != 30 || math.Abs(q.P95MS-48) > 1e-9 {
		t.Fatalf("p50=%v p95=%v", q.P50MS, q.P95MS)
	}
	if len(q.RungMix) != 1 || q.RungMix[0].Key != "L2" || q.RungMix[0].Count != 5 {
		t.Fatalf("rung mix=%+v", q.RungMix)
	}
	if q.TokensSaved != 10 || q.SuccessRate != 1 {
		t.Fatalf("saved=%d success=%v", q.TokensSaved, q.SuccessRate)
	}
	if tl.Tools[1].SuccessRate != 1 || tl.Tools[1].OutcomeMix[0].Key != telemetry.OutcomeCold {
		t.Fatalf("status row=%+v", tl.Tools[1])
	}
	empty, _ := Tools(context.Background(), &fakeStore{}, Options{})
	if empty.Tools == nil {
		t.Fatal("tools must be non-nil")
	}
}

func TestMemoryKPIsBanksAndRecent(t *testing.T) {
	st := &fakeStore{}
	st.mem = []telemetry.MemoryEvent{
		{ID: "r1", Verb: "recall", Banks: "project-a,global", Returned: 2, ReturnedIDs: "m1,m2", AgeMedianS: 100, TS: t0.Add(1 * time.Minute)},
		{ID: "r2", Verb: "recall", Banks: "project-a", Returned: 0, TS: t0.Add(2 * time.Minute)},
		{ID: "r3", Verb: "recall", Banks: "project-a", Returned: 1, ReturnedIDs: "m1", AgeMedianS: 300, TS: t0.Add(3 * time.Minute)},
		{ID: "w1", Verb: "retain", Banks: "project-a", Written: 4, Skipped: 1, Replaced: 2, TS: t0.Add(4 * time.Minute)},
		{ID: "i1", Verb: "inject", Banks: "project-a", Tokens: 500, TS: t0.Add(5 * time.Minute)},
	}
	ctx := context.Background()

	m, err := Memory(ctx, st, Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := findKPI(m.KPIs, "recall_calls"); k.Value != 3 {
		t.Fatalf("recall calls=%v", k.Value)
	}
	if k, _ := findKPI(m.KPIs, "recall_hit_rate"); math.Abs(k.Value-200.0/3.0) > 1e-9 {
		t.Fatalf("hit rate=%v want 66.67 percent", k.Value)
	}
	if k, _ := findKPI(m.KPIs, "median_age_s"); k.Value != 200 {
		t.Fatalf("median age=%v want 200 over hit recalls", k.Value)
	}
	if k, _ := findKPI(m.KPIs, "retain_written"); k.Value != 4 {
		t.Fatalf("written=%v", k.Value)
	}
	if k, _ := findKPI(m.KPIs, "injected_tokens"); k.Value != 500 {
		t.Fatalf("injected=%v", k.Value)
	}
	if len(m.Banks) != 2 {
		t.Fatalf("banks=%+v", m.Banks)
	}
	var pa report.BankStat
	for _, b := range m.Banks {
		if b.Bank == "project-a" {
			pa = b
		}
	}
	if pa.Recalls != 3 || pa.Written != 4 {
		t.Fatalf("project-a=%+v", pa)
	}
	// written 4, distinct returned ids {m1,m2} -> never-recalled proxy 0.5
	if math.Abs(pa.NeverRecalled-0.5) > 1e-9 {
		t.Fatalf("never recalled proxy=%v want 0.5", pa.NeverRecalled)
	}
	if len(m.Recent) != 5 || m.Recent[0].ID != "i1" {
		t.Fatalf("recent must be newest first, got %+v", m.Recent)
	}

	one, _ := Memory(ctx, st, Options{}, "global")
	if k, _ := findKPI(one.KPIs, "recall_calls"); k.Value != 1 || len(one.Recent) != 1 {
		t.Fatalf("bank filter: recalls=%v recent=%d", k.Value, len(one.Recent))
	}
	empty, _ := Memory(ctx, &fakeStore{}, Options{}, "")
	if empty.Banks == nil || empty.Recent == nil || empty.KPIs == nil {
		t.Fatal("memory lists must be non-nil")
	}
}

func TestTranscriptSyntheticWhenNotGranted(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{mkSession("claude-code:a", 0, time.Hour)}
	c := mkCall("c1", "claude-code:a", time.Minute, "query", "search", telemetry.OutcomeOK, 5)
	c.ArgsRedacted = `{"query":"auth"}`
	c.OutTokensPost = 42
	st.calls = []telemetry.CallEvent{c, mkCall("c2", "claude-code:a", 2*time.Minute, "status", "", telemetry.OutcomeOK, 3)}

	tr, ok, err := Transcript(context.Background(), st, "claude-code:a", nil)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !tr.Synthetic || tr.Reason == "" {
		t.Fatalf("synthetic=%v reason=%q", tr.Synthetic, tr.Reason)
	}
	if len(tr.Messages) != 2 {
		t.Fatalf("messages=%d want one per call", len(tr.Messages))
	}
	part := tr.Messages[0].Content[0]
	if part.Type != "tool-call" || part.Tool == nil || part.Tool.ToolName != "leankg.query" {
		t.Fatalf("part=%+v", part)
	}
	if part.Tool.LeanKG == nil || part.Tool.LeanKG.ID != "c1" || part.Tool.ArgsText != `{"query":"auth"}` {
		t.Fatalf("enrichment=%+v", part.Tool)
	}

	if _, ok, _ := Transcript(context.Background(), st, "nope", nil); ok {
		t.Fatal("unknown session must report ok=false")
	}
}

func TestTranscriptLinkedMapsTurnsAndEnriches(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{{ID: "claude-code:a", ClientName: "claude-code", LinkStatus: "linked", FirstTS: t0, LastTS: t0.Add(time.Hour)}}
	c := mkCall("c1", "claude-code:a", time.Minute, "query", "search", telemetry.OutcomeOK, 5)
	c.HitFiles = "internal/a.go"
	c.ArgsHash = "h1"
	st.calls = []telemetry.CallEvent{c}
	win, _ := json.Marshal(sessionlink.Window{Client: "claude-code", FollowUps: []sessionlink.ToolCall{
		{ID: "tu2", Name: "Grep", Norm: "grep", Target: "internal/zzz.go", TS: t0.Add(2 * time.Minute)},
	}})
	st.links = []telemetry.SessionLink{{CallID: "c1", SessionID: "claude-code:a", ToolUseID: "tu1", Window: string(win)}}

	turns := []sessionlink.Turn{
		{Role: "user", Text: "find auth", TS: t0},
		{Role: "assistant", Text: "searching", TS: t0.Add(time.Minute), ToolCalls: []sessionlink.ToolCall{
			{ID: "tu1", Name: "mcp__leankg__query", Norm: "leankg.query", IsLeanKG: true, ArgsHash: "h1", TS: t0.Add(time.Minute)},
		}, OutputTokens: 7},
		{Role: "assistant", Text: "now grepping", TS: t0.Add(2 * time.Minute), ToolCalls: []sessionlink.ToolCall{
			{ID: "tu2", Name: "Grep", Norm: "grep", Target: "internal/zzz.go", TS: t0.Add(2 * time.Minute)},
		}},
	}
	tr, ok, err := Transcript(context.Background(), st, "claude-code:a", staticSrc(turns))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if tr.Synthetic {
		t.Fatal("linked session with src must not be synthetic")
	}
	if len(tr.Messages) < 3 {
		t.Fatalf("messages=%d", len(tr.Messages))
	}
	if tr.Messages[0].Role != "user" || tr.Messages[0].Content[0].Text != "find auth" {
		t.Fatalf("user message=%+v", tr.Messages[0])
	}
	var lk, grep *report.ToolCallPart
	for _, m := range tr.Messages {
		for _, p := range m.Content {
			if p.Type != "tool-call" {
				continue
			}
			if p.Tool.ToolName == "mcp__leankg__query" {
				lk = p.Tool
			}
			if p.Tool.ToolName == "Grep" {
				grep = p.Tool
			}
		}
	}
	if lk == nil || lk.LeanKG == nil || lk.LeanKG.ID != "c1" {
		t.Fatalf("leankg enrichment missing: %+v", lk)
	}
	if grep == nil || !grep.Fallback {
		t.Fatalf("grep outside hits within K must be marked fallback: %+v", grep)
	}
	// The replay card carries the same DS-17 signals as the session detail
	// (smoke-found: the replay built its row without them).
	if lk.LeanKG.Signals == nil || !lk.LeanKG.Signals.Fallback {
		t.Fatalf("replay LeanKG row must carry signals from the link window: %+v", lk.LeanKG.Signals)
	}
}

func TestTranscriptSourceErrorFallsBackToSynthetic(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{{ID: "claude-code:a", ClientName: "claude-code", LinkStatus: "linked"}}
	st.calls = []telemetry.CallEvent{mkCall("c1", "claude-code:a", 0, "status", "", telemetry.OutcomeOK, 1)}
	src := func(context.Context, telemetry.Session) ([]sessionlink.Turn, error) {
		return nil, sessionlink.ErrNotFound
	}
	tr, ok, err := Transcript(context.Background(), st, "claude-code:a", src)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !tr.Synthetic || tr.Reason == "" {
		t.Fatalf("src error must yield a labelled synthetic timeline: %+v", tr)
	}
}

// A linked session whose transcript yields no turns (moved, or not granted
// for that client) must still show the labelled call timeline, never an empty
// replay (smoke-found).
func TestTranscriptEmptyTurnsFallsBackToSynthetic(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{{ID: "claude-code:a", ClientName: "claude-code", LinkStatus: "linked"}}
	st.calls = []telemetry.CallEvent{mkCall("c1", "claude-code:a", 0, "status", "", telemetry.OutcomeOK, 1)}
	src := func(context.Context, telemetry.Session) ([]sessionlink.Turn, error) { return nil, nil }
	tr, ok, err := Transcript(context.Background(), st, "claude-code:a", src)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !tr.Synthetic || tr.Reason == "" || len(tr.Messages) != 1 {
		t.Fatalf("empty turns must yield a labelled synthetic timeline: %+v", tr)
	}
}

func TestEvidenceControlledValidGate(t *testing.T) {
	st := &fakeStore{}
	mk := func(task, arm string, tokens int64, valid bool) telemetry.ABRun {
		return telemetry.ABRun{ID: task + arm + string(rune('0'+tokens%10)) + string(rune('a'+tokens)), Task: task, Arm: arm, Tokens: tokens, Turns: 3, DurationS: 10, ToolCalls: 4, FileReads: 2, CostUSD: 0.5, JudgeScore: 5, Valid: valid}
	}
	st.ab = []telemetry.ABRun{
		mk("t1", "with", 100, true), mk("t1", "with", 110, true), mk("t1", "with", 120, true),
		mk("t1", "without", 200, true), mk("t1", "without", 210, true), mk("t1", "without", 220, true),
		mk("t2", "with", 50, true), mk("t2", "without", 90, false),
	}
	ev, err := Evidence(context.Background(), st, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ControlledValid {
		t.Fatal("t2 has <3 valid without runs: controlled must not be valid")
	}
	if len(ev.Controlled) != 0 || ev.Controlled == nil {
		t.Fatalf("controlled must be an empty non-nil list when not valid, got %+v", ev.Controlled)
	}
	if ev.ControlledRuns != 7 || ev.ControlledNote == "" {
		t.Fatalf("runs=%d note=%q", ev.ControlledRuns, ev.ControlledNote)
	}

	st.ab = append(st.ab, mk("t2", "with", 55, true), mk("t2", "with", 56, true), mk("t2", "without", 95, true), mk("t2", "without", 96, true), mk("t2", "without", 97, true))
	ev, _ = Evidence(context.Background(), st, Options{}, nil)
	if !ev.ControlledValid {
		t.Fatalf("every task has >=3 valid per arm: must be valid; note=%q", ev.ControlledNote)
	}
	if len(ev.Controlled) != 7 {
		t.Fatalf("controlled metrics=%d want 7", len(ev.Controlled))
	}
	var tok report.ArmCompare
	for _, c := range ev.Controlled {
		if c.Metric == "tokens" {
			tok = c
		}
	}
	// per-task medians: with t1=110, t2=55 -> 82.5; without t1=210, t2=96 (90 is invalid) -> 153
	if tok.With.Median != 82.5 || tok.Without.Median != 153 {
		t.Fatalf("tokens median with=%v without=%v want 82.5/153 (median of per-task medians)", tok.With.Median, tok.Without.Median)
	}
}

func TestEvidenceObservationalSegments(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{
		{ID: "claude-code:a", ClientName: "claude-code", LinkStatus: "linked", FirstTS: t0, LastTS: t0.Add(time.Hour)},
	}
	discovery := func(name, norm string, isLK bool, ts time.Duration) sessionlink.ToolCall {
		return sessionlink.ToolCall{ID: name, Name: name, Norm: norm, IsLeanKG: isLK, TS: t0.Add(ts)}
	}
	// Segment 1 uses LeanKG: 2 discovery calls then an edit.
	seg1 := []sessionlink.Turn{
		{Role: "user", Text: "task one", TS: t0},
		{Role: "assistant", TS: t0.Add(10 * time.Second), InputTokens: 100, ToolCalls: []sessionlink.ToolCall{discovery("s1", "leankg.query", true, 10*time.Second)}},
		{Role: "assistant", TS: t0.Add(20 * time.Second), InputTokens: 200, ToolCalls: []sessionlink.ToolCall{discovery("s2", "grep", false, 20*time.Second)}},
		{Role: "assistant", TS: t0.Add(30 * time.Second), InputTokens: 300, ToolCalls: []sessionlink.ToolCall{{ID: "e1", Norm: "edit", TS: t0.Add(30 * time.Second)}}},
		{Role: "assistant", TS: t0.Add(40 * time.Second), InputTokens: 10, ToolCalls: []sessionlink.ToolCall{{ID: "e2", Norm: "edit"}, {ID: "e3", Norm: "bash"}}},
	}
	// Segment 2 does not use LeanKG: 3 grep/read calls then an edit.
	seg2 := []sessionlink.Turn{
		{Role: "user", Text: "task two", TS: t0.Add(time.Minute)},
		{Role: "assistant", TS: t0.Add(61 * time.Second), InputTokens: 400, ToolCalls: []sessionlink.ToolCall{discovery("g1", "grep", false, 0)}},
		{Role: "assistant", TS: t0.Add(62 * time.Second), InputTokens: 400, ToolCalls: []sessionlink.ToolCall{discovery("g2", "read", false, 0)}},
		{Role: "assistant", TS: t0.Add(63 * time.Second), InputTokens: 400, ToolCalls: []sessionlink.ToolCall{discovery("g3", "glob", false, 0)}},
		{Role: "assistant", TS: t0.Add(64 * time.Second), InputTokens: 400, ToolCalls: []sessionlink.ToolCall{{ID: "e4", Norm: "edit"}}},
	}
	// Segment 3 has only 2 tool calls and must be excluded.
	seg3 := []sessionlink.Turn{
		{Role: "user", Text: "tiny", TS: t0.Add(2 * time.Minute)},
		{Role: "assistant", TS: t0.Add(121 * time.Second), ToolCalls: []sessionlink.ToolCall{discovery("t1", "grep", false, 0), discovery("t2", "grep", false, 0)}},
	}
	turns := append(append(append([]sessionlink.Turn{}, seg1...), seg2...), seg3...)

	ev, err := Evidence(context.Background(), st, Options{}, staticSrc(turns))
	if err != nil {
		t.Fatal(err)
	}
	if ev.ObservationalNote == "" {
		t.Fatal("observational note must say it is confounded")
	}
	byMetric := map[string]report.ArmCompare{}
	for _, c := range ev.Observational {
		byMetric[c.Metric] = c
	}
	disc, ok := byMetric["discovery_calls_before_edit"]
	if !ok {
		t.Fatalf("missing discovery metric in %+v", ev.Observational)
	}
	if disc.With.N != 1 || disc.Without.N != 1 {
		t.Fatalf("n with=%d without=%d want 1/1 (short segment excluded)", disc.With.N, disc.Without.N)
	}
	if disc.With.Median != 2 || disc.Without.Median != 3 {
		t.Fatalf("discovery with=%v without=%v want 2/3", disc.With.Median, disc.Without.Median)
	}
	tok := byMetric["input_tokens_before_edit"]
	if tok.With.Median != 300 || tok.Without.Median != 1200 {
		t.Fatalf("input tokens with=%v without=%v want 300/1200 (turns strictly before first edit turn)", tok.With.Median, tok.Without.Median)
	}
	if ev.ControlledValid {
		t.Fatal("no ab runs: controlled must not be valid")
	}

	none, _ := Evidence(context.Background(), st, Options{}, nil)
	if none.Observational == nil || len(none.Observational) != 0 {
		t.Fatal("nil src must give an empty observational list")
	}
}

func TestPercentileInterpolates(t *testing.T) {
	cases := []struct {
		xs   []float64
		p    float64
		want float64
	}{
		{nil, 0.5, 0},
		{[]float64{7}, 0.95, 7},
		{[]float64{1, 2, 3, 4}, 0.5, 2.5},
		{[]float64{10, 20, 30, 40, 50}, 0.95, 48},
		{[]float64{10, 20, 30, 40, 50}, 0, 10},
		{[]float64{10, 20, 30, 40, 50}, 1, 50},
	}
	for _, c := range cases {
		if got := percentile(c.xs, c.p); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("percentile(%v,%v)=%v want %v", c.xs, c.p, got, c.want)
		}
	}
}

// Handshake rows (initialize / server/discover) carry identity but are not
// tool calls: they must not count as calls or move the success rate, and they
// do not appear as calls of a session (smoke-found).
func TestHandshakeRowsAreNotCalls(t *testing.T) {
	st := &fakeStore{}
	st.sessions = []telemetry.Session{{ID: "s", ClientName: "claude-code", FirstTS: t0, LastTS: t0}}
	hs := mkCall("h1", "s", 0, "", "", telemetry.OutcomeOK, 1)
	hs.Method = telemetry.MethodInitialize
	st.calls = []telemetry.CallEvent{hs, mkCall("c1", "s", time.Second, "query", "", telemetry.OutcomeErrorPrefix+"X", 3)}
	ov, err := Overview(context.Background(), st, telemetry.Config{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range ov.KPIs {
		if k.Key == "calls" && k.Value != 1 {
			t.Fatalf("calls KPI = %v, want 1 (handshake excluded)", k.Value)
		}
	}
	det, ok, err := SessionDetail(context.Background(), st, "s")
	if err != nil || !ok {
		t.Fatalf("detail ok=%v err=%v", ok, err)
	}
	if len(det.Calls) != 1 || det.Session.Calls != 1 {
		t.Fatalf("session calls = %d/%d, want 1", len(det.Calls), det.Session.Calls)
	}
}

// SessionText is the `leankg metrics --session` summary (DS-16): the session
// line, its KPIs with estimate markers, and its ranked failures.
func TestSessionTextRendersKPIsAndFailures(t *testing.T) {
	d := report.SessionDetail{
		Session: report.SessionSummary{ID: "claude-code:abc", ClientName: "claude-code", Calls: 3, Errors: 1, Correlation: "exact", LinkStatus: "linked"},
		KPIs: []report.KPI{
			{Key: "calls", Label: "Calls", Value: 3},
			{Key: "tokens_saved", Label: "Tokens saved", Value: 1200, Unit: "tokens", Estimate: true, Method: "file_read"},
		},
		Failures: []report.Count{{Key: "zero_hit", Count: 1}},
	}
	got := SessionText(d)
	for _, want := range []string{"claude-code:abc", "exact", "linked", "Calls: 3", "Tokens saved: 1200 tokens (estimate, file_read)", "zero_hit: 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("SessionText missing %q:\n%s", want, got)
		}
	}
}
