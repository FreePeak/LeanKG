package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// spendAfterWrites closes the budget channel on the n-th element write, so a
// test can spend the budget inside a directory.
type spendAfterWrites struct {
	store.Backend
	after  int
	spent  chan struct{}
	writes int
}

func (s *spendAfterWrites) UpsertElements(ctx context.Context, els []store.Element) error {
	s.writes++
	if s.writes == s.after {
		close(s.spent)
	}
	return s.Backend.UpsertElements(ctx, els)
}

// spentBudget is a context whose budget is already spent.
func spentBudget() context.Context {
	spent := make(chan struct{})
	close(spent)
	return WithBudget(context.Background(), spent)
}

// kvValue reads one index KV entry ("" when it is absent).
func kvValue(t *testing.T, st store.Backend, key string) string {
	t.Helper()
	v, _, err := st.KVGet(context.Background(), rootKVNamespace, key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// writeThreeDirectories is writePackage plus a third directory "t", so a
// re-extraction can stop between directories that are not the last one.
func writeThreeDirectories(t *testing.T, dir string) {
	t.Helper()
	writePackage(t, dir, "V1")
	if err := os.MkdirAll(filepath.Join(dir, "t"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"t/p.go": "package t\n\nfunc Pa() {}\n",
		"t/q.go": "package t\n\nfunc Qu() {}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A budget spent while a directory is being written lets that directory finish:
// the run returns ErrBudget with all four files of "." kept, and a rerun ends at
// the uninterrupted shape.
func TestBudgetSpentMidDirectoryFinishesIt(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, dir, "V1")
	reference := openIndexStore(t)
	if _, err := IndexDir(context.Background(), reference, dir); err != nil {
		t.Fatal(err)
	}
	want := shapeOf(t, reference)

	st := openIndexStore(t)
	w := &spendAfterWrites{Backend: st, after: 1, spent: make(chan struct{})}
	if _, err := IndexDir(WithBudget(context.Background(), w.spent), w, dir); !errors.Is(err, ErrBudget) {
		t.Fatalf("budgeted IndexDir error = %v, want ErrBudget", err)
	}
	if got := shapeOf(t, st).files; got != 4 {
		t.Fatalf("files after the budget stop = %d, want the 4 files of the directory in progress", got)
	}
	assertRecordsHaveElements(t, st)
	res, err := IndexDir(context.Background(), st, dir)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if res.Files != 2 {
		t.Fatalf("rerun indexed %d files, want the 2 of the directory the budget did not reach", res.Files)
	}
	if got := shapeOf(t, st); got != want {
		t.Fatalf("rerun shape = %+v, want uninterrupted %+v", got, want)
	}
}

// A store whose budget is spent before every run still makes progress: each run
// finishes the first directory it has to write, and the store converges.
func TestSpentBudgetStillIndexesOneDirectoryPerRun(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, dir, "V1")
	reference := openIndexStore(t)
	if _, err := IndexDir(context.Background(), reference, dir); err != nil {
		t.Fatal(err)
	}
	want := shapeOf(t, reference)

	st := openIndexStore(t)
	res, err := IndexDir(spentBudget(), st, dir)
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("run 1 error = %v, want ErrBudget", err)
	}
	if res.Files != 4 {
		t.Fatalf("run 1 indexed %d files, want the first directory (4)", res.Files)
	}
	res, err = IndexDir(spentBudget(), st, dir)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if res.Files != 2 || res.Skipped != 4 {
		t.Fatalf("run 2 files=%d skipped=%d, want files=2 skipped=4", res.Files, res.Skipped)
	}
	if got := shapeOf(t, st); got != want {
		t.Fatalf("shape = %+v, want uninterrupted %+v", got, want)
	}
}

// An extractor upgrade resumes across budget stops: the run that re-extracts
// the first directory records a cursor, the next run starts after it, and the
// version is stamped only when the whole store has been re-extracted.
func TestReextractResumesAcrossBudgetStops(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, dir, "V1")
	reference := openIndexStore(t)
	if _, err := IndexDir(context.Background(), reference, dir); err != nil {
		t.Fatal(err)
	}
	want := shapeOf(t, reference)

	st := openIndexStore(t)
	if _, err := IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(context.Background(), rootKVNamespace, extractorKVKey, "1"); err != nil {
		t.Fatal(err)
	}

	res, err := IndexDir(spentBudget(), st, dir)
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("run 1 error = %v, want ErrBudget", err)
	}
	if res.Files != 4 {
		t.Fatalf("run 1 re-extracted %d files, want the first directory (4)", res.Files)
	}
	res, err = IndexDir(spentBudget(), st, dir)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if res.Files != 2 {
		t.Fatalf("run 2 re-extracted %d files, want the 2 of the directory after the cursor", res.Files)
	}
	if got := kvValue(t, st, extractorKVKey); got != extractorVersion {
		t.Fatalf("extractor version after the upgrade = %q, want %q", got, extractorVersion)
	}
	if got := kvValue(t, st, reextractKVKey); got != "" {
		t.Fatalf("reextract cursor after the upgrade = %q, want it cleared", got)
	}
	if got := shapeOf(t, st); got != want {
		t.Fatalf("shape = %+v, want uninterrupted %+v", got, want)
	}
	res, err = IndexDir(context.Background(), st, dir)
	if err != nil {
		t.Fatalf("run 3: %v", err)
	}
	if res.Files != 0 {
		t.Fatalf("run 3 re-extracted %d files after the upgrade, want 0", res.Files)
	}
}

// The cursor only moves forward. Once "sub" is finished, an edit in "." is
// redone by the next budgeted run, and the run after it does not re-extract
// "sub" again (a cursor moved back to "." would make it do so).
func TestReextractCursorNeverMovesBack(t *testing.T) {
	dir := t.TempDir()
	writeThreeDirectories(t, dir)
	st := openIndexStore(t)
	if _, err := IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(context.Background(), rootKVNamespace, extractorKVKey, "1"); err != nil {
		t.Fatal(err)
	}

	// Writes 1-4 are ".", 5-6 are "sub": the budget is spent on the last write of
	// "sub", so "sub" finishes and "t" waits.
	w := &spendAfterWrites{Backend: st, after: 6, spent: make(chan struct{})}
	res, err := IndexDir(WithBudget(context.Background(), w.spent), w, dir)
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("run A error = %v, want ErrBudget", err)
	}
	if res.Files != 6 {
		t.Fatalf("run A re-extracted %d files, want 6 (\".\" and \"sub\")", res.Files)
	}
	if got := kvValue(t, st, reextractKVKey); got != extractorVersion+"\nsub" {
		t.Fatalf("cursor after run A = %q, want %q", got, extractorVersion+"\nsub")
	}

	edited := "package p\n\nfunc AlphaV1() {\n\tBravoV1()\n}\n\n// edited after the cursor passed sub\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = IndexDir(spentBudget(), st, dir)
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("run B error = %v, want ErrBudget", err)
	}
	if res.Files != 1 {
		t.Fatalf("run B re-extracted %d files, want only the edited a.go (1)", res.Files)
	}
	if got := kvValue(t, st, reextractKVKey); got != extractorVersion+"\nsub" {
		t.Fatalf("cursor after run B = %q, want it to stay at %q", got, extractorVersion+"\nsub")
	}

	res, err = IndexDir(context.Background(), st, dir)
	if err != nil {
		t.Fatalf("run C: %v", err)
	}
	if res.Files != 2 {
		t.Fatalf("run C re-extracted %d files, want the 2 of \"t\" only (\"sub\" must not be re-extracted)", res.Files)
	}
}

// A cursor recorded under another extractor version says nothing about this one,
// so an unbudgeted run re-extracts every file.
func TestReextractCursorOfAnotherVersionIsIgnored(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, dir, "V1")
	st := openIndexStore(t)
	if _, err := IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(context.Background(), rootKVNamespace, extractorKVKey, "1"); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(context.Background(), rootKVNamespace, reextractKVKey, "0\nzzz"); err != nil {
		t.Fatal(err)
	}
	res, err := IndexDir(context.Background(), st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 6 {
		t.Fatalf("run re-extracted %d files, want all 6: a cursor of another extractor version must be ignored", res.Files)
	}
}

// Guard: a signal that arrives while the budget is spent still cancels the run.
// Mid-directory the cut directory is rolled back; at the boundary, after "." has
// finished, the run must still report Canceled rather than ErrBudget.
func TestSignalDuringSpentBudgetCancelsWithinAFile(t *testing.T) {
	cases := []struct {
		name      string
		after     int
		wantFiles int
	}{
		{"cancel_mid_directory", 2, 0},
		{"cancel_at_directory_boundary", 4, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writePackage(t, dir, "V1")
			reference := openIndexStore(t)
			if _, err := IndexDir(context.Background(), reference, dir); err != nil {
				t.Fatal(err)
			}
			want := shapeOf(t, reference)

			st := openIndexStore(t)
			ctx, cancel := context.WithCancel(spentBudget())
			defer cancel()
			w := &cancelAfterWrites{Backend: st, after: tc.after, cancel: cancel}
			if _, err := IndexDir(ctx, w, dir); !errors.Is(err, context.Canceled) || errors.Is(err, ErrBudget) {
				t.Fatalf("IndexDir error = %v, want context.Canceled", err)
			}
			if got := shapeOf(t, st).files; got != tc.wantFiles {
				t.Fatalf("files after the cancel = %d, want %d", got, tc.wantFiles)
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
