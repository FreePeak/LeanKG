package budget

import (
	"testing"
)

// TestTruncationKeepsAProtectedKeyEvenWhenItAloneExceedsTheBudget pins the
// property that makes a protected key a promise rather than a preference.
//
// The bug, found by wave 18 driving every query action over stdio. The
// protected-key set carries the primary payload so a response "keeps its shape"
// after truncation — `query action=ontology` answers `{"matches": […]}` and
// `matches` is the payload, so it must survive. But the implementation
// truncated every child first and then dropped non-protected keys until the
// object fit, WITHOUT checking whether the protected ones still did:
//
//	{"_token_budget":{"max":4000,"actual":22,"pre_truncation_tokens":17184,
//	                 "truncated":true}}
//
// That is an answer with no payload at all. An agent reading it cannot tell a
// query that matched nothing from a query whose answer was thrown away to fit
// the envelope, and the marker that was supposed to say so is the ONLY thing
// left. On this repository `ontology` produced 673 matches, ~17k tokens of
// them, and the delivered answer was the marker.
//
// The rule a protected key needs: it is protected, so it is never dropped and
// never truncated to nothing. When the protected payload alone exceeds the
// budget, the caller gets the largest prefix that fits plus the marker saying
// exactly what happened — a shortened answer the caller can see is short, not a
// hollow one it cannot.
//
// ponytail: no rebalancing, no per-tool cap, no streaming. The envelope is the
// contract and the marker is the honesty mechanism; making the payload fit is
// the payload's business, and the only thing this layer owes is that what it
// delivers is not empty of content while claiming success.
func TestTruncationKeepsAProtectedKeyEvenWhenItAloneExceedsTheBudget(t *testing.T) {
	// A TYPED slice, which is the shape the engine really returns
	// ([]ontology.Match, not []any) and the shape that defeated truncateValue:
	// it matched neither truncation case, so nothing shrank, the object stayed
	// over budget, and the payload key was deleted whole.
	type match struct {
		ConceptID string `json:"concept_id"`
		QN        string `json:"qn"`
		Via       string `json:"via"`
	}
	big := make([]match, 673)
	for i := range big {
		big[i] = match{ConceptID: "rank", QN: "internal/store/pg_fts.go::FuseRRF", Via: "label"}
	}
	payload := map[string]any{"matches": big}

	out, _ := (TokenBudget{}).Apply(payload, "ontology")
	obj, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("payload identity changed: %T", out)
	}
	kept, present := obj["matches"]
	if !present {
		t.Fatalf("matches was DROPPED although it is the primary payload: %v", obj)
	}
	if kept == nil {
		t.Fatalf("matches was truncated to nothing: a protected key must stay usable")
	}
	list, isList := kept.([]any)
	if !isList || len(list) == 0 {
		t.Fatalf("matches must survive as a non-empty list, got %T %v", kept, kept)
	}
	if len(list) >= len(big) {
		t.Errorf("a 673-item payload under a 4000-token envelope should still be trimmed, got all %d", len(list))
	}
	// And the marker must say the answer was cut, because it was.
	report, hasReport := obj[BudgetMarkerKey].(TokenReport)
	if !hasReport {
		t.Fatalf("truncation must leave its marker: %v", obj)
	}
	if !report.Truncated {
		t.Errorf("the marker must report truncation when the payload was trimmed")
	}
	if report.PreTruncationToken <= report.Actual {
		t.Errorf("the marker must show the payload shrank: pre=%d actual=%d", report.PreTruncationToken, report.Actual)
	}
}

// TestASmallAnswerIsNeverReducedToJustTheMarker is the same property from the
// other side, and the shape wave 18 actually saw: an answer that fits nowhere
// near its envelope must come through untouched, because a small answer that
// gets hollowed is indistinguishable from a failed query.
func TestASmallAnswerIsNeverReducedToJustTheMarker(t *testing.T) {
	payload := map[string]any{
		"matches": []any{map[string]any{"qn": "a.go::A", "concept_id": "rank"}},
	}
	out, stats := (TokenBudget{}).Apply(payload, "ontology")
	obj, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("payload identity changed: %T", out)
	}
	if _, dropped := obj["matches"]; !dropped {
		t.Fatalf("a small answer lost its payload: %v", obj)
	}
	if stats.Truncated {
		t.Errorf("a small answer must not be reported as truncated (pre=%d max=%d)", stats.PreTruncationToken, stats.Max)
	}
	if stats.Actual != stats.PreTruncationToken {
		t.Errorf("an untruncated answer must report itself whole: actual=%d pre=%d", stats.Actual, stats.PreTruncationToken)
	}
}
