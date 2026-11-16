package core

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
)

func seedBudgetHits(t *testing.T, e *Engine) {
	t.Helper()
	var els []store.Element
	for i := range 40 {
		els = append(els, store.Element{
			QualifiedName: fmt.Sprintf("pkg/f%02d.go::Widget", i), ElementType: "function",
			Name: "Widget", FilePath: fmt.Sprintf("pkg/f%02d.go", i), Language: "go",
			Content: "func Widget() { /* widget pipeline renders the frame */ }",
		})
	}
	if err := e.st.UpsertElements(context.Background(), els); err != nil {
		t.Fatal(err)
	}
}

// TestQueryAnswerHonoursTokenBudgetArg: args.token_budget overrides the table
// cap, the marker it produces carries that cap, and the raw engine answer
// stays unmarked (capture reads it).
func TestQueryAnswerHonoursTokenBudgetArg(t *testing.T) {
	e, _ := newEngine(t)
	seedBudgetHits(t, e)
	for _, override := range []any{float64(60), "60", int64(60)} {
		ans, err := e.QueryAnswer(context.Background(), QueryRequest{
			Action: "exact", Query: "Widget", Args: map[string]any{"token_budget": override},
		})
		if err != nil {
			t.Fatalf("%v: %v", override, err)
		}
		rep, ok := ans.Body.(map[string]any)[budget.BudgetMarkerKey].(budget.TokenReport)
		if !ok || !rep.Truncated || rep.Max != 60 {
			t.Fatalf("token_budget %v: marker %+v (ok=%v), want truncated at max 60", override, rep, ok)
		}
		if _, has := ans.Raw[budget.BudgetMarkerKey]; has {
			t.Fatalf("token_budget %v: the raw answer carries the marker", override)
		}
		if !ans.Stats.Truncated || ans.Stats.Max != 60 {
			t.Fatalf("token_budget %v: stats %+v", override, ans.Stats)
		}
	}
}

// TestQueryAnswerDefaultIsTheTableCap: without the argument the cap is the
// budget table's (exact: 4000), and an answer under it is the engine answer
// untouched.
func TestQueryAnswerDefaultIsTheTableCap(t *testing.T) {
	e, _ := newEngine(t)
	seedBudgetHits(t, e)
	ans, err := e.QueryAnswer(context.Background(), QueryRequest{Action: "exact", Query: "Widget"})
	if err != nil {
		t.Fatal(err)
	}
	if ans.Stats.Max != (budget.TokenBudget{}).MaxTokensForTool("exact") || ans.Stats.Truncated {
		t.Fatalf("stats %+v, want the table cap and no truncation", ans.Stats)
	}
	if !reflect.DeepEqual(ans.Body, any(ans.Raw)) {
		t.Fatal("an answer under its cap must be the engine answer, unchanged")
	}
}

// TestQueryAnswerRefusesBadTokenBudget: a budget that is not a positive whole
// number is a validation error on every transport (errs.InvalidParam).
func TestQueryAnswerRefusesBadTokenBudget(t *testing.T) {
	e, _ := newEngine(t)
	for _, bad := range []any{"x", float64(0), float64(-3), 2.5, true, ""} {
		_, err := e.QueryAnswer(context.Background(), QueryRequest{
			Action: "exact", Query: "Widget", Args: map[string]any{"token_budget": bad},
		})
		coded, ok := err.(*errs.Error)
		if !ok || coded.Code() != errs.InvalidParam.Code {
			t.Fatalf("token_budget %#v: err = %v, want %s", bad, err, errs.InvalidParam.Code)
		}
	}
}
