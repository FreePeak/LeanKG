package sessionlink

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// LinkStats summarizes one Link pass.
type LinkStats struct {
	Considered int `json:"considered"`
	Linked     int `json:"linked"`
	NotFound   int `json:"not_found"`
	Skipped    int `json:"skipped_not_consented"`
	Errors     int `json:"errors"`
}

// Link statuses written to Session.LinkStatus.
const (
	StatusLinked      = "linked"
	StatusNotFound    = "not_found"
	StatusUnsupported = "unsupported_version"
	StatusNotConsent  = "not_consented"
)

const (
	// DefaultOlderThan is how old a call must be before linking (plan DS-10).
	DefaultOlderThan = 2 * time.Minute
	// DefaultEvery is the Loop interval used by `leankg dashboard`.
	DefaultEvery = time.Minute

	batchLimit     = 500
	windowTurns    = 6
	maxWindowBytes = 64 << 10
)

// grantedFn is the consent gate. A variable only so tests can drive it;
// production always uses telemetry.SessionsGranted.
var grantedFn = telemetry.SessionsGranted

// experimental is implemented by adapters that are linked but flagged.
type experimental interface{ Experimental() bool }

// Link joins unlinked calls older than olderThan to their transcripts. home
// is the user's real home (agent stores live there, not under LEANKG_HOME).
// Clients without a sessions grant are never opened.
//
// A call whose transcript is definitively absent (not_found) or unreadable by
// schema (unsupported_version) gets a marker SessionLink with no window and
// zero confidence, so the next pass does not re-read the same first 500 rows.
// A transient error writes nothing and is retried. Calls that are not
// consented are left unlinked, so a later grant can still link them.
func Link(ctx context.Context, st telemetry.Store, cfg telemetry.Config, home string, adapters []Adapter, now time.Time, olderThan time.Duration) (LinkStats, error) {
	var stats LinkStats
	if st == nil {
		return stats, errors.New("sessionlink: nil store")
	}
	calls, err := st.UnlinkedCalls(ctx, now.Add(-olderThan), batchLimit)
	if err != nil {
		return stats, err
	}
	// Adapters are resolved lazily, only for consented calls, so a denied
	// client never sees even a Client() call.
	byClient := map[string]Adapter{}
	resolved := map[string]bool{}
	resolve := func(client string) Adapter {
		if resolved[client] {
			return byClient[client]
		}
		resolved[client] = true
		for _, a := range adapters {
			if a != nil && a.Client() == client {
				byClient[client] = a
				return a
			}
		}
		return nil
	}

	sessions := map[string]*telemetry.Session{}
	statusOf := map[string]string{}
	paths := map[string]string{}
	var links []telemetry.SessionLink

	for _, ev := range calls {
		if ctx.Err() != nil {
			break
		}
		stats.Considered++
		client := ev.ClientName

		// Consent first: a denied client gets no adapter method call at all.
		if !grantedFn(cfg, client) {
			stats.Skipped++
			noteStatus(statusOf, ev.SessionID, StatusNotConsent)
			continue
		}
		a := resolve(client)
		if a == nil {
			stats.NotFound++
			noteStatus(statusOf, ev.SessionID, StatusNotFound)
			links = append(links, marker(ev, now))
			continue
		}

		c := callRef(ev)
		t, conf, err := a.Locate(home, c)
		switch {
		case errors.Is(err, ErrNotFound):
			stats.NotFound++
			noteStatus(statusOf, ev.SessionID, StatusNotFound)
			links = append(links, marker(ev, now))
			continue
		case errors.Is(err, ErrUnsupportedVersion):
			stats.Errors++
			noteStatus(statusOf, ev.SessionID, StatusUnsupported)
			links = append(links, marker(ev, now))
			continue
		case err != nil:
			stats.Errors++
			continue
		}

		w, err := a.Window(t, c, windowTurns)
		if err != nil {
			stats.Errors++
			continue
		}
		raw, err := boundedWindowJSON(w, isExperimental(a))
		if err != nil {
			stats.Errors++
			continue
		}
		links = append(links, telemetry.SessionLink{
			CallID:     ev.ID,
			SessionID:  ev.SessionID,
			Confidence: float64(conf),
			ToolUseID:  toolUseID(w),
			Window:     raw,
			LinkedAt:   now,
		})
		stats.Linked++
		noteStatus(statusOf, ev.SessionID, StatusLinked)
		paths[ev.SessionID] = telemetry.Redact(t.Path)
	}

	if len(links) > 0 {
		if err := st.UpsertLinks(ctx, links); err != nil {
			return stats, err
		}
	}
	var upd []telemetry.Session
	for id, status := range statusOf {
		s := sessionFor(ctx, st, sessions, id)
		if s == nil {
			continue
		}
		s.LinkStatus = status
		if p := paths[id]; p != "" {
			s.TranscriptPath = p
		}
		upd = append(upd, *s)
	}
	if len(upd) > 0 {
		if err := st.UpsertSessions(ctx, upd); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

// Loop runs Link every interval until ctx is done, re-reading the config
// through cfgFn each pass so a revoked grant takes effect immediately.
func Loop(ctx context.Context, st telemetry.Store, home string, cfgFn func() telemetry.Config, adapters []Adapter, every time.Duration) {
	if every <= 0 {
		every = DefaultEvery
	}
	if cfgFn == nil {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// Fail soft: a failed pass is retried on the next tick.
			_, _ = Link(ctx, st, cfgFn(), home, adapters, now, DefaultOlderThan)
		}
	}
}

// callRef converts a ledger call into the adapter-facing CallRef.
func callRef(ev telemetry.CallEvent) CallRef {
	var files []string
	for _, f := range strings.Split(ev.HitFiles, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return CallRef{
		CallID:          ev.ID,
		TS:              ev.TS,
		ClientName:      ev.ClientName,
		ClientSessionID: ev.ClientSessionID,
		Cwd:             ev.Cwd,
		Tool:            ev.Tool,
		Action:          ev.Action,
		ArgsHash:        ev.ArgsHash,
		HitFiles:        files,
	}
}

// marker closes out a call that has no transcript to join.
func marker(ev telemetry.CallEvent, now time.Time) telemetry.SessionLink {
	return telemetry.SessionLink{CallID: ev.ID, SessionID: ev.SessionID, LinkedAt: now}
}

// noteStatus records a session status; a linked call wins over other outcomes.
func noteStatus(m map[string]string, sessionID, status string) {
	if m[sessionID] == StatusLinked && status != StatusLinked {
		return
	}
	m[sessionID] = status
}

func sessionFor(ctx context.Context, st telemetry.Store, cache map[string]*telemetry.Session, id string) *telemetry.Session {
	if s, ok := cache[id]; ok {
		return s
	}
	s, ok, err := st.Session(ctx, id)
	if err != nil || !ok {
		cache[id] = nil
		return nil
	}
	cache[id] = &s
	return &s
}

func isExperimental(a Adapter) bool {
	e, ok := a.(experimental)
	return ok && e.Experimental()
}

func toolUseID(w Window) string {
	if w.Matched == nil {
		return ""
	}
	return w.Matched.ID
}

// boundedWindowJSON encodes, redacts and caps a window. When the encoding
// exceeds maxWindowBytes it drops the furthest turns and then the follow-ups
// until it fits, so the stored JSON stays valid. Only as a last resort is the
// raw text cut.
func boundedWindowJSON(w Window, experimentalFlag bool) (string, error) {
	enc := func(w Window) (string, error) {
		b, err := json.Marshal(w)
		if err != nil {
			return "", err
		}
		if !experimentalFlag {
			return telemetry.Redact(string(b)), nil
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return "", err
		}
		m["experimental"] = true
		b, err = json.Marshal(m)
		if err != nil {
			return "", err
		}
		return telemetry.Redact(string(b)), nil
	}
	s, err := enc(w)
	if err != nil {
		return "", err
	}
	for len(s) > maxWindowBytes {
		switch {
		case len(w.After) > 0:
			w.After = w.After[:len(w.After)-1]
		case len(w.Before) > 0:
			w.Before = w.Before[1:]
		case len(w.FollowUps) > 0:
			w.FollowUps = w.FollowUps[:len(w.FollowUps)-1]
		default:
			return telemetry.Cap(s, maxWindowBytes), nil
		}
		if s, err = enc(w); err != nil {
			return "", err
		}
	}
	return s, nil
}
