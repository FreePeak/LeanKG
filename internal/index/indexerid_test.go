//go:build !tstree

package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestIndexerChangeInvalidatesTheIndex pins the reconciliation's missing input.
//
// The defect, found by measuring retrieval after a change to the EXTRACTOR
// (wave 9 -> wave 10 of the dogfood loop). `IndexDirWith` skips a file when its
// size+mtime match the stored record, or when its SHA-256 matches ContentHash.
// Both are properties of the SOURCE. So a change to the code that READS the
// source is invisible: the files are byte-identical, the run reports
// `files=15 skipped=842`, and the store keeps element bodies extracted by the
// OLD extractor while `doctor --deep` passes index-freshness.
//
// Measured, on this repository: adding the doc comment to an element's stored
// content left 842 of 857 files unvisited, and the retrieval bench moved from
// 65/84 in-pool to 64/84 — because the corpus was quietly half-old. The only
// cure the CLI offered was `rm -rf .leankg`, which is not a cure, it is a
// coincidence.
//
// An extractor identity belongs in the store exactly as `ChunkerVersion` belongs
// on the embedding stamp: a change to the pipeline that produces the data is a
// REBUILD directive, never a no-op. The content hash answers "did the file
// change?"; it can never answer "would we extract it differently?", and the
// index needs both.
func TestIndexerChangeInvalidatesTheIndex(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "repo")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	storeAt := func(d string) *store.Store {
		t.Helper()
		st, err := store.Open(filepath.Join(d, ".leankg", "leankg.db"), store.RW)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		if err := st.Migrate(); err != nil {
			t.Fatal(err)
		}
		return st
	}
	write(filepath.Join(src, "a.go"), "package p\n\n// Alpha does the first thing.\nfunc Alpha() int { return 1 }\n")

	st := storeAt(src)
	// First run: indexed, and the extractor identity is recorded.
	res, err := IndexDir(context.Background(), st, src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 1 {
		t.Fatalf("first run indexed %d files, want 1: %+v", res.Files, res)
	}
	if _, ok, err := st.KVGet(indexerNamespace, indexerVersionKey); err != nil || !ok {
		t.Fatalf("the index must record the extractor identity (err=%v ok=%v)", err, ok)
	}

	// The sources do not change. An index built by a DIFFERENT extractor must
	// still be rebuilt, because the stored elements would differ.
	if err := st.KVSet(indexerNamespace, indexerVersionKey, "an-older-extractor"); err != nil {
		t.Fatal(err)
	}
	res, err = IndexDir(context.Background(), st, src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped == 1 {
		t.Fatalf("a changed extractor must invalidate the index, but the only file was SKIPPED: %+v", res)
	}
	if res.Files != 1 {
		t.Fatalf("the stale file must be re-extracted, indexed %d: %+v", res.Files, res)
	}
	// And the identity is restored to the running one, so the next run against
	// THIS build is a normal incremental.
	got, ok, err := st.KVGet(indexerNamespace, indexerVersionKey)
	if err != nil || !ok {
		t.Fatalf("identity unreadable after rebuild (err=%v ok=%v)", err, ok)
	}
	if got == "an-older-extractor" {
		t.Fatal("the rebuild must record the running extractor, not leave the stale identity")
	}
}

// TestSameExtractorKeepsIncrementality is the other half, and the reason the
// gate is an identity rather than a "force" flag: a normal run must still skip
// untouched files, or every `leankg index` becomes a full rebuild.
func TestSameExtractorKeepsIncrementality(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "repo")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.go"),
		[]byte("package p\n\nfunc Alpha() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(src, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := IndexDir(context.Background(), st, src); err != nil {
		t.Fatal(err)
	}
	res, err := IndexDir(context.Background(), st, src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Files != 0 {
		t.Fatalf("an unchanged file under the SAME extractor must be skipped, got %+v", res)
	}
}
