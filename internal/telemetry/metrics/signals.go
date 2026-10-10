package metrics

import (
	"encoding/json"
	"path"
	"strings"
	"unicode"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

// requeryShare is the share of a follow-up's terms that must repeat the
// matched call's terms to count as a re-query (DS-17).
const requeryShare = 0.5

// SignalsFor derives the DS-17 context-use proxies for one linked call from
// its transcript window. They are proxies, not ground truth:
//   - HitsReturned is the number of distinct hit files (or Hits when no file
//     list was captured).
//   - HitsUsed counts hit files that a later read, edit or write touched, or
//     that the assistant named before the next user prompt. Symbols are not
//     matched.
//   - Fallback is set when one of the next fallbackK follow-ups is a discovery
//     tool whose target is not a hit file.
//   - Requery is set when a later LeanKG call shares at least half of its
//     terms with the matched call's args.
//   - For zero_hit and error:* calls, FollowedGuide says whether the next
//     LeanKG call used an action named in outcome_reason, and AbandonedAfter
//     says there was no further LeanKG call before the next prompt.
func SignalsFor(c telemetry.CallEvent, w sessionlink.Window) report.Signals {
	hits := splitFiles(c.HitFiles)
	s := report.Signals{FallbackTools: []string{}}
	s.HitsReturned = len(hits)
	if s.HitsReturned == 0 {
		s.HitsReturned = c.Hits
	}
	for _, h := range hits {
		if hitUsed(h, w) {
			s.HitsUsed++
		}
	}
	if s.HitsReturned > 0 {
		s.UsedPrecision = float64(s.HitsUsed) / float64(s.HitsReturned)
	}

	tools := map[string]bool{}
	for i, tc := range w.FollowUps {
		if i >= fallbackK {
			break
		}
		if sessionlink.IsDiscovery(tc.Norm) && !matchesAny(tc.Target, hits) {
			s.Fallback = true
			tools[tc.Norm] = true
		}
	}
	s.FallbackTools = sortedKeys(tools)

	if w.Matched != nil {
		mt := termSet(w.Matched.Args)
		for _, tc := range w.FollowUps {
			if !tc.IsLeanKG {
				continue
			}
			ft := termSet(tc.Args)
			if len(ft) == 0 {
				continue
			}
			if float64(overlap(mt, ft))/float64(len(ft)) >= requeryShare {
				s.Requery = true
				break
			}
		}
	}

	if c.Outcome == telemetry.OutcomeZeroHit || strings.HasPrefix(c.Outcome, telemetry.OutcomeErrorPrefix) {
		var next *sessionlink.ToolCall
		for i := range w.FollowUps {
			if w.FollowUps[i].IsLeanKG {
				next = &w.FollowUps[i]
				break
			}
		}
		s.AbandonedAfter = next == nil
		if c.OutcomeReason != "" {
			followed := next != nil && namesGuidedAction(c.OutcomeReason, next.Args)
			s.FollowedGuide = &followed
		}
	}
	return s
}

// hitUsed reports whether a hit file was touched by a later read, edit or
// write, or named by the assistant before the next user prompt.
func hitUsed(h string, w sessionlink.Window) bool {
	for _, tc := range w.FollowUps {
		switch tc.Norm {
		case "read", "edit", "write":
			if matchFile(tc.Target, h) {
				return true
			}
		}
	}
	base := path.Base(h)
	for _, t := range w.After {
		if t.Role == "user" {
			break
		}
		if t.Role != "assistant" || t.Text == "" {
			continue
		}
		if strings.Contains(t.Text, h) || (len(base) >= 4 && strings.Contains(t.Text, base)) {
			return true
		}
	}
	return false
}

// namesGuidedAction reports whether the args of a follow-up call carry an
// action or command that outcome_reason names.
func namesGuidedAction(reason, args string) bool {
	var m map[string]any
	if json.Unmarshal([]byte(args), &m) != nil {
		return false
	}
	lreason := strings.ToLower(reason)
	for _, k := range []string{"action", "command"} {
		v, ok := m[k].(string)
		if !ok || len(v) < 3 {
			continue
		}
		if strings.Contains(lreason, strings.ToLower(v)) {
			return true
		}
	}
	return false
}

// termSet is the lower-cased words of the string values in a JSON args blob.
// Non-JSON text is split as-is.
func termSet(args string) map[string]bool {
	var strs []string
	var v any
	if err := json.Unmarshal([]byte(args), &v); err == nil {
		collectStrings(v, &strs)
	} else {
		strs = append(strs, args)
	}
	out := map[string]bool{}
	for _, s := range strs {
		for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) {
			if len(w) >= 2 {
				out[w] = true
			}
		}
	}
	return out
}

func collectStrings(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case []any:
		for _, e := range x {
			collectStrings(e, out)
		}
	case map[string]any:
		for _, e := range x {
			collectStrings(e, out)
		}
	}
}

func overlap(a, b map[string]bool) int {
	n := 0
	for k := range b {
		if a[k] {
			n++
		}
	}
	return n
}
