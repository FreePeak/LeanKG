package sessionlink

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// fakeStore implements telemetry.Store with only the methods the runner uses.
type fakeStore struct {
	telemetry.Store // embedded nil: any unexpected method call panics

	mu        sync.Mutex
	calls     []telemetry.CallEvent
	sessions  map[string]telemetry.Session
	links     []telemetry.SessionLink
	upserted  []telemetry.Session
	lastOlder time.Time
}

func newFakeStore(calls []telemetry.CallEvent, sessions ...telemetry.Session) *fakeStore {
	f := &fakeStore{calls: calls, sessions: map[string]telemetry.Session{}}
	for _, s := range sessions {
		f.sessions[s.ID] = s
	}
	return f
}

func (f *fakeStore) UnlinkedCalls(_ context.Context, olderThan time.Time, limit int) ([]telemetry.CallEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastOlder = olderThan
	linked := map[string]bool{}
	for _, l := range f.links {
		linked[l.CallID] = true
	}
	var out []telemetry.CallEvent
	for _, c := range f.calls {
		if linked[c.ID] || !c.TS.Before(olderThan) {
			continue
		}
		out = append(out, c)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) Session(_ context.Context, id string) (telemetry.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	return s, ok, nil
}

func (f *fakeStore) UpsertSessions(_ context.Context, ss []telemetry.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range ss {
		f.sessions[s.ID] = s
		f.upserted = append(f.upserted, s)
	}
	return nil
}

func (f *fakeStore) UpsertLinks(_ context.Context, ls []telemetry.SessionLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, ls...)
	return nil
}

// spyAdapter counts every method call so consent tests can assert zero opens.
type spyAdapter struct {
	client string
	mu     sync.Mutex
	calls  map[string]int
	locate func(c CallRef) (TranscriptRef, float64, error)
	window func(t TranscriptRef, c CallRef, n int) (Window, error)
}

func newSpy(client string) *spyAdapter {
	return &spyAdapter{client: client, calls: map[string]int{}}
}

func (s *spyAdapter) hit(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[name]++
}

func (s *spyAdapter) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.calls {
		n += v
	}
	return n
}

func (s *spyAdapter) Client() string { s.hit("Client"); return s.client }

func (s *spyAdapter) Detect(string) bool { s.hit("Detect"); return true }

func (s *spyAdapter) Roots(string) []string { s.hit("Roots"); return []string{"/spy"} }

func (s *spyAdapter) Locate(_ string, c CallRef) (TranscriptRef, Confidence, error) {
	s.hit("Locate")
	if s.locate == nil {
		return TranscriptRef{}, 0, ErrNotFound
	}
	t, conf, err := s.locate(c)
	return t, Confidence(conf), err
}

func (s *spyAdapter) Window(t TranscriptRef, c CallRef, n int) (Window, error) {
	s.hit("Window")
	if s.window == nil {
		return Window{}, errors.New("no window")
	}
	return s.window(t, c, n)
}

func (s *spyAdapter) Transcript(TranscriptRef, int) ([]Turn, error) {
	s.hit("Transcript")
	return nil, nil
}

// experimentalSpy also implements the optional Experimental flag.
type experimentalSpy struct{ *spyAdapter }

func (experimentalSpy) Experimental() bool { return true }

// grantOnly makes grantedFn allow exactly the listed clients for one test.
func grantOnly(t *testing.T, clients ...string) {
	t.Helper()
	prev := grantedFn
	t.Cleanup(func() { grantedFn = prev })
	allowed := map[string]bool{}
	for _, c := range clients {
		allowed[c] = true
	}
	grantedFn = func(_ telemetry.Config, client string) bool { return allowed[client] }
}

var baseTS = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func mkCall(id, client, sid string, ts time.Time) telemetry.CallEvent {
	return telemetry.CallEvent{
		ID:        id,
		TS:        ts,
		Tool:      "query",
		Action:    "search",
		ArgsHash:  "hash-" + id,
		SessionID: client + ":" + sid,
		Identity:  telemetry.Identity{ClientName: client, ClientSessionID: sid, Cwd: "/work/repo"},
	}
}

func baseWindow(conf float64) Window {
	return Window{
		Client:     "claude-code",
		SessionID:  "s1",
		Prompt:     "find the auth handler",
		Matched:    &ToolCall{ID: "tu-1", Norm: "leankg.query", Name: "mcp__leankg__query"},
		Confidence: Confidence(conf),
	}
}

func TestLinkConsentOffMakesZeroAdapterCalls(t *testing.T) {
	// Nothing granted: the runner must not touch the adapter at all.
	grantOnly(t)
	st := newFakeStore([]telemetry.CallEvent{
		mkCall("c1", "claude-code", "s1", baseTS),
		mkCall("c2", "claude-code", "s1", baseTS.Add(time.Second)),
	})
	spy := newSpy("claude-code")
	spy.locate = func(CallRef) (TranscriptRef, float64, error) {
		return TranscriptRef{Path: "/x"}, 1, nil
	}
	spy.window = func(TranscriptRef, CallRef, int) (Window, error) { return baseWindow(1), nil }

	stats, err := Link(context.Background(), st, telemetry.Config{}, t.TempDir(), []Adapter{spy}, baseTS.Add(10*time.Minute), 2*time.Minute)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if n := spy.total(); n != 0 {
		t.Fatalf("consent off: adapter calls = %d, want 0 (%v)", n, spy.calls)
	}
	if stats.Considered != 2 || stats.Skipped != 2 || stats.Linked != 0 {
		t.Fatalf("stats = %+v, want considered=2 skipped=2 linked=0", stats)
	}
	if len(st.links) != 0 {
		t.Fatalf("consent off wrote %d links, want 0", len(st.links))
	}
}

func TestLinkConsentIsPerClient(t *testing.T) {
	grantOnly(t, "claude-code")
	st := newFakeStore([]telemetry.CallEvent{
		mkCall("c1", "claude-code", "s1", baseTS),
		mkCall("c2", "opencode", "s2", baseTS),
	})
	granted := newSpy("claude-code")
	denied := newSpy("opencode")
	granted.locate = func(CallRef) (TranscriptRef, float64, error) {
		return TranscriptRef{Path: "/t.jsonl", SessionID: "s1"}, 1, nil
	}
	granted.window = func(TranscriptRef, CallRef, int) (Window, error) { return baseWindow(1), nil }

	stats, err := Link(context.Background(), st, telemetry.Config{}, t.TempDir(), []Adapter{granted, denied}, baseTS.Add(10*time.Minute), 2*time.Minute)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if denied.total() != 0 {
		t.Fatalf("denied client was touched: %v", denied.calls)
	}
	if granted.calls["Locate"] != 1 || stats.Linked != 1 || stats.Skipped != 1 {
		t.Fatalf("granted client: locate=%d stats=%+v", granted.calls["Locate"], stats)
	}
}

func TestLinkStoresWindowAndSessionStatus(t *testing.T) {
	grantOnly(t, "claude-code")
	sess := telemetry.Session{ID: "claude-code:s1", ClientName: "claude-code", ClientSessionID: "s1"}
	st := newFakeStore([]telemetry.CallEvent{mkCall("c1", "claude-code", "s1", baseTS)}, sess)
	spy := newSpy("claude-code")
	spy.locate = func(CallRef) (TranscriptRef, float64, error) {
		return TranscriptRef{Client: "claude-code", Path: "/home/u/.claude/projects/p/s1.jsonl", SessionID: "s1"}, 1, nil
	}
	spy.window = func(_ TranscriptRef, c CallRef, n int) (Window, error) {
		if c.CallID != "c1" || n != windowTurns {
			t.Errorf("Window called with call=%q n=%d, want c1 and %d", c.CallID, n, windowTurns)
		}
		return baseWindow(1), nil
	}

	stats, err := Link(context.Background(), st, telemetry.Config{}, t.TempDir(), []Adapter{spy}, baseTS.Add(10*time.Minute), 2*time.Minute)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if stats.Linked != 1 || stats.Errors != 0 {
		t.Fatalf("stats = %+v, want linked=1", stats)
	}
	if len(st.links) != 1 {
		t.Fatalf("links = %d, want 1", len(st.links))
	}
	l := st.links[0]
	if l.CallID != "c1" || l.SessionID != "claude-code:s1" || l.ToolUseID != "tu-1" || l.Confidence != 1 {
		t.Fatalf("link = %+v", l)
	}
	var w Window
	if err := json.Unmarshal([]byte(l.Window), &w); err != nil {
		t.Fatalf("stored window is not JSON: %v", err)
	}
	if w.Prompt != "find the auth handler" {
		t.Fatalf("stored prompt = %q", w.Prompt)
	}
	got := st.sessions["claude-code:s1"]
	if got.LinkStatus != "linked" || got.TranscriptPath == "" {
		t.Fatalf("session = %+v, want linked with transcript path", got)
	}
}

func TestLinkStatusesForNotFoundAndUnsupported(t *testing.T) {
	grantOnly(t, "claude-code", "opencode")
	st := newFakeStore([]telemetry.CallEvent{
		mkCall("c1", "claude-code", "s1", baseTS),
		mkCall("c2", "opencode", "s2", baseTS),
	},
		telemetry.Session{ID: "claude-code:s1"},
		telemetry.Session{ID: "opencode:s2"},
	)
	cc := newSpy("claude-code")
	cc.locate = func(CallRef) (TranscriptRef, float64, error) { return TranscriptRef{}, 0, ErrNotFound }
	oc := newSpy("opencode")
	oc.locate = func(CallRef) (TranscriptRef, float64, error) { return TranscriptRef{}, 0, ErrUnsupportedVersion }

	stats, err := Link(context.Background(), st, telemetry.Config{}, t.TempDir(), []Adapter{cc, oc}, baseTS.Add(10*time.Minute), 2*time.Minute)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if stats.NotFound != 1 || stats.Errors != 1 {
		t.Fatalf("stats = %+v, want not_found=1 errors=1", stats)
	}
	if got := st.sessions["claude-code:s1"].LinkStatus; got != "not_found" {
		t.Fatalf("claude-code status = %q, want not_found", got)
	}
	if got := st.sessions["opencode:s2"].LinkStatus; got != "unsupported_version" {
		t.Fatalf("opencode status = %q, want unsupported_version", got)
	}
	// Terminal outcomes are closed out so the next pass does not re-read them.
	if len(st.links) != 2 {
		t.Fatalf("marker links = %d, want 2", len(st.links))
	}
	for _, l := range st.links {
		if l.Window != "" || l.Confidence != 0 {
			t.Fatalf("marker link carries data: %+v", l)
		}
	}
}

func TestLinkFailSoftOnAdapterError(t *testing.T) {
	grantOnly(t, "claude-code")
	st := newFakeStore([]telemetry.CallEvent{
		mkCall("c1", "claude-code", "s1", baseTS),
		mkCall("c2", "claude-code", "s1", baseTS.Add(time.Second)),
	}, telemetry.Session{ID: "claude-code:s1"})
	spy := newSpy("claude-code")
	spy.locate = func(c CallRef) (TranscriptRef, float64, error) {
		if c.CallID == "c1" {
			return TranscriptRef{}, 0, errors.New("disk on fire")
		}
		return TranscriptRef{Path: "/t"}, 1, nil
	}
	spy.window = func(TranscriptRef, CallRef, int) (Window, error) { return baseWindow(1), nil }

	stats, err := Link(context.Background(), st, telemetry.Config{}, t.TempDir(), []Adapter{spy}, baseTS.Add(10*time.Minute), 2*time.Minute)
	if err != nil {
		t.Fatalf("Link returned error, want soft failure: %v", err)
	}
	if stats.Errors != 1 || stats.Linked != 1 {
		t.Fatalf("stats = %+v, want errors=1 linked=1", stats)
	}
	// A transient error writes nothing, so the call is retried next pass.
	for _, l := range st.links {
		if l.CallID == "c1" {
			t.Fatalf("transient error closed out call c1")
		}
	}
}

func TestLinkExperimentalIsFlagged(t *testing.T) {
	grantOnly(t, "codex")
	st := newFakeStore([]telemetry.CallEvent{mkCall("c1", "codex", "s1", baseTS)}, telemetry.Session{ID: "codex:s1"})
	inner := newSpy("codex")
	inner.locate = func(CallRef) (TranscriptRef, float64, error) { return TranscriptRef{Path: "/t"}, 0.5, nil }
	inner.window = func(TranscriptRef, CallRef, int) (Window, error) { return baseWindow(0.5), nil }

	_, err := Link(context.Background(), st, telemetry.Config{}, t.TempDir(), []Adapter{experimentalSpy{inner}}, baseTS.Add(10*time.Minute), 2*time.Minute)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(st.links) != 1 {
		t.Fatalf("links = %d, want 1 (experimental still links)", len(st.links))
	}
	if !strings.Contains(st.links[0].Window, `"experimental":true`) {
		t.Fatalf("experimental flag missing from stored window: %s", st.links[0].Window)
	}
}

func TestLinkCapsStoredWindowAndKeepsJSONValid(t *testing.T) {
	grantOnly(t, "claude-code")
	st := newFakeStore([]telemetry.CallEvent{mkCall("c1", "claude-code", "s1", baseTS)}, telemetry.Session{ID: "claude-code:s1"})
	spy := newSpy("claude-code")
	spy.locate = func(CallRef) (TranscriptRef, float64, error) { return TranscriptRef{Path: "/t"}, 1, nil }
	big := strings.Repeat("x", 9000)
	spy.window = func(TranscriptRef, CallRef, int) (Window, error) {
		w := baseWindow(1)
		for i := 0; i < 12; i++ {
			w.After = append(w.After, Turn{Role: "assistant", Text: big})
			w.Before = append(w.Before, Turn{Role: "assistant", Text: big})
		}
		return w, nil
	}

	if _, err := Link(context.Background(), st, telemetry.Config{}, t.TempDir(), []Adapter{spy}, baseTS.Add(10*time.Minute), 2*time.Minute); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(st.links) != 1 {
		t.Fatalf("links = %d", len(st.links))
	}
	w := st.links[0].Window
	if len(w) > maxWindowBytes {
		t.Fatalf("stored window = %d bytes, cap %d", len(w), maxWindowBytes)
	}
	var out Window
	if err := json.Unmarshal([]byte(w), &out); err != nil {
		t.Fatalf("capped window is not valid JSON: %v", err)
	}
	if out.Prompt != "find the auth handler" {
		t.Fatalf("prompt lost while capping: %q", out.Prompt)
	}
}

func TestLinkPassesCutoffAndBatchLimit(t *testing.T) {
	grantOnly(t)
	st := newFakeStore(nil)
	now := baseTS
	if _, err := Link(context.Background(), st, telemetry.Config{}, t.TempDir(), nil, now, 2*time.Minute); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if want := now.Add(-2 * time.Minute); !st.lastOlder.Equal(want) {
		t.Fatalf("UnlinkedCalls cutoff = %v, want %v", st.lastOlder, want)
	}
}

func TestLoopRereadsConfigEachPass(t *testing.T) {
	grantOnly(t)
	st := newFakeStore(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	reads := 0
	cfgFn := func() telemetry.Config {
		mu.Lock()
		defer mu.Unlock()
		reads++
		if reads >= 3 {
			cancel()
		}
		return telemetry.Config{}
	}
	done := make(chan struct{})
	go func() {
		Loop(ctx, st, t.TempDir(), cfgFn, nil, 5*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Loop did not stop after context cancel")
	}
	mu.Lock()
	defer mu.Unlock()
	if reads < 3 {
		t.Fatalf("cfgFn read %d times, want at least 3 (one per pass)", reads)
	}
}
