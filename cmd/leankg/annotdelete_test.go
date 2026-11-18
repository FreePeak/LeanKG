package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/annot"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestDeleteAnnotationRoundTrip is the guard for the CLI gap this verb
// closes: annotate/link/search existed, annot.Delete existed, and NOTHING
// called it — so an annotation on the wrong element lived in the kv document
// until someone edited the store by hand.
func TestDeleteAnnotationRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{ProjectDir: dir, Engine: store.EngineSQLite, PGURL: "", DBPath: "", Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	const el = "pkg/a.go::Do"
	if _, err := annot.Annotate(context.Background(), st, el, "wrong description", nil, nil); err != nil {
		t.Fatal(err)
	}
	// A search finds it BEFORE the delete (so the delete is what changes the
	// answer, not a search that never matched).
	hits, err := annot.Search(context.Background(), st, "wrong description")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("search before delete = %d hits, want 1", len(hits))
	}
	// Deleting an element that has no annotation is a no-op, not an error:
	// the CLI reports it and exits 0.
	if err := annot.Delete(context.Background(), st, "pkg/not_there.go::Nope"); err != nil {
		t.Errorf("delete of an absent element failed: %v", err)
	}
	if err := annot.Delete(context.Background(), st, el); err != nil {
		t.Fatal(err)
	}
	hits, err = annot.Search(context.Background(), st, "wrong description")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("search after delete = %d hits, want 0", len(hits))
	}
	// The kv document must not keep a stale null row behind.
	raw, ok, err := st.KVGet(context.Background(), "annotation", "records")
	if err != nil {
		t.Fatal(err)
	}
	if ok && string(raw) != "[]" {
		t.Errorf("annotation document = %s, want [] after removing the only row", raw)
	}
}

var _ = filepath.Join

// TestDeleteAnnotationParsesFlagsOnce guards a double parse: the flags were
// parsed once and then again by the positional reader, so a bad flag or -h
// printed the usage twice. Each case must print the usage exactly once.
func TestDeleteAnnotationParsesFlagsOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"bad flag", []string{"--no-such-flag"}, 2},
		{"help", []string{"-h"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runCLI(t, append([]string{"delete-annotation"}, tc.args...)...)
			if code != tc.code {
				t.Errorf("exit = %d, want %d (stderr: %s)", code, tc.code, stderr)
			}
			if n := strings.Count(stderr, "Usage of delete-annotation"); n != 1 {
				t.Errorf("usage printed %d times, want 1 (stderr: %s)", n, stderr)
			}
		})
	}
}
