package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestLadderRoutesProseToHybrid pins RS-12: after an L1 miss, a prose question
// reaches the fused L3 rung on SQLite (it used to stop at L2 for every
// question that shared one word with any element), while identifier-shaped
// queries keep L1/L2 and an engine with no embedder keeps L2.
func TestLadderRoutesProseToHybrid(t *testing.T) {
	e, _ := newEngine(t)
	var els []store.Element
	for i := range 20 {
		els = append(els, store.Element{
			QualifiedName: fmt.Sprintf("pkg/w%02d.go::RenderWidget%d", i, i), ElementType: "function",
			Name: fmt.Sprintf("RenderWidget%d", i), FilePath: fmt.Sprintf("pkg/w%02d.go", i), Language: "go",
			Content: fmt.Sprintf("// RenderWidget%d draws the widget frame.\nfunc RenderWidget%d() {}", i, i),
		})
	}
	if err := e.st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(64)
	if _, err := embed.Run(context.Background(), e.st, p, "full"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rung := func(q string) (string, string) {
		out, err := e.Query(ctx, QueryRequest{Query: q})
		if err != nil {
			t.Fatal(err)
		}
		r := out["retrieval"].(map[string]any)
		return r["rung"].(string), r["reason"].(string)
	}

	// No embedder: prose stays on the keyword rung.
	if r, _ := rung("draws the widget frame"); r != "L2" {
		t.Fatalf("no embedder: prose answered by %s, want L2", r)
	}

	e.SetEmbedder(QueryEmbedderFromProvider(p))
	if r, reason := rung("draws the widget frame"); r != "L3" || !strings.Contains(reason, "rrf(vector+fts5)") {
		t.Fatalf("prose: %s %q, want L3 rrf(vector+fts5)", r, reason)
	}
	if r, _ := rung("RenderWidget3"); r != "L1" {
		t.Fatalf("identifier: %s, want L1", r)
	}
	if r, _ := rung("RenderWidget frame"); r != "L2" {
		t.Fatalf("code-shaped token: %s, want L2", r)
	}
}

func TestIsProse(t *testing.T) {
	for q, want := range map[string]bool{
		"how does the ladder pick a rung": true,
		"vector similarity":               true,
		"ModelStamp drift":                false,
		"rungSemantic":                    false,
		"store.Backend interface":         false,
		"parse_config file":               false,
		"single":                          false,
		"load vectors from JSON lines":    true, // acronyms are not code
		"construct an RPC client":         true,
	} {
		if got := isProse(q); got != want {
			t.Errorf("isProse(%q) = %v, want %v", q, got, want)
		}
	}
}

// downEmbedder always fails, like a stopped sidecar.
type downEmbedder struct{ QueryEmbedder }

func (downEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return nil, fmt.Errorf("dial tcp 127.0.0.1:1: connection refused")
}

// TestStatusReportsEmbedderOutage pins RS-15: status says the embedder is
// unreachable instead of reporting a plain healthy server while every L3
// query degrades.
func TestStatusReportsEmbedderOutage(t *testing.T) {
	e, _ := newEngine(t)
	e.SetEmbedder(downEmbedder{QueryEmbedderFromProvider(embed.Deterministic(8))})
	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	qe := out["query_embedder"].(map[string]any)
	if qe["reachable"] != false || !strings.Contains(fmt.Sprint(qe["error"]), "connection refused") {
		t.Fatalf("query_embedder = %v, want reachable=false with the error", qe)
	}
}

// innerProvider names the embedded field so it does not shadow the
// interface's own Provider() method.
type innerProvider = embed.Provider

// lenRecorder records the rune length of each embedded text.
type lenRecorder struct {
	innerProvider
	got int
}

func (r *lenRecorder) Embed(ctx context.Context, kind embed.TextKind, texts []string) ([][]float32, error) {
	r.got = len([]rune(texts[0]))
	return r.innerProvider.Embed(ctx, kind, texts)
}

// TestLongQueryIsTruncatedNotDegraded pins RS-15: a query longer than the
// provider's text budget is embedded from its head instead of failing at the
// sidecar (a 962-token query used to degrade L3 to keywords).
func TestLongQueryIsTruncatedNotDegraded(t *testing.T) {
	rec := &lenRecorder{innerProvider: embed.Deterministic(8)}
	q := QueryEmbedderFromProvider(rec)
	if _, err := q.EmbedQuery(context.Background(), strings.Repeat("vector ", 5000)); err != nil {
		t.Fatal(err)
	}
	if limit := embed.TextCap(rec); rec.got > limit {
		t.Fatalf("query sent with %d runes, over the %d budget", rec.got, limit)
	}
}

// TestSemanticConfidenceAgainstNoiseFloor pins the calibrated "no answer"
// signal: L3 hits that do not beat the collection's noise floor are flagged
// low-confidence (and still returned); hits above it are normal; an
// uncalibrated collection carries no flag.
func TestSemanticConfidenceAgainstNoiseFloor(t *testing.T) {
	e, _ := newEngine(t)
	if err := e.st.UpsertElements([]store.Element{{QualifiedName: "a.go::Widget", ElementType: "function", Name: "Widget", FilePath: "a.go", Language: "go", Content: "func Widget() {}"}}); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(16)
	if _, err := embed.Run(context.Background(), e.st, p, "full"); err != nil {
		t.Fatal(err)
	}
	e.SetEmbedder(QueryEmbedderFromProvider(p))
	ask := func() map[string]any {
		out, err := e.Query(context.Background(), QueryRequest{Action: "semantic", Query: "unrelated cooking question"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	stamp := embed.StampOf(p)
	if err := embed.WriteCalibration(e.st, stamp, 0.999); err != nil {
		t.Fatal(err)
	}
	out := ask()
	if out["retrieval"].(map[string]any)["confidence"] != "low" || len(hitsOf(out)) == 0 || !strings.Contains(fmt.Sprint(out["guidance"]), "noise floor") {
		t.Fatalf("below floor: %v", out)
	}
	if err := embed.WriteCalibration(e.st, stamp, -1); err != nil {
		t.Fatal(err)
	}
	if out := ask(); out["retrieval"].(map[string]any)["confidence"] != "normal" {
		t.Fatalf("above floor: %v", out["retrieval"])
	}
	stamp.Revision = "other"
	if err := embed.WriteCalibration(e.st, stamp, 0.999); err != nil { // calibration for another identity
		t.Fatal(err)
	}
	if _, ok := ask()["retrieval"].(map[string]any)["confidence"]; ok {
		t.Fatal("a calibration for a different collection identity was applied")
	}
}
