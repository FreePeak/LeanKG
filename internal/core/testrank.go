package core

import (
	"regexp"
	"sort"
	"strings"
)

// Test-file demotion. Descriptive test names (TestDockEndsWithMCPThenTrajectory)
// match a natural-language question word for word once identifiers are
// split (RS-13), so a "where is X" question kept answering with the test that
// pins X instead of X. Unless the question is about tests, a hit from a test
// file counts as testDemotion positions lower; ranking runs over twice the
// requested window so implementations below the cut can move up. Tests are
// never removed.

// testDemotion is how many positions a test-file hit is pushed down. Swept
// over 0/2/3/5 on two repositories (xdev, onegw; 120 commit subjects each,
// scored both on implementation files only and on every file the commit
// changed): 2 is the only value that beats the pre-v4.14 ladder on both
// scores in both repositories; 3 and 5 lose too much of the all-files score.
const testDemotion = 2

var testPathRe = regexp.MustCompile(`(_test\.go$|\.(test|spec)\.[cm]?[jt]sx?$|(^|/)(tests?|__tests__|spec)/|(^|/)test_[^/]*\.py$|_test\.py$|Tests?\.(java|kt|cs|swift)$|_spec\.rb$)`)

// isTestPath reports whether a file path is test code.
func isTestPath(p string) bool { return testPathRe.MatchString(p) }

// mentionsTests reports whether a query asks about tests, in which case test
// files keep their natural rank.
func mentionsTests(q string) bool {
	for _, w := range strings.Fields(strings.ToLower(q)) {
		w = strings.Trim(w, ".,;:?!\"'`()")
		switch w {
		case "test", "tests", "testing", "tested", "spec", "specs", "fixture", "fixtures", "assert", "assertion":
			return true
		}
		if strings.HasPrefix(w, "test") && len(w) > 4 && strings.ToUpper(w[:1]) == "T" {
			return true
		}
	}
	return strings.Contains(q, "Test")
}

// demotesTests reports whether an action's hits are ranked search results.
func demotesTests(action string) bool {
	switch action {
	case "", "search", "fuzzy", "semantic":
		return true
	}
	return false
}

// demoteTestHits reorders hits so a test-file hit ranks testDemotion places
// lower, then trims to limit. The order is otherwise stable.
func demoteTestHits(hits []map[string]any, limit int) []map[string]any {
	type ranked struct {
		h   map[string]any
		pos int
	}
	rs := make([]ranked, len(hits))
	for i, h := range hits {
		pos := i
		if fp, _ := h["file_path"].(string); isTestPath(fp) {
			pos += testDemotion
		}
		rs[i] = ranked{h, pos}
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].pos < rs[j].pos })
	out := make([]map[string]any, 0, min(limit, len(rs)))
	for _, r := range rs {
		if len(out) == limit {
			break
		}
		out = append(out, r.h)
	}
	return out
}
