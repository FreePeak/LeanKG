package index

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// cancelAfterWrites is a store whose element writes cancel a run after the
// given number of files, so a test can stop an index mid-package.
type cancelAfterWrites struct {
	store.Backend
	after  int
	cancel context.CancelFunc
	writes int
}

func (s *cancelAfterWrites) UpsertElements(ctx context.Context, els []store.Element) error {
	s.writes++
	if s.writes == s.after {
		s.cancel()
	}
	return s.Backend.UpsertElements(ctx, els)
}

// TestIndexStopsMidPackage: a run cancelled while one large package is being
// written stops within a bounded number of files. Before the per-file check the
// write loop only looked at ctx once per directory, so all files of the package
// were written after the cancel.
func TestIndexStopsMidPackage(t *testing.T) {
	dir := t.TempDir()
	const files = 2000
	for i := range files {
		src := fmt.Sprintf("package big\n\nfunc F%04d() {}\n", i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.go", i)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const cancelAt = 5
	w := &cancelAfterWrites{Backend: st, after: cancelAt, cancel: cancel}
	_, err = IndexDir(ctx, w, dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("IndexDir error = %v, want context.Canceled", err)
	}
	if w.writes > cancelAt+1 {
		t.Fatalf("wrote %d of %d files after cancelling at file %d; the write loop does not check ctx per file", w.writes, files, cancelAt)
	}
}

// cancelInDeleteByFile cancels the run right after the k-th DeleteByFile has
// committed: the point where a file's old elements are gone and its new ones are
// not written yet.
type cancelInDeleteByFile struct {
	store.Backend
	after  int
	cancel context.CancelFunc
	calls  int
}

func (s *cancelInDeleteByFile) DeleteByFile(ctx context.Context, path string) error {
	s.calls++
	err := s.Backend.DeleteByFile(ctx, path)
	if s.calls == s.after {
		s.cancel()
	}
	return err
}

// indexShape is what a convergence check compares: the counts of one store.
type indexShape struct{ elements, relationships, files int }

func shapeOf(t *testing.T, st store.Backend) indexShape {
	t.Helper()
	ctx := context.Background()
	e, err := st.ElementCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.RelationshipCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.FileCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return indexShape{e, r, f}
}

// openIndexStore opens a fresh migrated store under its own directory.
func openIndexStore(t *testing.T) store.Backend {
	t.Helper()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(t.TempDir(), ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// writePackage writes a four-file package whose files call one another, so
// relationships cross file boundaries. tag changes every function name, so a
// second call makes every file changed.
func writePackage(t *testing.T, dir, tag string) {
	t.Helper()
	files := map[string]string{
		"a.go":     "package p\n\nfunc Alpha" + tag + "() {\n\tBravo" + tag + "()\n}\n",
		"b.go":     "package p\n\nfunc Bravo" + tag + "() {\n\tx := 1\n\t_ = x\n}\n",
		"c.go":     "package p\n\nfunc Charlie" + tag + "() {\n\tAlpha" + tag + "()\n\tBravo" + tag + "()\n}\n",
		"d.go":     "package p\n\nfunc Delta" + tag + "() {\n\tCharlie" + tag + "()\n}\n",
		"sub/x.go": "package sub\n\nfunc Xray" + tag + "() {\n\tx := 1\n\t_ = x\n}\n",
		"sub/y.go": "package sub\n\nfunc Yankee" + tag + "() {\n\tXray" + tag + "()\n}\n",
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCancelMidFileConvergesToUninterruptedRun: a run cancelled between two
// files of one package leaves a store whose records all have their elements,
// and a plain rerun ends at the same counts as a run that was never cancelled.
// Call edges are resolved against the files of the same run, so a rerun that
// skipped the committed files would lose the edges into them.
func TestCancelMidFileConvergesToUninterruptedRun(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, dir, "V1")
	reference := openIndexStore(t)
	if _, err := IndexDir(context.Background(), reference, dir); err != nil {
		t.Fatal(err)
	}
	writePackage(t, dir, "V2")
	want := func() indexShape {
		st := openIndexStore(t)
		if _, err := IndexDir(context.Background(), st, dir); err != nil {
			t.Fatal(err)
		}
		return shapeOf(t, st)
	}()

	for after := 1; after <= 6; after++ {
		t.Run(fmt.Sprintf("cancel_after_file_%d", after), func(t *testing.T) {
			st := openIndexStore(t)
			writePackage(t, dir, "V1")
			if _, err := IndexDir(context.Background(), st, dir); err != nil {
				t.Fatal(err)
			}
			writePackage(t, dir, "V2")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &cancelInDeleteByFile{Backend: st, after: after, cancel: cancel}
			if _, err := IndexDir(ctx, w, dir); !errors.Is(err, context.Canceled) {
				t.Fatalf("interrupted IndexDir error = %v, want context.Canceled", err)
			}
			assertRecordsHaveElements(t, st)
			if _, err := IndexDir(context.Background(), st, dir); err != nil {
				t.Fatalf("rerun: %v", err)
			}
			if got := shapeOf(t, st); got != want {
				t.Fatalf("rerun shape = %+v, want uninterrupted %+v", got, want)
			}
		})
	}
}

// assertRecordsHaveElements checks the store invariant a cancel must keep: a
// file record is never present without the elements that record describes.
func assertRecordsHaveElements(t *testing.T, st store.Backend) {
	t.Helper()
	ctx := context.Background()
	files, err := st.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	els, err := st.Elements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, e := range els {
		have[e.FilePath] = true
	}
	for _, f := range files {
		if !have[f.Path] {
			t.Errorf("file record %s has no elements after the cancel", f.Path)
		}
	}
}

// SupportedFiles walks the tree under the caller's ctx: a cancelled ctx (SIGINT,
// or the doctor probe deadline) stops the walk with that cancel.
func TestSupportedFilesStopsOnCancelledCtx(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, dir, "pkg")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := SupportedFiles(ctx, dir, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("SupportedFiles under a cancelled ctx: err = %v, want context.Canceled", err)
	}
}

// TestCancelRollsBackSpecialistFiles: a specialist file (an AndroidManifest here)
// is written during extraction, before the directory's ordinary writes begin.
// A run cancelled part-way through that directory must roll the specialist file
// back with the other changed files it finished, so a rerun re-indexes it. The
// rollback used to cover only the files of the write loop.
func TestCancelRollsBackSpecialistFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"AndroidManifest.xml": `<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="com.example.app"/>`,
		"a.go":                "package p\n\nfunc Alpha() {}\n",
		"b.go":                "package p\n\nfunc Bravo() {}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := openIndexStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// DeleteByFile calls: 1 is the specialist's (written during extraction),
	// 2 is a.go's (the first ordinary write). Cancelling after 2 stops the run
	// before b.go, so the directory is cancelled part-way through.
	w := &cancelInDeleteByFile{Backend: st, after: 2, cancel: cancel}
	if _, err := IndexDir(ctx, w, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("IndexDir error = %v, want context.Canceled", err)
	}
	if got := shapeOf(t, st).files; got != 0 {
		t.Fatalf("file records after cancelled directory = %d, want 0: the specialist and a.go were not rolled back", got)
	}
}

// failFileRecordDelete fails the first DeleteFileRecord of one path, so a
// rollback stops between removing a file's record and its elements.
type failFileRecordDelete struct {
	store.Backend
	path   string
	failed bool
}

func (s *failFileRecordDelete) DeleteFileRecord(ctx context.Context, path string) error {
	if path == s.path && !s.failed {
		s.failed = true
		return errors.New("injected file record delete failure")
	}
	return s.Backend.DeleteFileRecord(ctx, path)
}

// writeThreeFiles writes three Go files of one package with distinct symbols.
func writeThreeFiles(t *testing.T, dir string) {
	t.Helper()
	for name, fn := range map[string]string{"a.go": "Alpha", "b.go": "Bravo", "c.go": "Charlie"} {
		src := "package p\n\nfunc " + fn + "() {}\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCancelRollbackCountsNoStoredFiles pins that a directory rolled back after
// its files were written reports none of them. The write loop counted each file
// before the rollback removed it, so a cancelled run reported files it did not
// store.
func TestCancelRollbackCountsNoStoredFiles(t *testing.T) {
	dir := t.TempDir()
	writeThreeFiles(t, dir)
	st := openIndexStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancelling during b's write leaves a and b written; the check before c
	// then rolls the directory back.
	w := &cancelInDeleteByFile{Backend: st, after: 2, cancel: cancel}
	res, err := IndexDir(ctx, w, dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("IndexDir error = %v, want context.Canceled", err)
	}
	if res.Files != 0 || res.Elements != 0 || res.Relationships != 0 {
		t.Fatalf("result after rollback = Files %d, Elements %d, Relationships %d; want all 0 (nothing is stored)",
			res.Files, res.Elements, res.Relationships)
	}
	if got := shapeOf(t, st).files; got != 0 {
		t.Fatalf("file records after rollback = %d, want 0", got)
	}
}

// TestRollbackRecordFailureConvergesOnRerun pins that a rollback which fails to
// delete one file's record still leaves a rerun able to restore every file. The
// record must go before the elements: a record that outlived its elements read
// as current and was skipped forever, with nothing indexed.
func TestRollbackRecordFailureConvergesOnRerun(t *testing.T) {
	dir := t.TempDir()
	writeThreeFiles(t, dir)
	reference := openIndexStore(t)
	if _, err := IndexDir(context.Background(), reference, dir); err != nil {
		t.Fatal(err)
	}
	want := shapeOf(t, reference)

	st := openIndexStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancelling during b's write rolls back a and b. The first record delete
	// of a.go fails, so the rollback stops after it.
	w := &cancelInDeleteByFile{Backend: &failFileRecordDelete{Backend: st, path: "a.go"}, after: 2, cancel: cancel}
	if _, err := IndexDir(ctx, w, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted IndexDir error = %v, want context.Canceled", err)
	}
	assertRecordsHaveElements(t, st)
	if _, err := IndexDir(context.Background(), st, dir); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if got := shapeOf(t, st); got != want {
		t.Fatalf("rerun shape = %+v, want uninterrupted %+v", got, want)
	}
}
