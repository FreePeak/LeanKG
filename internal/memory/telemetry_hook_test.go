package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// fakeRecorder captures what the memory layer hands the process sink.
type fakeRecorder struct {
	mu    sync.Mutex
	level telemetry.Level
	calls []telemetry.CallEvent
	mems  []telemetry.MemoryEvent
}

func (f *fakeRecorder) Level() telemetry.Level { return f.level }
func (f *fakeRecorder) RecordCall(ev telemetry.CallEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ev)
}
func (f *fakeRecorder) RecordMemory(ev telemetry.MemoryEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mems = append(f.mems, ev)
}
func (f *fakeRecorder) Close() error { return nil }

func (f *fakeRecorder) memEvents() []telemetry.MemoryEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]telemetry.MemoryEvent(nil), f.mems...)
}

// useRecorder installs a fake sink for one test and restores the no-op.
func useRecorder(t *testing.T, level telemetry.Level) *fakeRecorder {
	t.Helper()
	rec := &fakeRecorder{level: level}
	SetRecorder(rec)
	t.Cleanup(func() { SetRecorder(nil) })
	return rec
}

// verbEvents keeps the events of one verb, in emit order.
func verbEvents(evs []telemetry.MemoryEvent, verb string) []telemetry.MemoryEvent {
	var out []telemetry.MemoryEvent
	for _, ev := range evs {
		if ev.Verb == verb {
			out = append(out, ev)
		}
	}
	return out
}

func identityCtx() context.Context {
	return telemetry.WithIdentity(context.Background(),
		telemetry.Identity{ClientName: "omp", ClientSessionID: "sess-1", Cwd: "/work/repo"},
		telemetry.TransportHS)
}

func TestRecallEmitsMemoryEvent(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	now := time.Now().Unix()
	if err := m.RetainRaw("b1", []Entry{
		{ID: "a", Content: "postgres stores the vector index", Timestamp: now - 100},
		{ID: "b", Content: "vector index rebuilt nightly", Timestamp: now - 7200},
		{ID: "c", Content: "unrelated cake recipe", Timestamp: now},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := m.RecallFilteredCtx(identityCtx(), []string{"b1"}, "vector index", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := verbEvents(rec.memEvents(), "recall")
	if len(evs) != 1 {
		t.Fatalf("recall events = %d, want exactly one", len(evs))
	}
	ev := evs[0]
	if ev.Verb != "recall" || ev.Transport != telemetry.TransportHS {
		t.Fatalf("verb/transport = %q/%q", ev.Verb, ev.Transport)
	}
	if ev.ClientName != "omp" || ev.ClientSessionID != "sess-1" {
		t.Fatalf("identity not joined: %+v", ev.Identity)
	}
	if ev.Banks != "b1" || ev.Limit != 5 {
		t.Fatalf("banks/limit = %q/%d", ev.Banks, ev.Limit)
	}
	if want := telemetry.ArgsHash([]byte(`"vector index"`)); ev.QueryHash != want {
		t.Fatalf("query hash = %s, want ArgsHash of the query JSON %s", ev.QueryHash, want)
	}
	if len(rows) != 2 || ev.Returned != 2 {
		t.Fatalf("returned = %d (rows %d), want 2", ev.Returned, len(rows))
	}
	if want := rows[0].ID + "," + rows[1].ID; ev.ReturnedIDs != want {
		t.Fatalf("returned ids = %q, want rank order %q", ev.ReturnedIDs, want)
	}
	if n := len(splitCSV(ev.Scores)); n != 2 {
		t.Fatalf("scores = %q, want one per returned row", ev.Scores)
	}
	if ev.DenseHits != 0 {
		t.Fatalf("dense hits = %d without a vectorizer", ev.DenseHits)
	}
	if ev.AgeMaxS < 7200 || ev.AgeMedianS < 100 || ev.AgeMedianS > 7200 {
		t.Fatalf("ages median/max = %d/%d", ev.AgeMedianS, ev.AgeMaxS)
	}
}

func TestRecallScoresAreRealAndMonotonic(t *testing.T) {
	m := openTest(t)
	if err := m.RetainRaw("b", []Entry{
		{ID: "1", Content: "alpha beta"},
		{ID: "2", Content: "alpha alpha alpha beta"},
		{ID: "3", Content: "alpha"},
		{ID: "4", Content: "beta gamma alpha alpha"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := m.RecallFilteredCtx(context.Background(), []string{"b"}, "alpha beta", 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 3 {
		t.Fatalf("rows = %d, want at least 3", len(rows))
	}
	for i, r := range rows {
		if r.Score <= 0 {
			t.Fatalf("row %d score = %v, want a real positive relevance score", i, r.Score)
		}
		if i > 0 && r.Score > rows[i-1].Score {
			t.Fatalf("scores not non-increasing in rank order: %v then %v", rows[i-1].Score, r.Score)
		}
	}
	ranked := RankEntries(rows)
	for i := range ranked {
		if ranked[i].Score != rows[i].Score {
			t.Fatalf("RankEntries dropped the score at %d: %v != %v", i, ranked[i].Score, rows[i].Score)
		}
	}
}

func TestDenseFusedScoresMonotonic(t *testing.T) {
	m := openTest(t)
	if err := m.RetainRaw("b", []Entry{
		{ID: "pg", Content: "We store embeddings in pgvector on Postgres."},
		{ID: "rel", Content: "CI will publish binaries when the tag lands."},
	}); err != nil {
		t.Fatal(err)
	}
	m.SetVectorizer(&conceptVectorizer{floor: 0.5})
	rows, err := m.RecallFilteredCtx(context.Background(), []string{"b"}, "which database holds the vectors", 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || rows[0].ID != "pg" {
		t.Fatalf("rows = %v, want pg first", rows)
	}
	if rows[0].Score <= 0 {
		t.Fatalf("fused score = %v, want positive", rows[0].Score)
	}
}

func TestDenseHitsCountsDenseRankedRows(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	if err := m.RetainRaw("b", []Entry{
		{ID: "pg", Content: "We store embeddings in pgvector on Postgres."},
	}); err != nil {
		t.Fatal(err)
	}
	m.SetVectorizer(&conceptVectorizer{floor: 0.5})
	if _, err := m.RecallFilteredCtx(context.Background(), []string{"b"}, "which database holds the vectors", 3, nil); err != nil {
		t.Fatal(err)
	}
	evs := verbEvents(rec.memEvents(), "recall")
	if len(evs) != 1 || evs[0].DenseHits != 1 {
		t.Fatalf("events = %+v, want one recall with dense_hits=1", evs)
	}
}

func TestRetainEventsCountWrittenAndSkipped(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	turns := []Entry{{Content: "first turn"}, {Content: "second turn"}}
	if _, err := m.RetainCtx(identityCtx(), "b", turns, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RetainCtx(identityCtx(), "b", []Entry{{Content: "again"}, {Content: "again2"}}, 3); err != nil {
		t.Fatal(err)
	}
	evs := verbEvents(rec.memEvents(), "retain")
	if len(evs) != 2 {
		t.Fatalf("retain events = %d, want 2", len(evs))
	}
	if evs[0].Verb != "retain" || evs[0].Written != 2 || evs[0].Skipped != 0 || evs[0].Banks != "b" {
		t.Fatalf("first retain event = %+v", evs[0])
	}
	if evs[1].Written != 0 || evs[1].Skipped != 2 {
		t.Fatalf("cursor-skipped retain event = %+v, want skipped=2", evs[1])
	}
	if evs[0].ClientSessionID != "sess-1" {
		t.Fatalf("retain identity not joined: %+v", evs[0].Identity)
	}
}

func TestRetainReplacingReturnsReplacedCount(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	doc := func(content string) Entry {
		return Entry{Content: content, Metadata: map[string]any{"document_id": "doc1"}}
	}
	if err := m.RetainRaw("b", []Entry{doc("old one"), doc("old two"), {Content: "keep me"}}); err != nil {
		t.Fatal(err)
	}
	replaced, err := m.RetainReplacingCtx(identityCtx(), "b", []Entry{doc("new")}, []string{"doc1"})
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 2 {
		t.Fatalf("replaced = %d, want 2", replaced)
	}
	evs := verbEvents(rec.memEvents(), "retain")
	if len(evs) != 2 || evs[1].Replaced != 2 || evs[1].Written != 1 {
		t.Fatalf("retain event = %+v, want written=1 replaced=2", evs)
	}
	rows, _, err := m.List("b", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows after replace = %d, want 2 (kept + new)", len(rows))
	}
}

func TestDeleteDocumentEvent(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	if err := m.RetainRaw("b", []Entry{{Content: "x", Metadata: map[string]any{"document_id": "d"}}}); err != nil {
		t.Fatal(err)
	}
	n, err := m.DeleteDocumentCtx(identityCtx(), "b", "d")
	if err != nil || n != 1 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	evs := verbEvents(rec.memEvents(), "delete")
	if len(evs) != 1 || evs[0].Deleted != 1 {
		t.Fatalf("delete event = %+v", evs)
	}
}

func TestInjectEventRowsAndTokens(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	if err := m.RetainRaw("b", []Entry{
		{ID: "a", Content: "vector index lives in postgres"},
		{ID: "b", Content: "vector index rebuilt nightly"},
	}); err != nil {
		t.Fatal(err)
	}
	scope, _ := ParseScope("")
	text, entries, err := m.FirstTurnMemoriesCtx(identityCtx(), scope, "", "b", "vector index")
	if err != nil {
		t.Fatal(err)
	}
	var inject *telemetry.MemoryEvent
	for _, ev := range rec.memEvents() {
		if ev.Verb == "inject" {
			e := ev
			inject = &e
		}
	}
	if inject == nil {
		t.Fatalf("no inject event in %+v", rec.memEvents())
	}
	if inject.Returned != len(entries) || inject.Returned == 0 {
		t.Fatalf("inject rows = %d, want %d", inject.Returned, len(entries))
	}
	if inject.Tokens != int64(len(text)/4) {
		t.Fatalf("inject tokens = %d, want bytes/4 = %d", inject.Tokens, len(text)/4)
	}
	if want := entries[0].ID + "," + entries[1].ID; inject.ReturnedIDs != want {
		t.Fatalf("inject ids = %q, want rank order %q", inject.ReturnedIDs, want)
	}
}

func TestOffRecordsNothingAndResultsMatch(t *testing.T) {
	seed := func(m *Memory) {
		if err := m.RetainRaw("b", []Entry{
			{ID: "1", Content: "alpha beta gamma"},
			{ID: "2", Content: "alpha delta"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	run := func(m *Memory) []string {
		rows, err := m.RecallFilteredCtx(context.Background(), []string{"b"}, "alpha beta", 5, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.DeleteDocumentCtx(context.Background(), "b", "nope"); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids
	}

	off := useRecorder(t, telemetry.Off)
	mOff := openTest(t)
	seed(mOff)
	idsOff := run(mOff)
	if n := len(off.memEvents()); n != 0 {
		t.Fatalf("Off recorded %d memory events", n)
	}
	SetRecorder(nil)

	on := useRecorder(t, telemetry.Metadata)
	mOn := openTest(t)
	seed(mOn)
	idsOn := run(mOn)
	if len(on.memEvents()) == 0 {
		t.Fatalf("Metadata recorded nothing")
	}
	if len(idsOff) != len(idsOn) {
		t.Fatalf("results differ with capture on: %v vs %v", idsOff, idsOn)
	}
	for i := range idsOff {
		if idsOff[i] != idsOn[i] {
			t.Fatalf("results differ with capture on: %v vs %v", idsOff, idsOn)
		}
	}
}

// splitCSV splits a comma-joined event field, treating "" as empty.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
