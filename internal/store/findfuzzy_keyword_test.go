package store

import (
	"context"
	"slices"
	"testing"
)

// keywordArmSearcher is the part of an engine the keyword-arm conformance
// check needs: seed elements, then run the L2 keyword rung.
type keywordArmSearcher interface {
	UpsertElements(ctx context.Context, els []Element) error
	FindFuzzy(ctx context.Context, query string, limit int) ([]FuzzyMatch, Arm, error)
}

// checkFindFuzzyKeywordArm seeds one element set and asserts that FindFuzzy
// returns the keyword hits together with the arm that served them. wantArms
// lists the arm labels this engine may report.
func checkFindFuzzyKeywordArm(t *testing.T, s keywordArmSearcher, wantArms ...Arm) {
	t.Helper()
	if err := s.UpsertElements(context.Background(), []Element{
		{QualifiedName: "main.parseConfig", ElementType: "function", Name: "parseConfig", FilePath: "main.go", Language: "go", Content: "parses the yaml config file"},
		{QualifiedName: "main.unrelated", ElementType: "function", Name: "unrelated", FilePath: "main.go", Language: "go", Content: "does something else"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for _, q := range []string{"config", "parseConfig"} {
		hits, arm, err := s.FindFuzzy(context.Background(), q, 10)
		if err != nil {
			t.Fatalf("FindFuzzy(%q): %v", q, err)
		}
		if len(hits) == 0 || hits[0].Element.Name != "parseConfig" {
			t.Fatalf("FindFuzzy(%q) = %+v; want parseConfig first", q, hits)
		}
		if !slices.Contains(wantArms, arm) {
			t.Fatalf("FindFuzzy(%q) arm = %q; want one of %v", q, arm, wantArms)
		}
	}
	if hits, _, err := s.FindFuzzy(context.Background(), "   ", 10); err != nil || len(hits) != 0 {
		t.Fatalf("FindFuzzy(blank) = %+v, %v; want no hits and no error", hits, err)
	}
}

// SQLite answers the keyword rung with FTS5 bm25 and labels it ArmFTS5.
func TestFindFuzzyReturnsKeywordArmSQLite(t *testing.T) {
	checkFindFuzzyKeywordArm(t, openTestStore(t), ArmFTS5)
}

// PostgreSQL answers with tsvector, trigram or ILIKE depending on what the
// server supports; the arm must be one of those three labels.
func TestFindFuzzyReturnsKeywordArmPostgres(t *testing.T) {
	checkFindFuzzyKeywordArm(t, openPGTest(t), ArmTSVector, ArmTrigram, ArmILIKE)
}
