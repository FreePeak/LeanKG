package core

// CX-45: a store read that fails is an error for the caller, never an answer.
// Each fake is a real seeded store with one read overridden to fail.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

type elementCountFails struct {
	store.Backend
	cause error
}

func (f elementCountFails) ElementCount() (int, error) { return 0, f.cause }

type findExactFails struct {
	store.Backend
	cause error
}

func (f findExactFails) FindExact(string) ([]store.Element, error) { return nil, f.cause }

type lastEmbedRunFails struct {
	store.Backend
	cause error
}

func (f lastEmbedRunFails) LastEmbedRunAny() (*store.EmbedRun, error) { return nil, f.cause }

type spaceFails struct {
	store.Backend
	cause error
}

func (f spaceFails) Space() (store.SpaceReport, error) { return store.SpaceReport{}, f.cause }

// A store that cannot count its elements must fail the query, not answer
// "no elements indexed" (the L0 guidance tells the caller to index a repo).
func TestQuerySurfacesElementCountError(t *testing.T) {
	cause := errors.New("element count unreadable")
	e := New(elementCountFails{Backend: seededStore(t), cause: cause}, Options{})
	out, err := e.Query(context.Background(), QueryRequest{Query: "Alpha"})
	if !errors.Is(err, cause) {
		t.Fatalf("Query err = %v (answer %v), want the store error", err, out)
	}
}

// A failing L1 read ends the ladder with the error; it is not a miss that falls
// through to the next rung.
func TestLadderSurfacesExactRungError(t *testing.T) {
	cause := errors.New("exact lookup unreadable")
	e := New(findExactFails{Backend: seededStore(t), cause: cause}, Options{})
	out, err := e.Query(context.Background(), QueryRequest{Query: "Alpha"})
	if !errors.Is(err, cause) {
		t.Fatalf("Query err = %v (answer %v), want the store error", err, out)
	}
}

// status names a failed last-embed-run read and keeps the rest of the payload.
func TestStatusNamesLastEmbedRunReadError(t *testing.T) {
	cause := errors.New("embed runs unreadable")
	e := New(lastEmbedRunFails{Backend: seededStore(t), cause: cause}, Options{})
	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["last_embed_run"]; ok {
		t.Fatal("a failed read must not publish a last_embed_run value")
	}
	if got, _ := out["last_embed_run_error"].(string); !strings.Contains(got, cause.Error()) {
		t.Fatalf("last_embed_run_error = %q, want it to name %q", got, cause)
	}
}

// status names a failed space read the same way.
func TestStatusNamesSpaceReadError(t *testing.T) {
	cause := errors.New("pragma unreadable")
	e := New(spaceFails{Backend: seededStore(t), cause: cause}, Options{})
	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["space"]; ok {
		t.Fatal("a failed read must not publish a space value")
	}
	if got, _ := out["space_error"].(string); !strings.Contains(got, cause.Error()) {
		t.Fatalf("space_error = %q, want it to name %q", got, cause)
	}
}

// endLookupFailsAfterWalk reads normally until the walk has concluded an end is
// unknown (Incoming is the last read hasNode makes), then fails every lookup. The
// path verb's end-naming lookup is the one that runs after that conclusion.
type endLookupFailsAfterWalk struct {
	store.Backend
	cause  error
	walked bool
}

func (f *endLookupFailsAfterWalk) Incoming(qn string) ([]store.Relationship, error) {
	f.walked = true
	return f.Backend.Incoming(qn)
}

func (f *endLookupFailsAfterWalk) FindExact(name string) ([]store.Element, error) {
	if f.walked {
		return nil, f.cause
	}
	return f.Backend.FindExact(name)
}

// A path whose end is unknown is answered as unresolved only when the lookup
// that names the end succeeds; a failed lookup is the store error, not an answer.
func TestPathSurfacesEndLookupError(t *testing.T) {
	cause := errors.New("end lookup unreadable")
	e := New(&endLookupFailsAfterWalk{Backend: seededStore(t), cause: cause}, Options{})
	out, err := e.Query(context.Background(), QueryRequest{
		Action: "path", Query: "ghost.node", Args: map[string]any{"to": "phantom.node"},
	})
	if !errors.Is(err, cause) {
		t.Fatalf("Query err = %v (answer %v), want the store error", err, out)
	}
}
