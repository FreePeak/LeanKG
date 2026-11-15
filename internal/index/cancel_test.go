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

func (s *cancelAfterWrites) UpsertElements(els []store.Element) error {
	s.writes++
	if s.writes == s.after {
		s.cancel()
	}
	return s.Backend.UpsertElements(els)
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
	if err := st.Migrate(); err != nil {
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
