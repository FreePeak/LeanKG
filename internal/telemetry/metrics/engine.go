// Package metrics turns the telemetry ledger into the dashboard API shapes
// (plan v4.15 DS-16..DS-19). The exported signatures are frozen for
// internal/dashboard. Every counterfactual figure is an estimate and says so
// (plan rule 5); only the controlled A/B panel is called measured.
package metrics

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

// Options scopes a report. Zero values mean "all".
type Options struct {
	Since   time.Time
	Client  string
	Project string
	Now     func() time.Time // nil = time.Now
}

// TranscriptSource loads a linked session's full transcript; nil when the
// session's client is not granted (the replay then falls back to a synthetic
// call timeline).
type TranscriptSource func(ctx context.Context, s telemetry.Session) ([]sessionlink.Turn, error)

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) filter() telemetry.CallFilter {
	return telemetry.CallFilter{Since: o.Since, Client: o.Client, Project: o.Project}
}

// dataset is everything the window-level reports read.
type dataset struct {
	calls    []telemetry.CallEvent
	sessions []telemetry.Session
	mem      []telemetry.MemoryEvent
}

// toolCalls loads calls without the MCP handshake rows: the one place the
// metrics read calls, so every report counts the same thing.
func toolCalls(ctx context.Context, st telemetry.Store, f telemetry.CallFilter) ([]telemetry.CallEvent, error) {
	calls, err := st.Calls(ctx, f)
	if err != nil {
		return nil, err
	}
	out := calls[:0]
	for _, c := range calls {
		if !c.IsHandshake() {
			out = append(out, c)
		}
	}
	return out, nil
}

func load(ctx context.Context, st telemetry.Store, o Options) (dataset, error) {
	var ds dataset
	var err error
	if ds.calls, err = toolCalls(ctx, st, o.filter()); err != nil {
		return ds, err
	}
	if ds.sessions, err = st.Sessions(ctx, o.filter()); err != nil {
		return ds, err
	}
	if ds.mem, err = st.MemoryEvents(ctx, o.filter()); err != nil {
		return ds, err
	}
	return ds, nil
}

// Overview is GET /overview: KPI tiles, the local-time daily series, the top
// failures, the client and outcome mixes, and the capture state.
func Overview(ctx context.Context, st telemetry.Store, cfg telemetry.Config, o Options) (report.Overview, error) {
	ds, err := load(ctx, st, o)
	if err != nil {
		return report.Overview{}, err
	}
	calls := ds.calls
	recalls := 0
	for _, m := range ds.mem {
		if m.Verb == "recall" {
			recalls++
		}
	}
	failures := []string{}
	for _, c := range calls {
		if isFailure(c.Outcome) {
			failures = append(failures, c.Outcome)
		}
	}
	clients := make([]string, 0, len(calls))
	for _, c := range calls {
		clients = append(clients, c.ClientName)
	}
	top := countBy(failures)
	if len(top) > 5 {
		top = top[:5]
	}
	return report.Overview{
		Since: o.Since,
		KPIs: []report.KPI{
			kpi("calls", "Calls", float64(len(calls)), ""),
			kpi("success_rate", "Success rate", successRate(calls)*100, "%"),
			kpi("errors", "Errors", float64(countErrors(calls)), ""),
			kpi("sessions", "Sessions", float64(len(ds.sessions)), ""),
			kpi("memory_recalls", "Memory recalls", float64(recalls), ""),
			kpi("tokens_delivered", "Tokens delivered", float64(sumDelivered(calls)), "tokens"),
			tokensSavedKPI(sumSaved(calls)),
		},
		Series:       dailySeries(calls, o),
		TopFailures:  top,
		ByClient:     countBy(clients),
		OutcomeMix:   countBy(outcomesOf(calls)),
		CaptureLevel: string(telemetry.EffectiveLevel(cfg, os.Getenv("LEANKG_TELEMETRY"))),
		SessionsOn:   cfg.Sessions.Enabled && len(cfg.Sessions.Clients) > 0,
	}, nil
}

// dailySeries buckets calls by local day. With a Since bound the series runs
// from that day to today (or the last call), zero-filled, so the chart has no
// gaps. Without one it runs from the first call day to the last.
func dailySeries(calls []telemetry.CallEvent, o Options) []report.DayPoint {
	out := []report.DayPoint{}
	if len(calls) == 0 && o.Since.IsZero() {
		return out
	}
	var first, last time.Time
	if !o.Since.IsZero() {
		first = dayStart(o.Since)
		last = dayStart(o.now())
	}
	for _, c := range calls {
		d := dayStart(c.TS)
		if first.IsZero() || d.Before(first) {
			first = d
		}
		if last.IsZero() || d.After(last) {
			last = d
		}
	}
	index := map[string]int{}
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		index[k] = len(out)
		out = append(out, report.DayPoint{Day: k})
	}
	sessions := make([]map[string]bool, len(out))
	for i := range sessions {
		sessions[i] = map[string]bool{}
	}
	for _, c := range calls {
		i, ok := index[dayKey(c.TS)]
		if !ok {
			continue
		}
		p := &out[i]
		p.Calls++
		if isError(c.Outcome) {
			p.Errors++
		}
		p.TokensSaved += c.TokensSaved
		sessions[i][c.SessionID] = true
	}
	for i := range out {
		out[i].Sessions = int64(len(sessions[i]))
	}
	return out
}

// SessionList is GET /sessions: sessions in the window that match outcome
// (empty = any; "error" = any error), newest first, paged.
func SessionList(ctx context.Context, st telemetry.Store, o Options, outcome string, limit, offset int) (report.SessionList, error) {
	ds, err := load(ctx, st, o)
	if err != nil {
		return report.SessionList{}, err
	}
	callsBy := map[string][]telemetry.CallEvent{}
	for _, c := range ds.calls {
		callsBy[c.SessionID] = append(callsBy[c.SessionID], c)
	}
	recallsBy := recallCounts(ds.mem)
	sessions := sessionsWithCalls(ds.sessions, callsBy)

	rows := []report.SessionSummary{}
	for _, s := range sessions {
		cs := callsBy[s.ID]
		if outcome != "" && !anyOutcome(cs, outcome) {
			continue
		}
		rows = append(rows, summarize(s, cs, recallsBy[s.ID]))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].LastTS.Equal(rows[j].LastTS) {
			return rows[i].LastTS.After(rows[j].LastTS)
		}
		return rows[i].ID < rows[j].ID
	})
	return report.SessionList{Total: len(rows), Sessions: paginate(rows, offset, limit)}, nil
}

// sessionsWithCalls returns the session rows plus a reconstructed row for any
// session that has calls but no row yet (the row write may lag the call write).
func sessionsWithCalls(rows []telemetry.Session, callsBy map[string][]telemetry.CallEvent) []telemetry.Session {
	known := map[string]bool{}
	out := make([]telemetry.Session, 0, len(rows))
	for _, s := range rows {
		known[s.ID] = true
		out = append(out, s)
	}
	for id, cs := range callsBy {
		if known[id] || len(cs) == 0 {
			continue
		}
		s := telemetry.Session{
			ID:              id,
			ClientName:      cs[0].ClientName,
			ClientSessionID: cs[0].ClientSessionID,
			Project:         cs[0].Project,
			Cwd:             cs[0].Cwd,
			Correlation:     cs[0].Correlation,
		}
		for _, c := range cs {
			if s.FirstTS.IsZero() || c.TS.Before(s.FirstTS) {
				s.FirstTS = c.TS
			}
			if c.TS.After(s.LastTS) {
				s.LastTS = c.TS
			}
		}
		out = append(out, s)
	}
	return out
}

func recallCounts(mem []telemetry.MemoryEvent) map[string]int64 {
	out := map[string]int64{}
	for _, m := range mem {
		if m.Verb == "recall" {
			out[m.SessionID]++
		}
	}
	return out
}

func anyOutcome(calls []telemetry.CallEvent, want string) bool {
	for _, c := range calls {
		if matchOutcome(c.Outcome, want) {
			return true
		}
	}
	return false
}

func summarize(s telemetry.Session, calls []telemetry.CallEvent, recalls int64) report.SessionSummary {
	dur := s.LastTS.Sub(s.FirstTS).Seconds()
	if dur < 0 {
		dur = 0
	}
	return report.SessionSummary{
		ID:              s.ID,
		ClientName:      s.ClientName,
		ClientSessionID: s.ClientSessionID,
		Project:         s.Project,
		FirstTS:         s.FirstTS,
		LastTS:          s.LastTS,
		DurationS:       dur,
		Calls:           int64(len(calls)),
		Errors:          countErrors(calls),
		SuccessRate:     successRate(calls),
		TokensSaved:     sumSaved(calls),
		OutcomeMix:      countBy(outcomesOf(calls)),
		Correlation:     s.Correlation,
		LinkStatus:      s.LinkStatus,
		MemoryRecalls:   recalls,
	}
}

// SessionDetail is GET /sessions/{id}. ok is false when the session is unknown.
func SessionDetail(ctx context.Context, st telemetry.Store, id string) (report.SessionDetail, bool, error) {
	s, ok, err := st.Session(ctx, id)
	if err != nil || !ok {
		return report.SessionDetail{}, false, err
	}
	calls, err := toolCalls(ctx, st, telemetry.CallFilter{SessionID: id})
	if err != nil {
		return report.SessionDetail{}, false, err
	}
	sortCalls(calls)
	links, err := st.Links(ctx, id)
	if err != nil {
		return report.SessionDetail{}, false, err
	}
	mem, err := st.MemoryEvents(ctx, telemetry.CallFilter{SessionID: id})
	if err != nil {
		return report.SessionDetail{}, false, err
	}
	linkBy := linksByCall(links)
	rows := make([]report.CallRow, 0, len(calls))
	for _, c := range calls {
		rows = append(rows, rowWithSignals(c, linkBy))
	}
	memRows := make([]report.MemoryRow, 0, len(mem))
	sort.SliceStable(mem, func(i, j int) bool { return mem[i].TS.Before(mem[j].TS) })
	for _, m := range mem {
		memRows = append(memRows, memoryRow(m))
	}
	return report.SessionDetail{
		Session:  summarize(s, calls, recallCounts(mem)[id]),
		KPIs:     sessionKPIs(calls),
		Calls:    rows,
		Memory:   memRows,
		Failures: failureCounts(calls),
		Linked:   s.LinkStatus == "linked" || len(links) > 0,
	}, true, nil
}

func sessionKPIs(calls []telemetry.CallEvent) []report.KPI {
	lat := make([]float64, 0, len(calls))
	degraded := 0
	for _, c := range calls {
		lat = append(lat, float64(c.LatencyMS))
		switch c.Outcome {
		case telemetry.OutcomeCold, telemetry.OutcomeStale, telemetry.OutcomeDegraded:
			degraded++
		}
	}
	share := 0.0
	if len(calls) > 0 {
		share = float64(degraded) / float64(len(calls)) * 100
	}
	k := []report.KPI{
		kpi("calls", "Calls", float64(len(calls)), ""),
		kpi("success_rate", "Success rate", successRate(calls)*100, "%"),
		kpi("p50_latency", "p50 latency", percentile(lat, 0.5), "ms"),
		kpi("p95_latency", "p95 latency", percentile(lat, 0.95), "ms"),
		kpi("tokens_delivered", "Tokens delivered", float64(sumDelivered(calls)), "tokens"),
		tokensSavedKPI(sumSaved(calls)),
		kpi("degraded_share", "Cold, stale or degraded", share, "%"),
	}
	return k
}

// failureCounts ranks the non-ok outcomes of calls, most frequent first.
func failureCounts(calls []telemetry.CallEvent) []report.Count {
	var keys []string
	for _, c := range calls {
		if isFailure(c.Outcome) {
			keys = append(keys, c.Outcome)
		}
	}
	return countBy(keys)
}

func sortCalls(calls []telemetry.CallEvent) {
	sort.SliceStable(calls, func(i, j int) bool {
		if !calls[i].TS.Equal(calls[j].TS) {
			return calls[i].TS.Before(calls[j].TS)
		}
		return calls[i].ID < calls[j].ID
	})
}

func linksByCall(links []telemetry.SessionLink) map[string]telemetry.SessionLink {
	out := make(map[string]telemetry.SessionLink, len(links))
	for _, l := range links {
		out[l.CallID] = l
	}
	return out
}

// rowWithSignals builds the call row and attaches DS-17 signals when the call
// has a readable window.
func rowWithSignals(c telemetry.CallEvent, linkBy map[string]telemetry.SessionLink) report.CallRow {
	row := callRow(c)
	if l, ok := linkBy[c.ID]; ok {
		if w, ok := decodeWindow(l.Window); ok {
			sig := SignalsFor(c, w)
			row.Signals = &sig
		}
	}
	return row
}

func decodeWindow(raw string) (sessionlink.Window, bool) {
	var w sessionlink.Window
	if raw == "" || json.Unmarshal([]byte(raw), &w) != nil {
		return w, false
	}
	return w, true
}

func callRow(c telemetry.CallEvent) report.CallRow {
	return report.CallRow{
		ID:             c.ID,
		TS:             c.TS,
		LatencyMS:      c.LatencyMS,
		Transport:      c.Transport,
		Tool:           c.Tool,
		Action:         c.Action,
		Command:        c.Command,
		Outcome:        c.Outcome,
		OutcomeReason:  c.OutcomeReason,
		ErrorCode:      c.ErrorCode,
		Rung:           c.Rung,
		Confidence:     c.Confidence,
		Freshness:      c.Freshness,
		Hits:           c.Hits,
		OutTokens:      c.OutTokensPost,
		BaselineTokens: c.BaselineTokens,
		BaselineMethod: c.BaselineMethod,
		TokensSaved:    c.TokensSaved,
		Args:           c.ArgsRedacted,
		Body:           c.BodyRedacted,
	}
}

func memoryRow(m telemetry.MemoryEvent) report.MemoryRow {
	return report.MemoryRow{
		ID:        m.ID,
		TS:        m.TS,
		Verb:      m.Verb,
		Transport: m.Transport,
		Banks:     m.Banks,
		Returned:  m.Returned,
		Written:   m.Written,
		Skipped:   m.Skipped,
		Replaced:  m.Replaced,
		Deleted:   m.Deleted,
		AgeMaxS:   m.AgeMaxS,
		LatencyMS: m.LatencyMS,
		Error:     m.Error,
	}
}

// CallDetail is GET /calls/{id}. ok is false when the call is unknown.
func CallDetail(ctx context.Context, st telemetry.Store, id string) (report.CallRow, bool, error) {
	c, ok, err := st.Call(ctx, id)
	if err != nil || !ok {
		return report.CallRow{}, false, err
	}
	links, err := st.Links(ctx, c.SessionID)
	if err != nil {
		return report.CallRow{}, false, err
	}
	return rowWithSignals(c, linksByCall(links)), true, nil
}
