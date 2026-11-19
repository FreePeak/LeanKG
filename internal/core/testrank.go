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

// Doc demotion. A markdown section matches a question in prose as well as the
// code it documents, and bm25 favours the prose, so a question about the code
// was answered by CLAUDE.md or a README chunk. Unless the question names docs,
// a doc hit counts as docDemotion positions lower, on the same window as tests.
// docDemotion is not swept: it is set to testDemotion so the two demotions
// read the same way.
const docDemotion = testDemotion

// mentionsDocs reports whether a query asks about documentation, in which case
// doc hits keep their natural rank.
func mentionsDocs(q string) bool {
	for _, w := range strings.Fields(strings.ToLower(q)) {
		w = strings.Trim(w, ".,;:?!\"'`()")
		switch w {
		case "doc", "docs", "documentation", "documented", "readme", "markdown", "md", "mdx", "changelog", "guide":
			return true
		}
		if strings.HasSuffix(w, ".md") || strings.HasSuffix(w, ".mdx") {
			return true
		}
	}
	return false
}

// isDocHit reports whether a ranked hit is documentation: a markdown section
// (element_type doc) or a markdown file.
func isDocHit(h map[string]any) bool {
	if t, _ := h["element_type"].(string); t == "doc" {
		return true
	}
	fp, _ := h["file_path"].(string)
	return strings.HasSuffix(fp, ".md") || strings.HasSuffix(fp, ".mdx")
}

// demoteTestHits reorders hits so a test-file hit ranks testDemotion places
// lower (when demoteTests is set) and a doc hit ranks docDemotion lower (when
// demoteDocs is set), then trims to limit. A hit can take both. The order is
// otherwise stable.
func demoteTestHits(hits []map[string]any, limit int, demoteTests, demoteDocs bool) []map[string]any {
	type ranked struct {
		h   map[string]any
		pos int
	}
	rs := make([]ranked, len(hits))
	for i, h := range hits {
		pos := i
		if fp, _ := h["file_path"].(string); demoteTests && isTestPath(fp) {
			pos += testDemotion
		}
		if demoteDocs && isDocHit(h) {
			pos += docDemotion
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
