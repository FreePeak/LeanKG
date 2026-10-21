package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// unreadableStampStore is a store whose STAMP READ fails, and nothing else.
// It embeds a real, fully migrated store, so every other read the L3 rung
// makes (the L2 fallback, the element counts) behaves exactly as it does in
// production; only Stamp() is faulted, with the error the production read
// actually produced ("no such column: text_cap" on a pre-017 store).
type unreadableStampStore struct {
	store.Backend
	err error
}

func (s unreadableStampStore) Stamp(modelID string) (*store.ModelStamp, error) {
	return nil, s.err
}

// TestL3DegradeReasonNamesUnreadableStamp pins that the L3 degrade reason
// tells the operator WHICH of the two happened: the collection is missing,
// or the collection's stamp could not be READ.
//
// The defect: both cases produced the same sentence, "no vector collection
// for model X; degraded from L3" — so a stamp that could not be read (a
// pre-017 store, where the read dies with "no such column: text_cap") was
// reported as a collection that does not exist, and the operator was sent to
// rebuild a collection that was there all along (measured: 2314 vectors in
// the store). A read error must name its cause.
func TestL3DegradeReasonNamesUnreadableStamp(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	// The error a pre-017 store's stamp read produced.
	readErr := errors.New(`no such column: text_cap`)
	broken := unreadableStampStore{Backend: st, err: readErr}
	p := embed.Deterministic(4)
	e := New(broken, nil, QueryEmbedderFromProvider(p))

	out, err := e.Query(context.Background(), QueryRequest{Action: "semantic", Query: "anything"})
	if err != nil {
		t.Fatalf("an unreadable stamp must degrade, not error: %v", err)
	}
	r, ok := out["retrieval"].(map[string]any)
	if !ok {
		t.Fatalf("no retrieval in %v", out)
	}
	if r["rung"] != "L2" {
		t.Fatalf("want L2 degrade, got %v", r)
	}
	reason, _ := r["reason"].(string)
	if strings.Contains(reason, "no vector collection") {
		t.Fatalf("reason must not claim a missing collection: %q", reason)
	}
	if !strings.Contains(reason, "could not be read") {
		t.Fatalf("reason must name the unreadable stamp: %q", reason)
	}
	if !strings.Contains(reason, "text_cap") {
		t.Fatalf("reason must carry the read error: %q", reason)
	}
	if !strings.Contains(reason, "degraded from L3") {
		t.Fatalf("reason must keep the L3 provenance: %q", reason)
	}
}

// TestL3DegradeReasonStillSaysNoVectorCollection pins the OTHER half: a
// collection that genuinely does not exist still says so, in the same words
// as before. The fix separates the two reasons; it does not rewrite the
// missing-collection one.
func TestL3DegradeReasonStillSaysNoVectorCollection(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	// A real, migrated store with NO stamp row for the embedder's model:
	// the collection genuinely does not exist.
	p := embed.Deterministic(4)
	e := New(st, nil, QueryEmbedderFromProvider(p))

	out, err := e.Query(context.Background(), QueryRequest{Action: "semantic", Query: "anything"})
	if err != nil {
		t.Fatalf("a missing collection must degrade, not error: %v", err)
	}
	r, ok := out["retrieval"].(map[string]any)
	if !ok {
		t.Fatalf("no retrieval in %v", out)
	}
	reason, _ := r["reason"].(string)
	if !strings.Contains(reason, "no vector collection for model "+p.ModelID()) {
		t.Fatalf("reason must still say the collection is missing: %q", reason)
	}
	if !strings.Contains(reason, "degraded from L3") {
		t.Fatalf("reason must keep the L3 provenance: %q", reason)
	}
}
