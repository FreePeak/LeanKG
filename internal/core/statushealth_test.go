package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// stampsReadFails is a real store whose emb_stamp listing fails: the
// embeddings layer cannot be read at all. The rest of the store is intact —
// the elements, the vectors, the watermark — so the ONLY wrong fact status
// could publish is the one about the embeddings layer.
type stampsReadFails struct {
	store.Backend
	cause error
}

func (s stampsReadFails) Stamps() ([]store.ModelStamp, error) { return nil, s.cause }

// seededStore opens a migrated store holding one element, one stamped model
// and one vector: a store that really does have embeddings, so a payload that
// claims "no embeddings" is demonstrably wrong.
func seededStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "m.alpha", ElementType: "function", Name: "alpha",
		FilePath: "m.go", Language: "go", Content: "alpha code",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteStamp(store.ModelStamp{
		ModelID: "m-1", Revision: "r1", Dimensions: 4, Distance: "cosine", Provider: "test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertVectors("m-1", []store.VectorRow{{QualifiedName: "m.alpha", Vec: []float32{1, 0, 0, 0}}}); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestStatusUnhealthyWhenEmbeddingsReadFails is the regression for the
// false-health payload: status used to hardcode healthy:true and swallow a
// failed emb_stamp read into an empty embeddings list, so a store whose
// embeddings layer could not be read was reported as healthy AND as having
// no embeddings at all — while it actually held vectors.
func TestStatusUnhealthyWhenEmbeddingsReadFails(t *testing.T) {
	st := seededStore(t)
	cause := errors.New("no such table: emb_stamp")
	e := New(stampsReadFails{Backend: st, cause: cause}, nil, nil)

	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatalf("status must still answer when only the embeddings read fails: %v", err)
	}
	if healthy, _ := out["healthy"].(bool); healthy {
		t.Fatalf("healthy must be derived, not asserted: got %v with a failed embeddings read", out["healthy"])
	}
	// The cause must be named: an operator has to be able to tell WHICH read
	// failed, not just that something did.
	msg, _ := out["embeddings_error"].(string)
	if msg == "" {
		t.Fatal("payload must name the failed embeddings read (embeddings_error)")
	}
	if !strings.Contains(msg, "emb_stamp") {
		t.Fatalf("embeddings_error must name the cause %q, got %q", cause, msg)
	}
	// An empty list caused by a failed read must NOT read as "there are no
	// embeddings configured": the list is null (unknown), not an empty
	// array, so the failure is visible without reading the error field.
	emb, ok := out["embeddings"].([]map[string]any)
	if !ok {
		t.Fatalf("the embeddings field must keep its list type: %T", out["embeddings"])
	}
	if emb != nil {
		t.Fatalf("a failed embeddings read must not publish a list (empty or not): %v", emb)
	}
	// The rest of the payload is still delivered — the failure degrades one
	// field, it does not empty the whole status.
	if out["elements"] != 1 {
		t.Fatalf("the inventory must still be published: %v", out["elements"])
	}
	// On the WIRE the failed read is a null list, not an empty array: an
	// empty array still means "read succeeded, nothing is embedded".
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"embeddings":null`) {
		t.Fatalf("a failed embeddings read must marshal as null, not []: %s", raw)
	}
	if strings.Contains(string(raw), `"embeddings":[]`) {
		t.Fatalf("a failed embeddings read must not marshal as an empty array: %s", raw)
	}
}

// TestStatusHealthyStoreReportsHealthy proves the derivation did not just
// invert a constant: a store whose embeddings layer reads fine is healthy.
func TestStatusHealthyStoreReportsHealthy(t *testing.T) {
	st := seededStore(t)
	e := New(st, nil, nil)

	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if healthy, _ := out["healthy"].(bool); !healthy {
		t.Fatalf("a readable store must report healthy:true, got %v (error %v)", out["healthy"], out["embeddings_error"])
	}
	if _, ok := out["embeddings_error"]; ok {
		t.Fatalf("a healthy payload must not carry embeddings_error: %v", out["embeddings_error"])
	}
	emb, ok := out["embeddings"].([]map[string]any)
	if !ok || len(emb) != 1 {
		t.Fatalf("the stamped model must be published: %v", out["embeddings"])
	}
	if emb[0]["model_id"] != "m-1" || emb[0]["vectors"] != 1 {
		t.Fatalf("stamped model payload: %v", emb[0])
	}
}

// TestStatusHealthyStoreWithoutEmbeddings is the other half of the
// distinction: a store that genuinely has NO embeddings configured reports an
// EMPTY LIST (not null) and no error, while a store whose embeddings read
// FAILED reports null plus embeddings_error.
func TestStatusHealthyStoreWithoutEmbeddings(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	e := New(st, nil, nil)

	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if healthy, _ := out["healthy"].(bool); !healthy {
		t.Fatalf("a store with no embeddings configured is still healthy: %v", out["healthy"])
	}
	emb, ok := out["embeddings"].([]map[string]any)
	if !ok {
		t.Fatalf("embeddings must be a list when the read succeeded: %T", out["embeddings"])
	}
	if emb == nil {
		t.Fatal("a successful read with no stamps must publish an empty list, not null")
	}
	if len(emb) != 0 {
		t.Fatalf("no stamps means an empty list: %v", emb)
	}
	if _, ok := out["embeddings_error"]; ok {
		t.Fatalf("a successful read must not carry embeddings_error: %v", out["embeddings_error"])
	}
}

// TestStatusProviderUnreachableIsNotUnhealthy pins the separation the brief
// demands: an unreachable query provider degrades the L3 rung, it does not
// make the store unreadable, so it must not flip healthy.
func TestStatusProviderUnreachableIsNotUnhealthy(t *testing.T) {
	st := seededStore(t)
	e := New(st, nil, QueryEmbedderFromProvider(failingProvider{inner: embed.Deterministic(4)}))

	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	qe, ok := out["query_embedder"].(map[string]any)
	if !ok {
		t.Fatalf("query_embedder must be published: %v", out["query_embedder"])
	}
	if reachable, _ := qe["reachable"].(bool); reachable {
		t.Fatalf("the provider was made to fail, so it must be unreachable: %v", qe)
	}
	if healthy, _ := out["healthy"].(bool); !healthy {
		t.Fatalf("an unreachable provider must not make the store unhealthy: %v (error %v)", out["healthy"], out["embeddings_error"])
	}
}
