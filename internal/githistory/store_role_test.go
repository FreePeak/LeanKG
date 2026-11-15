package githistory

import (
	"context"
	"errors"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// historyOnly implements exactly the six store methods githistory calls. The
// ones History and CoChange never reach return an error, so an accidental call
// fails the test. Passing it proves the dependency is narrowed: it would not
// compile against store.Backend.
type historyOnly struct {
	out map[string][]store.Relationship
}

var errNotCalled = errors.New("not used by this test")

func (h *historyOnly) Outgoing(_ context.Context, source string) ([]store.Relationship, error) {
	return h.out[source], nil
}

func (h *historyOnly) FindExact(context.Context, string) ([]store.Element, error) {
	return nil, errNotCalled
}

func (h *historyOnly) KVGet(context.Context, string, string) (string, bool, error) {
	return "", false, errNotCalled
}

func (h *historyOnly) KVSet(context.Context, string, string, string) error {
	return errNotCalled
}

func (h *historyOnly) UpsertElements(context.Context, []store.Element) error {
	return errNotCalled
}

func (h *historyOnly) UpsertRelationships(context.Context, []store.Relationship) error {
	return errNotCalled
}

func TestHistoryOverNarrowedStore(t *testing.T) {
	st := &historyOnly{out: map[string][]store.Relationship{
		"git:file:a.go": {{
			Source:   "git:file:a.go",
			Target:   "git:commit:abc",
			RelType:  "modified_in",
			Metadata: map[string]any{"sha": "abc", "date": "2026-01-02T10:00:00Z", "subject": "add a"},
		}},
	}}

	got, err := History(context.Background(), st, "a.go", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 1 || got[0].SHA != "abc" || got[0].Subject != "add a" {
		t.Fatalf("history = %+v; want the one modified_in change", got)
	}
}
