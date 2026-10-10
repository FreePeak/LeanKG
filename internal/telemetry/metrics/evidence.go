package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

const (
	// segmentMinCalls is the smallest task segment that counts (DS-18).
	segmentMinCalls = 3
	// controlledMinTrials is the per-arm, per-task floor (FR-ZCP-08 clause 2).
	controlledMinTrials = 3

	observationalNote = "Observational and confounded, not causal: segments that used LeanKG differ from segments that did not in task, repo, model and prompt. Each segment is one user prompt to the next, with at least 3 tool calls. Values are counted before the first edit."
)

// segment is one task segment of a transcript (DS-18, observational).
type segment struct {
	withLeanKG bool
	discovery  float64 // discovery or LeanKG calls before the first edit
	turns      float64 // assistant turns before the first edit
	seconds    float64 // wall time from the prompt to the first edit
	inTokens   float64 // input tokens (incl. cache) of turns before the first edit
}

// Evidence is GET /evidence. The observational panel needs full transcripts
// (src); with a nil src it is empty. The controlled panel is built only from
// valid imported A/B runs, and only when every task has at least 3 valid
// trials per arm.
func Evidence(ctx context.Context, st telemetry.Store, o Options, src TranscriptSource) (report.Evidence, error) {
	ev := report.Evidence{
		Observational:     []report.ArmCompare{},
		ObservationalNote: observationalNote,
		Controlled:        []report.ArmCompare{},
	}
	if src != nil {
		sessions, err := st.Sessions(ctx, o.filter())
		if err != nil {
			return ev, err
		}
		var segs []segment
		for _, s := range sessions {
			turns, err := src(ctx, s)
			if err != nil {
				continue
			}
			segs = append(segs, segmentsOf(turns)...)
		}
		ev.Observational = observationalPanel(segs)
	}

	runs, err := st.ABRuns(ctx)
	if err != nil {
		return ev, err
	}
	var valid []telemetry.ABRun
	for _, r := range runs {
		if r.Valid {
			valid = append(valid, r)
		}
	}
	ev.ControlledRuns = len(valid)
	ok, note := controlledGate(valid)
	ev.ControlledValid = ok
	ev.ControlledNote = note
	if ok {
		ev.Controlled = controlledPanel(valid)
	}
	return ev, nil
}

// segmentsOf splits turns at each user prompt and keeps the segments with at
// least segmentMinCalls tool calls.
func segmentsOf(turns []sessionlink.Turn) []segment {
	var out []segment
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		if s, ok := measureSegment(turns[start:end]); ok {
			out = append(out, s)
		}
	}
	for i, t := range turns {
		if t.Role == "user" && strings.TrimSpace(t.Text) != "" {
			flush(i)
			start = i
		}
	}
	flush(len(turns))
	return out
}

func measureSegment(seg []sessionlink.Turn) (segment, bool) {
	total := 0
	withLK := false
	for _, t := range seg {
		for _, tc := range t.ToolCalls {
			total++
			if tc.IsLeanKG {
				withLK = true
			}
		}
	}
	if total < segmentMinCalls {
		return segment{}, false
	}
	edit := firstEdit(seg)
	s := segment{withLeanKG: withLK}
	for _, t := range seg[:edit] {
		if t.Role == "assistant" {
			s.turns++
		}
		for _, tc := range t.ToolCalls {
			if sessionlink.IsDiscovery(tc.Norm) || tc.IsLeanKG {
				s.discovery++
			}
		}
		s.inTokens += float64(t.InputTokens + t.CacheRead + t.CacheWrite)
	}
	anchor := seg[len(seg)-1].TS
	if edit < len(seg) {
		anchor = seg[edit].TS
	}
	if d := anchor.Sub(seg[0].TS).Seconds(); d > 0 {
		s.seconds = d
	}
	return s, true
}

// firstEdit is the index of the first turn with an edit or write, or len(seg)
// when the segment never edits.
func firstEdit(seg []sessionlink.Turn) int {
	for i, t := range seg {
		for _, tc := range t.ToolCalls {
			if tc.Norm == "edit" || tc.Norm == "write" {
				return i
			}
		}
	}
	return len(seg)
}

func observationalPanel(segs []segment) []report.ArmCompare {
	type arm struct{ discovery, turns, seconds, tokens []float64 }
	var with, without arm
	add := func(a *arm, s segment) {
		a.discovery = append(a.discovery, s.discovery)
		a.turns = append(a.turns, s.turns)
		a.seconds = append(a.seconds, s.seconds)
		a.tokens = append(a.tokens, s.inTokens)
	}
	for _, s := range segs {
		if s.withLeanKG {
			add(&with, s)
		} else {
			add(&without, s)
		}
	}
	mk := func(metric, unit string, w, wo []float64) report.ArmCompare {
		return report.ArmCompare{Metric: metric, Unit: unit, With: dist(w), Without: dist(wo)}
	}
	return []report.ArmCompare{
		mk("discovery_calls_before_edit", "calls", with.discovery, without.discovery),
		mk("turns_before_edit", "turns", with.turns, without.turns),
		mk("wall_time_before_edit", "s", with.seconds, without.seconds),
		mk("input_tokens_before_edit", "tokens", with.tokens, without.tokens),
	}
}

// controlledGate decides whether the A/B runs may be shown. Every task seen
// must have at least controlledMinTrials valid runs in each arm.
func controlledGate(runs []telemetry.ABRun) (bool, string) {
	if len(runs) == 0 {
		return false, "no valid controlled A/B runs imported; run leankg telemetry import-ab <dir>"
	}
	counts := map[string]map[string]int{}
	for _, r := range runs {
		if counts[r.Task] == nil {
			counts[r.Task] = map[string]int{}
		}
		counts[r.Task][r.Arm]++
	}
	tasks := make([]string, 0, len(counts))
	for t := range counts {
		tasks = append(tasks, t)
	}
	sort.Strings(tasks)
	for _, t := range tasks {
		w, wo := counts[t]["with"], counts[t]["without"]
		if w < controlledMinTrials || wo < controlledMinTrials {
			return false, fmt.Sprintf("task %q has %d valid with and %d valid without runs; at least %d per arm are required (benchmark/ab pitfalls checklist)", t, w, wo, controlledMinTrials)
		}
	}
	return true, "measured: controlled A/B with at least 3 valid trials per arm for every task. Arm figures are the median of per-task medians, never pooled raw trials."
}

// controlledPanel compares the two arms on each metric. Each task contributes
// one median per arm; the panel reports the distribution of those medians.
func controlledPanel(runs []telemetry.ABRun) []report.ArmCompare {
	metrics := []struct {
		name, unit string
		value      func(telemetry.ABRun) (float64, bool)
	}{
		{"tokens", "tokens", func(r telemetry.ABRun) (float64, bool) { return float64(r.Tokens), true }},
		{"turns", "turns", func(r telemetry.ABRun) (float64, bool) { return float64(r.Turns), true }},
		{"duration_s", "s", func(r telemetry.ABRun) (float64, bool) { return r.DurationS, true }},
		{"tool_calls", "calls", func(r telemetry.ABRun) (float64, bool) { return float64(r.ToolCalls), true }},
		{"file_reads", "reads", func(r telemetry.ABRun) (float64, bool) { return float64(r.FileReads), true }},
		{"cost_usd", "USD", func(r telemetry.ABRun) (float64, bool) { return r.CostUSD, true }},
		{"judge_score", "score", func(r telemetry.ABRun) (float64, bool) { return r.JudgeScore, r.JudgeScore != 0 }},
	}
	out := make([]report.ArmCompare, 0, len(metrics))
	for _, m := range metrics {
		with := perTaskMedians(runs, "with", m.value)
		without := perTaskMedians(runs, "without", m.value)
		out = append(out, report.ArmCompare{Metric: m.name, Unit: m.unit, With: dist(with), Without: dist(without)})
	}
	return out
}

func perTaskMedians(runs []telemetry.ABRun, arm string, value func(telemetry.ABRun) (float64, bool)) []float64 {
	byTask := map[string][]float64{}
	for _, r := range runs {
		if r.Arm != arm {
			continue
		}
		if v, ok := value(r); ok {
			byTask[r.Task] = append(byTask[r.Task], v)
		}
	}
	out := make([]float64, 0, len(byTask))
	for _, vs := range byTask {
		out = append(out, percentile(vs, 0.5))
	}
	return out
}
