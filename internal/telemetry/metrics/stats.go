package metrics

import (
	"math"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

// savedMethod labels every tokens-saved figure: it is a counterfactual
// estimate, never a measurement (plan rule 5).
const savedMethod = "estimate: baseline minus delivered tokens; baseline is the whole-file token count of the hit files (file_read) or the SLOC rule (sloc); tokens are bytes/4"

// percentile returns the p-quantile (0..1) of xs with linear interpolation
// between the two nearest ranks. Empty input yields 0.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	v := append([]float64(nil), xs...)
	sort.Float64s(v)
	if p <= 0 {
		return v[0]
	}
	if p >= 1 {
		return v[len(v)-1]
	}
	rank := p * float64(len(v)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	return v[lo] + (v[hi]-v[lo])*(rank-float64(lo))
}

// dist summarises xs as N, median and inter-quartile range. N=0 yields zeros.
func dist(xs []float64) report.Dist {
	if len(xs) == 0 {
		return report.Dist{}
	}
	return report.Dist{
		N:      len(xs),
		Median: percentile(xs, 0.5),
		P25:    percentile(xs, 0.25),
		P75:    percentile(xs, 0.75),
	}
}

// isError is the error family used for error rates: error:*, refused, timeout.
func isError(outcome string) bool {
	return strings.HasPrefix(outcome, telemetry.OutcomeErrorPrefix) ||
		outcome == telemetry.OutcomeRefused || outcome == telemetry.OutcomeTimeout
}

// isFailure is any call that did not return a usable answer. Low-confidence
// answers still count as ok.
func isFailure(outcome string) bool {
	return outcome != telemetry.OutcomeOK && outcome != telemetry.OutcomeLowConf
}

// matchOutcome matches a single outcome against a filter value. The filter
// "error" selects the whole error family.
func matchOutcome(outcome, want string) bool {
	if want == "error" {
		return isError(outcome)
	}
	return outcome == want
}

// successRate is the share of calls that were not errors, in 0..1. No calls
// yields 0.
func successRate(calls []telemetry.CallEvent) float64 {
	if len(calls) == 0 {
		return 0
	}
	errs := 0
	for _, c := range calls {
		if isError(c.Outcome) {
			errs++
		}
	}
	return 1 - float64(errs)/float64(len(calls))
}

func countErrors(calls []telemetry.CallEvent) int64 {
	var n int64
	for _, c := range calls {
		if isError(c.Outcome) {
			n++
		}
	}
	return n
}

func sumSaved(calls []telemetry.CallEvent) int64 {
	var n int64
	for _, c := range calls {
		n += c.TokensSaved
	}
	return n
}

func sumDelivered(calls []telemetry.CallEvent) int64 {
	var n int64
	for _, c := range calls {
		n += c.OutTokensPost
	}
	return n
}

// countBy counts keys and returns them ranked by count (desc), then key (asc).
// The result is never nil.
func countBy(keys []string) []report.Count {
	m := map[string]int64{}
	for _, k := range keys {
		m[k]++
	}
	out := make([]report.Count, 0, len(m))
	for k, n := range m {
		out = append(out, report.Count{Key: k, Count: n})
	}
	sortCounts(out)
	return out
}

func sortCounts(cs []report.Count) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Count != cs[j].Count {
			return cs[i].Count > cs[j].Count
		}
		return cs[i].Key < cs[j].Key
	})
}

func outcomesOf(calls []telemetry.CallEvent) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Outcome)
	}
	return out
}

func kpi(key, label string, value float64, unit string) report.KPI {
	return report.KPI{Key: key, Label: label, Value: value, Unit: unit}
}

func tokensSavedKPI(saved int64) report.KPI {
	return report.KPI{
		Key: "tokens_saved", Label: "Tokens saved (est.)", Value: float64(saved),
		Unit: "tokens", Estimate: true, Method: savedMethod,
	}
}

// splitFiles splits a newline-joined file list, dropping blanks and
// duplicates while keeping first-seen order.
func splitFiles(joined string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range strings.Split(joined, "\n") {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// matchFile reports whether a tool target names the same file as a hit: equal
// after cleaning, one path a suffix of the other, or the same basename.
func matchFile(target, file string) bool {
	t := strings.TrimSpace(target)
	f := strings.TrimSpace(file)
	if t == "" || f == "" {
		return false
	}
	t = filepath.ToSlash(t)
	f = filepath.ToSlash(f)
	if t == f || strings.HasSuffix(t, "/"+f) || strings.HasSuffix(f, "/"+t) {
		return true
	}
	return path.Base(t) == path.Base(f)
}

func matchesAny(target string, files []string) bool {
	for _, f := range files {
		if matchFile(target, f) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// dayKey is the YYYY-MM-DD of t in local time.
func dayKey(t time.Time) string { return t.In(time.Local).Format("2006-01-02") }

// dayStart is local midnight of t's local day.
func dayStart(t time.Time) time.Time {
	l := t.In(time.Local)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.Local)
}

// paginate returns rows[offset:offset+limit] with limit<=0 meaning no limit.
// The result is never nil.
func paginate[T any](rows []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(rows) {
		return []T{}
	}
	end := len(rows)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return append([]T{}, rows[offset:end]...)
}
