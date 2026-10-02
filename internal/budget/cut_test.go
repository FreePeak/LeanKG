package budget

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTheMarkerSaysWhatWasCut pins the second half of wave 18's finding.
//
// Wave 18 fixed the worst case: a populated answer delivered as nothing but its
// own marker. The marker still says `truncated: true` and nothing about WHAT
// was cut, so a caller cannot finish the job. Measured on this repository:
//
//	query {"action":"search","query":"rank","limit":200}
//	  pre_truncation_tokens 40330  actual 3820
//
// 20,510 tokens are missing. The caller knows something was cut and has no way
// to learn whether `hits` was shortened from 200 to 12 or a key was dropped
// entirely — so it cannot re-query with a smaller limit, because it does not
// know the limit that would fit. The marker's whole purpose is to be acted on,
// and right now it can only be observed.
//
// The fix is one line of shape, not a new mechanism: record which keys the
// truncator shortened or removed, next to the counts it already reports. The
// information is already there — truncateValue knows — so the report is where it
// was being lost.
func TestTheMarkerSaysWhatWasCut(t *testing.T) {
	// One payload key long enough to be trimmed, one short key that must not be.
	big := make([]any, 600)
	for i := range big {
		big[i] = map[string]any{"qn": "internal/store/pg_fts.go::FuseRRF"}
	}
	payload := map[string]any{
		"elements": big,
		"query":    "rank",
		"noise":    strings.Repeat("z", 4000),
	}

	out, stats := (TokenBudget{}).Apply(payload, "get_service_context")
	obj, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("payload identity changed: %T", out)
	}
	if !stats.Truncated {
		t.Fatalf("fixture must actually truncate (pre=%d max=%d)", stats.PreTruncationToken, stats.Max)
	}
	report, has := obj[BudgetMarkerKey].(TokenReport)
	if !has {
		t.Fatalf("truncation must leave its marker: %v", obj)
	}
	if len(report.Cut) == 0 {
		t.Fatalf("a truncated answer must say WHAT was cut; the caller cannot act on `true` alone: %+v", report)
	}
	// It must name the key that was actually trimmed, and not one that survived
	// untouched: naming a survivor would send the caller to re-query for nothing.
	var namedTrimmed bool
	for _, c := range report.Cut {
		if c == "elements" {
			namedTrimmed = true
		}
		if c == "query" {
			t.Errorf("a key that survived must not be reported as cut: %+v", report.Cut)
		}
	}
	if !namedTrimmed {
		t.Errorf("the trimmed key must be named, got %v", report.Cut)
	}
	// And the report has to survive JSON, because that is how a caller sees it.
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "elements") {
		t.Errorf("the cut list must reach the wire: %s", b)
	}
}
