package summarize

import (
	"context"
	"errors"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// indexOnly implements exactly the store methods summarize calls. Passing it
// proves the dependency is narrowed: it would not compile against
// store.Backend. An empty index with DryRun makes Run a read-only pass.
type indexOnly struct {
	filesCalls int
}

var errNotCalled = errors.New("not used by this test")

func (x *indexOnly) Files(context.Context) ([]store.FileRecord, error) {
	x.filesCalls++
	return nil, nil
}

func (x *indexOnly) FindExact(context.Context, string) ([]store.Element, error) {
	return nil, errNotCalled
}

func (x *indexOnly) Incoming(context.Context, string) ([]store.Relationship, error) {
	return nil, errNotCalled
}

func (x *indexOnly) KVGet(context.Context, string, string) (string, bool, error) {
	return "", false, errNotCalled
}

func (x *indexOnly) KVSet(context.Context, string, string, string) error {
	return errNotCalled
}

func (x *indexOnly) SummaryGet(context.Context, string) (store.FileSummary, bool, error) {
	return store.FileSummary{}, false, errNotCalled
}

func (x *indexOnly) SummaryUpsert(context.Context, store.FileSummary) error {
	return errNotCalled
}

func (x *indexOnly) DeleteByFile(context.Context, string) error {
	return errNotCalled
}

func (x *indexOnly) UpsertElements(context.Context, []store.Element) error {
	return errNotCalled
}

func (x *indexOnly) UpsertRelationships(context.Context, []store.Relationship) error {
	return errNotCalled
}

func (x *indexOnly) UpsertFiles(context.Context, []store.FileRecord) error {
	return errNotCalled
}

func TestRunDryRunOverNarrowedStore(t *testing.T) {
	st := &indexOnly{}

	if _, err := Run(context.Background(), st, Options{ProjectDir: t.TempDir(), DryRun: true}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.filesCalls != 1 {
		t.Fatalf("Files called %d times; want 1", st.filesCalls)
	}
}
