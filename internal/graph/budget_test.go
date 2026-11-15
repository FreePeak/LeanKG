package graph

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// openGraph builds a migrated store holding the given element QNs and calls
// edges (rel_type "calls").
func openGraph(t *testing.T, qns []string, calls [][2]string) store.Backend {
	t.Helper()
	s, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(t.TempDir(), ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	els := make([]store.Element, 0, len(qns))
	for _, qn := range qns {
		els = append(els, store.Element{QualifiedName: qn, Name: qn, ElementType: "func", FilePath: qn + ".go"})
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	rels := make([]store.Relationship, 0, len(calls))
	for _, c := range calls {
		rels = append(rels, store.Relationship{Source: c[0], Target: c[1], RelType: "calls", Confidence: 0.9})
	}
	if len(rels) > 0 {
		if err := s.UpsertRelationships(rels); err != nil {
			t.Fatalf("upsert relationships: %v", err)
		}
	}
	return s
}

// hubGraph: callers c00000..c(n-1) all call the hub "h".
func hubGraph(t *testing.T, callers int) store.Backend {
	t.Helper()
	qns := []string{"h"}
	calls := make([][2]string, 0, callers)
	for i := range callers {
		qn := fmt.Sprintf("c%05d", i)
		qns = append(qns, qn)
		calls = append(calls, [2]string{qn, "h"})
	}
	return openGraph(t, qns, calls)
}

// chainGraph: n0 <- n1 <- ... <- n(length-1); each node calls the one before it.
func chainGraph(t *testing.T, length int) store.Backend {
	t.Helper()
	qns := make([]string, 0, length)
	calls := make([][2]string, 0, length)
	for i := range length {
		qns = append(qns, fmt.Sprintf("n%03d", i))
		if i > 0 {
			calls = append(calls, [2]string{fmt.Sprintf("n%03d", i), fmt.Sprintf("n%03d", i-1)})
		}
	}
	return openGraph(t, qns, calls)
}

// readCounter cancels ctx on the after-th traversal read and counts every
// read, so a test can see how far the walk ran past the cancellation.
type readCounter struct {
	store.Backend
	cancel context.CancelFunc
	after  int
	reads  int
}

func (r *readCounter) read() {
	r.reads++
	if r.reads == r.after {
		r.cancel()
	}
}

func (r *readCounter) Incoming(qn string) ([]store.Relationship, error) {
	r.read()
	return r.Backend.Incoming(qn)
}

func (r *readCounter) Outgoing(qn string) ([]store.Relationship, error) {
	r.read()
	return r.Backend.Outgoing(qn)
}

func TestImpactTruncatesAtNodeBudget(t *testing.T) {
	st := hubGraph(t, NodeBudget+1) // seed plus 5001 dependents: one over budget

	hits, b, err := Impact(context.Background(), st, "h", 1)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if len(hits) != NodeBudget {
		t.Fatalf("len(hits) = %d, want the budget %d", len(hits), NodeBudget)
	}
	if !b.Truncated {
		t.Fatalf("Bounds.Truncated = false, want true beyond the budget")
	}
	if b.Depth != 1 {
		t.Fatalf("Bounds.Depth = %d, want the effective depth 1", b.Depth)
	}
}

func TestImpactAtExactBudgetIsComplete(t *testing.T) {
	st := hubGraph(t, NodeBudget-1) // seed plus 4999 dependents: exactly the budget

	hits, b, err := Impact(context.Background(), st, "h", 1)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if len(hits) != NodeBudget || b.Truncated {
		t.Fatalf("len(hits)=%d truncated=%v, want %d and false", len(hits), b.Truncated, NodeBudget)
	}
}

func TestImpactReportsClampedDepth(t *testing.T) {
	st := hubGraph(t, 2)
	_, b, err := Impact(context.Background(), st, "h", 99)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if b.Depth != MaxDepth {
		t.Fatalf("Bounds.Depth = %d, want the clamped depth %d", b.Depth, MaxDepth)
	}
}

func TestImpactHonoursCancelledCtx(t *testing.T) {
	st := chainGraph(t, 200)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counted := &readCounter{Backend: st, cancel: cancel, after: 3}

	_, _, err := Impact(ctx, counted, "n000", 150)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Impact err = %v, want context.Canceled", err)
	}
	// One read per node. The cancel lands on read 3 and the loop checks ctx
	// before the next node, so at most one further read is allowed.
	if counted.reads > 4 {
		t.Fatalf("reads = %d after cancelling on read 3: the BFS kept walking", counted.reads)
	}
}

func TestShortestPathHonoursCancelledCtx(t *testing.T) {
	st := chainGraph(t, 200)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counted := &readCounter{Backend: st, cancel: cancel, after: 3}

	_, _, err := ShortestPath(ctx, counted, "n000", "n199", 150)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ShortestPath err = %v, want context.Canceled", err)
	}
	// Two reads per node (outgoing, incoming). The cancel lands on read 3, so
	// the walk may finish that node's second read and no more.
	if counted.reads > 4 {
		t.Fatalf("reads = %d after cancelling on read 3: the BFS kept walking", counted.reads)
	}
}

func TestCallersHonourCancelledCtx(t *testing.T) {
	st := chainGraph(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Callers(ctx, st, "n001"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Callers with a cancelled ctx: err = %v, want context.Canceled", err)
	}
}

func TestShortestPathTruncatesAtNodeBudget(t *testing.T) {
	// src calls NodeBudget+1 fan-out nodes; the target is reachable only
	// through the last one, so the answer lies beyond the budget.
	qns := []string{"src", "t"}
	calls := make([][2]string, 0, NodeBudget+2)
	for i := range NodeBudget + 1 {
		fan := fmt.Sprintf("f%05d", i)
		qns = append(qns, fan)
		calls = append(calls, [2]string{"src", fan})
	}
	last := fmt.Sprintf("f%05d", NodeBudget)
	calls = append(calls, [2]string{last, "t"})
	st := openGraph(t, qns, calls)

	path, b, err := ShortestPath(context.Background(), st, "src", "t", 5)
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}
	if path != nil {
		t.Fatalf("path = %v, want nil: the target lies beyond the budget", path)
	}
	if !b.Truncated {
		t.Fatalf("Bounds.Truncated = false, want true: the answer is unknown, not unreachable")
	}
}
