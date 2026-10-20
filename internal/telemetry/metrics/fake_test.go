package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// fakeStore is an in-memory telemetry.Store for the metrics tests. It honours
// the CallFilter fields the engine relies on and nothing else.
type fakeStore struct {
	calls    []telemetry.CallEvent
	mem      []telemetry.MemoryEvent
	sessions []telemetry.Session
	links    []telemetry.SessionLink
	ab       []telemetry.ABRun
	inserted []telemetry.ABRun
}

var _ telemetry.Store = (*fakeStore)(nil)

func (f *fakeStore) InsertCalls(_ context.Context, cs []telemetry.CallEvent) error {
	f.calls = append(f.calls, cs...)
	return nil
}

func (f *fakeStore) InsertMemoryEvents(_ context.Context, evs []telemetry.MemoryEvent) error {
	f.mem = append(f.mem, evs...)
	return nil
}

func (f *fakeStore) UpsertSessions(_ context.Context, ss []telemetry.Session) error {
	f.sessions = append(f.sessions, ss...)
	return nil
}

func (f *fakeStore) UpsertLinks(_ context.Context, ls []telemetry.SessionLink) error {
	f.links = append(f.links, ls...)
	return nil
}

func (f *fakeStore) InsertABRuns(_ context.Context, rs []telemetry.ABRun) error {
	f.inserted = append(f.inserted, rs...)
	return nil
}

func (f *fakeStore) AddDropped(context.Context, int64) error { return nil }

func matchCall(c telemetry.CallEvent, fl telemetry.CallFilter) bool {
	if !fl.Since.IsZero() && c.TS.Before(fl.Since) {
		return false
	}
	if !fl.Until.IsZero() && c.TS.After(fl.Until) {
		return false
	}
	if fl.Client != "" && c.ClientName != fl.Client {
		return false
	}
	if fl.Project != "" && c.Project != fl.Project {
		return false
	}
	if fl.SessionID != "" && c.SessionID != fl.SessionID {
		return false
	}
	if fl.Tool != "" && c.Tool != fl.Tool {
		return false
	}
	switch {
	case fl.Outcome == "":
	case fl.Outcome == "error":
		if !strings.HasPrefix(c.Outcome, telemetry.OutcomeErrorPrefix) {
			return false
		}
	case c.Outcome != fl.Outcome:
		return false
	}
	return true
}

func (f *fakeStore) Calls(_ context.Context, fl telemetry.CallFilter) ([]telemetry.CallEvent, error) {
	var out []telemetry.CallEvent
	for _, c := range f.calls {
		if matchCall(c, fl) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeStore) Call(_ context.Context, id string) (telemetry.CallEvent, bool, error) {
	for _, c := range f.calls {
		if c.ID == id {
			return c, true, nil
		}
	}
	return telemetry.CallEvent{}, false, nil
}

func (f *fakeStore) MemoryEvents(_ context.Context, fl telemetry.CallFilter) ([]telemetry.MemoryEvent, error) {
	var out []telemetry.MemoryEvent
	for _, m := range f.mem {
		if !fl.Since.IsZero() && m.TS.Before(fl.Since) {
			continue
		}
		if fl.SessionID != "" && m.SessionID != fl.SessionID {
			continue
		}
		if fl.Client != "" && m.ClientName != fl.Client {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func (f *fakeStore) Sessions(_ context.Context, fl telemetry.CallFilter) ([]telemetry.Session, error) {
	var out []telemetry.Session
	for _, s := range f.sessions {
		if !fl.Since.IsZero() && s.LastTS.Before(fl.Since) {
			continue
		}
		if fl.Client != "" && s.ClientName != fl.Client {
			continue
		}
		if fl.Project != "" && s.Project != fl.Project {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeStore) Session(_ context.Context, id string) (telemetry.Session, bool, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			return s, true, nil
		}
	}
	return telemetry.Session{}, false, nil
}

func (f *fakeStore) Links(_ context.Context, sid string) ([]telemetry.SessionLink, error) {
	var out []telemetry.SessionLink
	for _, l := range f.links {
		if sid == "" || l.SessionID == sid {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeStore) UnlinkedCalls(context.Context, time.Time, int) ([]telemetry.CallEvent, error) {
	return nil, nil
}

func (f *fakeStore) ABRuns(context.Context) ([]telemetry.ABRun, error) { return f.ab, nil }

func (f *fakeStore) Purge(context.Context, time.Time) (int64, error) { return 0, nil }

func (f *fakeStore) Stats(context.Context) (telemetry.Stats, error) { return telemetry.Stats{}, nil }

func (f *fakeStore) Close() error { return nil }

// ---- builders shared by the tests ----

var t0 = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

func mkCall(id, sid string, at time.Duration, tool, action, outcome string, latency int64) telemetry.CallEvent {
	return telemetry.CallEvent{
		ID:        id,
		TS:        t0.Add(at),
		LatencyMS: latency,
		Transport: telemetry.TransportStdio,
		Method:    "tools/call",
		Tool:      tool,
		Action:    action,
		Project:   "leankg",
		SessionID: sid,
		Identity:  telemetry.Identity{ClientName: "claude-code", ClientSessionID: sid},
		Outcome:   outcome,
		Rung:      "L1",
		Freshness: "fresh",
	}
}

func mkSession(id string, first, last time.Duration) telemetry.Session {
	return telemetry.Session{
		ID:              id,
		ClientName:      "claude-code",
		ClientSessionID: id,
		Project:         "leankg",
		FirstTS:         t0.Add(first),
		LastTS:          t0.Add(last),
		Correlation:     telemetry.CorrExact,
	}
}

// staticSrc returns a TranscriptSource that always yields turns.
func staticSrc(turns []sessionlink.Turn) TranscriptSource {
	return func(context.Context, telemetry.Session) ([]sessionlink.Turn, error) {
		return turns, nil
	}
}

func fixedNow() func() time.Time { return func() time.Time { return t0.Add(time.Hour) } }
