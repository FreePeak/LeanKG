package metrics

import (
	"context"
	"sort"

	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

// failureSamples is the number of recent calls shown per failure group.
const failureSamples = 5

// Failures is GET /failures?group=reason|tool|client. A failure is any call
// whose outcome is not ok. An unknown group is reported as reason.
func Failures(ctx context.Context, st telemetry.Store, o Options, group string) (report.Failures, error) {
	g := group
	switch g {
	case "reason", "tool", "client":
	default:
		g = "reason"
	}
	ds, err := load(ctx, st, o)
	if err != nil {
		return report.Failures{}, err
	}
	var fails []telemetry.CallEvent
	for _, c := range ds.calls {
		if isFailure(c.Outcome) {
			fails = append(fails, c)
		}
	}
	sortCallsDesc(fails)
	byKey := map[string][]telemetry.CallEvent{}
	for _, c := range fails {
		k := failureKey(g, c)
		byKey[k] = append(byKey[k], c)
	}
	total := int64(len(fails))
	groups := make([]report.FailureGroup, 0, len(byKey))
	for k, cs := range byKey {
		fg := report.FailureGroup{Key: k, Count: int64(len(cs)), Samples: []report.CallRow{}}
		if total > 0 {
			fg.Share = float64(len(cs)) / float64(total)
		}
		for _, c := range cs {
			if fg.Guidance == "" && c.OutcomeReason != "" {
				fg.Guidance = c.OutcomeReason
			}
		}
		for i := 0; i < len(cs) && i < failureSamples; i++ {
			fg.Samples = append(fg.Samples, callRow(cs[i]))
		}
		groups = append(groups, fg)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Key < groups[j].Key
	})
	return report.Failures{Group: g, Total: total, Groups: groups}, nil
}

func failureKey(group string, c telemetry.CallEvent) string {
	switch group {
	case "tool":
		if c.Action == "" {
			return c.Tool
		}
		return c.Tool + "." + c.Action
	case "client":
		if c.ClientName == "" {
			return "unknown"
		}
		return c.ClientName
	}
	return c.Outcome
}

// sortCallsDesc orders newest first, ties by ID.
func sortCallsDesc(calls []telemetry.CallEvent) {
	sort.SliceStable(calls, func(i, j int) bool {
		if !calls[i].TS.Equal(calls[j].TS) {
			return calls[i].TS.After(calls[j].TS)
		}
		return calls[i].ID < calls[j].ID
	})
}

// Tools is GET /tools: per tool and action, latency percentiles, the rung and
// outcome mixes, the success rate and the estimated tokens saved.
func Tools(ctx context.Context, st telemetry.Store, o Options) (report.Tools, error) {
	ds, err := load(ctx, st, o)
	if err != nil {
		return report.Tools{}, err
	}
	type key struct{ tool, action string }
	by := map[key][]telemetry.CallEvent{}
	for _, c := range ds.calls {
		k := key{c.Tool, c.Action}
		by[k] = append(by[k], c)
	}
	keys := make([]key, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tool != keys[j].tool {
			return keys[i].tool < keys[j].tool
		}
		return keys[i].action < keys[j].action
	})
	out := make([]report.ToolStat, 0, len(keys))
	for _, k := range keys {
		cs := by[k]
		lat := make([]float64, 0, len(cs))
		rungs := make([]string, 0, len(cs))
		for _, c := range cs {
			lat = append(lat, float64(c.LatencyMS))
			r := c.Rung
			if r == "" {
				r = "none"
			}
			rungs = append(rungs, r)
		}
		out = append(out, report.ToolStat{
			Tool:        k.tool,
			Action:      k.action,
			Calls:       int64(len(cs)),
			SuccessRate: successRate(cs),
			P50MS:       percentile(lat, 0.5),
			P95MS:       percentile(lat, 0.95),
			RungMix:     countBy(rungs),
			OutcomeMix:  countBy(outcomesOf(cs)),
			TokensSaved: sumSaved(cs),
		})
	}
	return report.Tools{Tools: out}, nil
}
