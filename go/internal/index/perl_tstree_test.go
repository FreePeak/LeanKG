//go:build tstree

package index

import (
	"path/filepath"
	"strconv"
	"testing"
)

// TestExtractFileAsPerlTreeSitter pins the issue-#61 swap end-to-end through
// the indexer's own pipeline: a .pm file whose constructs the previous
// (ganezdragon) Perl grammar could not parse — subroutine attributes, an
// experimental signature, a `method` with a defaulting parameter, a versioned
// package, postfix dereferencing, a heredoc — comes back with grammar-accurate
// spans, which the regex tier cannot produce (it has no end line at all, and
// it reports a package as "class").
func TestExtractFileAsPerlTreeSitter(t *testing.T) {
	fe, err := extractFileAs("lib/Billing/Invoice.pm",
		filepath.Join("..", "tstree", "testdata", "perl", "modern.pm"), "perl")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range fe.elements {
		got[e.name] = e.etype + "@" + strconv.Itoa(e.start) + "-" + strconv.Itoa(e.end)
	}
	want := map[string]string{
		"Billing::Invoice": "type@8-8",
		"strict":           "import@10-10",
		"warnings":         "import@11-11",
		"List::Util":       "import@12-12",
		"experimental":     "import@13-13",
		"_fmt_amount":      "function@17-20",
		"legacy_total":     "function@22-26",
		"render":           "method@28-35",
		"render_all":       "function@37-37",
	}
	for name, w := range want {
		if g, ok := got[name]; !ok {
			t.Errorf("element %q missing from %v", name, got)
		} else if g != w {
			t.Errorf("element %q = %s, want %s", name, g, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("element count = %d, want %d: %v", len(got), len(want), got)
	}
}
